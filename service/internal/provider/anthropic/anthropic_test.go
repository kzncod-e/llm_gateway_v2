package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"service/internal/provider"
)

func req() *provider.ChatRequest {
	return &provider.ChatRequest{Model: "claude-x", MaxTokens: 256, Messages: []provider.Message{
		{Role: "system", Content: "sys1"}, {Role: "system", Content: "sys2"},
		{Role: "user", Content: "a"}, {Role: "user", Content: "b"}, {Role: "assistant", Content: "c"}, {Role: "user", Content: "d"},
	}}
}

func TestChatCompletion(t *testing.T) {
	var got wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") != APIVersion {
			t.Errorf("bad request headers")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"Hi "},{"type":"text","text":"there"}],
		"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}`)
	}))
	defer srv.Close()

	resp, err := New().ChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL, APIKey: "k"}, req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hi there" || resp.FinishReason != "length" {
		t.Fatalf("resp %+v", resp)
	}
	u := resp.Usage
	if u.InputTokens != 10 || u.CachedInputTokens != 100 || u.CacheWriteTokens != 20 || u.OutputTokens != 4 || u.Total() != 134 {
		t.Fatalf("usage %+v", u)
	}
	if got.System != "sys1\n\nsys2" || len(got.Messages) != 3 || got.Messages[0].Content != "a\n\nb" || got.MaxTokens != 256 {
		t.Fatalf("wire request %+v", got)
	}
}

func TestOverloaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(529)
		fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	}))
	defer srv.Close()
	_, err := New().ChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	pe := provider.AsError(err)
	if pe.Kind != provider.KindOverloaded || pe.ProviderCode != "overloaded_error" || !provider.Retryable(err) {
		t.Fatalf("got %+v", pe)
	}
}

const streamBody = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":25,"output_tokens":1,"cache_read_input_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`

func collect(t *testing.T, s provider.Stream) (string, string, error) {
	var sb strings.Builder
	var fr string
	for {
		c, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return sb.String(), fr, nil
		}
		if err != nil {
			return sb.String(), fr, err
		}
		sb.WriteString(c.Delta)
		if c.FinishReason != "" {
			fr = c.FinishReason
		}
	}
}

func TestStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, streamBody)
	}))
	defer srv.Close()
	s, err := New().StreamChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	text, fr, err := collect(t, s)
	if err != nil || text != "Hello!" || fr != "stop" {
		t.Fatalf("got %q %q %v", text, fr, err)
	}
	if u := s.Usage(); u.Status != provider.UsageReported || u.InputTokens != 25 || u.OutputTokens != 15 {
		t.Fatalf("usage %+v", u)
	}
}

func TestStreamErrorEventAndTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.SplitN(streamBody, "event: content_block_stop", 2)[0])
		if r.URL.Query().Get("x") == "" {
			fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
		}
	}))
	defer srv.Close()
	s, _ := New().StreamChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	_, _, err := collect(t, s)
	if provider.AsError(err).Kind != provider.KindOverloaded {
		t.Fatalf("expected overloaded error event, got %v", err)
	}
	u := s.Usage()
	if u.Status != provider.UsageUnknown || u.InputTokens != 25 {
		t.Fatalf("partial usage must be unknown but keep known input: %+v", u)
	}
}
