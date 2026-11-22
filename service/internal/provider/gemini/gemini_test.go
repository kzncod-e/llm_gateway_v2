package gemini

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
	return &provider.ChatRequest{Model: "gemini-2.5-flash", MaxTokens: 64, Messages: []provider.Message{
		{Role: "system", Content: "sys"}, {Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "user", Content: "q2"},
	}}
}

func TestChatCompletion(t *testing.T) {
	var got wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-flash:generateContent" || r.Header.Get("x-goog-api-key") != "g" {
			t.Errorf("bad request %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking...","thought":true},{"text":"Answer"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3,"thoughtsTokenCount":9,"cachedContentTokenCount":2,"totalTokenCount":24},"modelVersion":"gemini-2.5-flash","responseId":"r1"}`)
	}))
	defer srv.Close()
	resp, err := New().ChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL, APIKey: "g"}, req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Answer" || resp.FinishReason != "stop" || resp.ID != "r1" {
		t.Fatalf("resp %+v", resp)
	}
	u := resp.Usage
	if u.InputTokens != 10 || u.CachedInputTokens != 2 || u.OutputTokens != 12 || u.ReasoningTokens != 9 || u.Total() != 24 {
		t.Fatalf("usage %+v", u)
	}
	if got.SystemInstruction == nil || got.SystemInstruction.Parts[0].Text != "sys" || len(got.Contents) != 3 || got.Contents[1].Role != "model" || got.GenerationConfig.MaxOutputTokens != 64 {
		t.Fatalf("wire %+v", got)
	}
}

func TestErrorFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`)
	}))
	defer srv.Close()
	_, err := New().ChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	pe := provider.AsError(err)
	if pe.Kind != provider.KindRateLimited || pe.ProviderCode != "RESOURCE_EXHAUSTED" || pe.Message != "quota" {
		t.Fatalf("got %+v", pe)
	}
}

func TestStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-flash:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Errorf("bad stream url %s", r.URL)
		}
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hel\"}],\"role\":\"model\"}}],\"usageMetadata\":{\"promptTokenCount\":5}}\r\n\r\n")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"totalTokenCount\":7}}\r\n\r\n")
	}))
	defer srv.Close()
	s, err := New().StreamChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var sb strings.Builder
	var fr string
	for {
		c, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		sb.WriteString(c.Delta)
		if c.FinishReason != "" {
			fr = c.FinishReason
		}
	}
	if sb.String() != "Hello" || fr != "stop" {
		t.Fatalf("got %q %q", sb.String(), fr)
	}
	if u := s.Usage(); u.Status != provider.UsageReported || u.InputTokens != 5 || u.OutputTokens != 2 {
		t.Fatalf("usage %+v", u)
	}
}

func TestStreamWithoutFinishIsInterrupted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hel\"}]}}],\"usageMetadata\":{\"promptTokenCount\":5}}\n\n")
	}))
	defer srv.Close()
	s, _ := New().StreamChatCompletion(context.Background(), provider.Config{BaseURL: srv.URL}, req())
	defer s.Close()
	if _, err := s.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recv(); provider.AsError(err).Kind != provider.KindStreamInterrupted {
		t.Fatalf("expected interruption, got %v", err)
	}
	if s.Usage().Status != provider.UsageUnknown {
		t.Fatal("usage must be unknown")
	}
}
