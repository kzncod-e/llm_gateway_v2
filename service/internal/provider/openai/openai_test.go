package openai

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
	"time"

	"service/internal/provider"
)

func req() *provider.ChatRequest {
	return &provider.ChatRequest{Model: "gpt-4o", MaxTokens: 100, Messages: []provider.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}}}
}

func TestChatCompletionSuccess(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"id":"chatcmpl-1","model":"gpt-4o-2024","choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":20,"completion_tokens":7,"total_tokens":27,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":2}}}`)
	}))
	defer srv.Close()

	resp, err := New().ChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL, APIKey: "sk-test"}, req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello" || resp.FinishReason != "stop" || resp.Model != "gpt-4o-2024" {
		t.Fatalf("unexpected response %+v", resp)
	}
	u := resp.Usage
	if u.Status != provider.UsageReported || u.InputTokens != 15 || u.CachedInputTokens != 5 || u.OutputTokens != 7 || u.ReasoningTokens != 2 || u.Total() != 27 {
		t.Fatalf("unexpected usage %+v", u)
	}
	if got["max_completion_tokens"].(float64) != 100 || len(got["messages"].([]any)) != 2 {
		t.Fatalf("unexpected wire request %v", got)
	}
	if _, ok := got["stream"]; ok {
		t.Fatal("stream must be omitted for non-streaming")
	}
}

func TestChatCompletionErrors(t *testing.T) {
	cases := []struct {
		status int
		kind   provider.ErrorKind
		retry  bool
	}{
		{429, provider.KindRateLimited, true},
		{500, provider.KindServer, true},
		{503, provider.KindServer, true},
		{400, provider.KindBadRequest, false},
		{401, provider.KindAuth, false},
		{404, provider.KindNotFound, false},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(c.status)
				fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error","code":"x"}}`)
			}))
			defer srv.Close()
			_, err := New().ChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
			pe := provider.AsError(err)
			if pe.Kind != c.kind || pe.StatusCode != c.status || pe.Message != "boom" || provider.Retryable(err) != c.retry {
				t.Fatalf("got %+v", pe)
			}
			if c.status == 429 && pe.RetryAfter != 2*time.Second {
				t.Fatalf("retry-after not parsed: %v", pe.RetryAfter)
			}
		})
	}
}

func TestTimeoutAndNetworkErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := New().ChatCompletion(ctx, provider.Config{BaseURL: srv.URL}, req())
	if provider.AsError(err).Kind != provider.KindTimeout {
		t.Fatalf("expected timeout, got %v", err)
	}

	_, err = New().ChatCompletion(context.Background(), provider.Config{BaseURL: "http://127.0.0.1:1"}, req())
	pe := provider.AsError(err)
	if pe.Kind != provider.KindNetwork || pe.Sent {
		t.Fatalf("expected unsent network error, got %+v", pe)
	}
}

func TestStream(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	s, err := New().StreamChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var text strings.Builder
	var finish string
	for {
		c, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(c.Delta)
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
	}
	if text.String() != "Hello" || finish != "stop" {
		t.Fatalf("got %q %q", text.String(), finish)
	}
	if u := s.Usage(); u.Status != provider.UsageReported || u.InputTokens != 3 || u.OutputTokens != 2 {
		t.Fatalf("usage %+v", u)
	}
	so := got["stream_options"].(map[string]any)
	if got["stream"] != true || so["include_usage"] != true {
		t.Fatalf("stream options not requested: %v", got)
	}
}

func TestStreamInterrupted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer srv.Close()
	s, err := New().StreamChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if c, err := s.Recv(); err != nil || c.Delta != "partial" {
		t.Fatalf("first chunk: %v %v", c, err)
	}
	_, err = s.Recv()
	if provider.AsError(err).Kind != provider.KindStreamInterrupted {
		t.Fatalf("expected interruption, got %v", err)
	}
	if s.Usage().Status != provider.UsageUnknown {
		t.Fatal("usage must be unknown for an interrupted stream")
	}
}
