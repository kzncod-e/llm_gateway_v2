// Package mockllm emulates the OpenAI, Anthropic and Gemini HTTP APIs for
// local development and end-to-end tests. It never calls real providers.
//
// Routes:
//
//	POST /openai/v1/chat/completions
//	POST /anthropic/v1/messages
//	POST /gemini/v1beta/models/{model}:generateContent | :streamGenerateContent?alt=sse
//	GET|POST /control   per-provider failure mode: {"openai":"ok|503|429|slow|drop"}
//
// Model names also inject failures: *fail-503*, *429*, *slow*, *drop-stream*.
package mockllm

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

type control struct {
	mu    sync.RWMutex
	modes map[string]string
}

func (c *control) mode(provider, model string) string {
	c.mu.RLock()
	m := c.modes[provider]
	c.mu.RUnlock()
	switch {
	case strings.Contains(model, "fail-503"):
		return "503"
	case strings.Contains(model, "429"):
		return "429"
	case strings.Contains(model, "slow"):
		return "slow"
	case strings.Contains(model, "drop-stream"):
		return "drop"
	}
	if m == "" {
		return "ok"
	}
	return m
}

var words = strings.Fields(`An LLM gateway centralizes access to model providers. It authenticates clients, applies rate limits and budgets,
routes each request to a healthy provider, retries transient failures with backoff, falls back when a provider is down,
and records token usage and cost for every attempt so teams can see exactly what they spend.`)

func reply(provider, model string) []string {
	out := []string{"[mock", provider + "/" + model + "]"}
	return append(out, words...)
}

func estimateTokens(chars int) int64 { return int64(math.Ceil(float64(chars) / 4)) }

// Handler returns the mock provider HTTP handler. delay is the pause between
// streamed chunks.
func Handler(delay time.Duration) http.Handler {
	ctl := &control{modes: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in map[string]string
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			ctl.mu.Lock()
			for k, v := range in {
				ctl.modes[k] = v
			}
			ctl.mu.Unlock()
		}
		ctl.mu.RLock()
		defer ctl.mu.RUnlock()
		_ = json.NewEncoder(w).Encode(ctl.modes)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("POST /openai/v1/chat/completions", openAIHandler(ctl, delay))
	mux.HandleFunc("POST /anthropic/v1/messages", anthropicHandler(ctl, delay))
	mux.HandleFunc("POST /gemini/v1beta/models/{call}", geminiHandler(ctl, delay))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func sse(w http.ResponseWriter) (func(event string, v any), bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	return func(event string, v any) {
		b, _ := json.Marshal(v)
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}, true
}

// failure applies a failure mode; it returns true when the response was written.
func failure(w http.ResponseWriter, r *http.Request, mode string, body func(status int, msg string) any) bool {
	switch mode {
	case "503":
		writeJSON(w, http.StatusServiceUnavailable, body(503, "mock provider unavailable"))
		return true
	case "429":
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, body(429, "mock rate limit"))
		return true
	case "500":
		writeJSON(w, http.StatusInternalServerError, body(500, "mock internal error"))
		return true
	case "slow":
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
			return true
		}
	}
	return false
}

func sleep(r *http.Request, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-r.Context().Done():
		return false
	}
}

// OpenAI -------------------------------------------------------------------------------

func openAIHandler(ctl *control, delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Stream        bool `json:"stream"`
			StreamOptions *struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		errBody := func(status int, msg string) any {
			return map[string]any{"error": map[string]any{"message": msg, "type": "server_error", "code": fmt.Sprint(status)}}
		}
		if r.Header.Get("Authorization") == "" {
			writeJSON(w, 401, errBody(401, "missing api key"))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model == "" {
			writeJSON(w, 400, errBody(400, "invalid request"))
			return
		}
		mode := ctl.mode("openai", req.Model)
		if failure(w, r, mode, errBody) {
			return
		}
		chars := 0
		for _, m := range req.Messages {
			chars += len(m.Content)
		}
		in, ws := estimateTokens(chars), reply("openai", req.Model)
		out := int64(len(ws))
		usage := map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out,
			"prompt_tokens_details": map[string]any{"cached_tokens": 0}, "completion_tokens_details": map[string]any{"reasoning_tokens": 0}}
		id := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
		if !req.Stream {
			writeJSON(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": strings.Join(ws, " ")}, "finish_reason": "stop"}},
				"usage":   usage})
			return
		}
		send, ok := sse(w)
		if !ok {
			return
		}
		chunk := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		}
		send("", chunk(map[string]any{"role": "assistant", "content": ""}, nil))
		for i, wd := range ws {
			if mode == "drop" && i == 3 {
				return // connection ends without [DONE]
			}
			if !sleep(r, delay) {
				return
			}
			if i > 0 {
				wd = " " + wd
			}
			send("", chunk(map[string]any{"content": wd}, nil))
		}
		send("", chunk(map[string]any{}, "stop"))
		if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
			send("", map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model, "choices": []any{}, "usage": usage})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// Anthropic ------------------------------------------------------------------------------

func anthropicHandler(ctl *control, delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			System   string `json:"system"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int  `json:"max_tokens"`
			Stream    bool `json:"stream"`
		}
		errBody := func(status int, msg string) any {
			typ := "api_error"
			switch status {
			case 429:
				typ = "rate_limit_error"
			case 503:
				typ = "overloaded_error"
			}
			return map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}}
		}
		if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
			writeJSON(w, 401, errBody(401, "missing x-api-key or anthropic-version"))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model == "" || req.MaxTokens <= 0 {
			writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "model and max_tokens are required"}})
			return
		}
		mode := ctl.mode("anthropic", req.Model)
		if failure(w, r, mode, errBody) {
			return
		}
		chars := len(req.System)
		for _, m := range req.Messages {
			chars += len(m.Content)
		}
		in, ws := estimateTokens(chars), reply("anthropic", req.Model)
		out := int64(len(ws))
		id := fmt.Sprintf("msg_mock_%d", time.Now().UnixNano())
		if !req.Stream {
			writeJSON(w, 200, map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
				"content": []any{map[string]any{"type": "text", "text": strings.Join(ws, " ")}}, "stop_reason": "end_turn",
				"usage": map[string]any{"input_tokens": in, "output_tokens": out, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}})
			return
		}
		send, ok := sse(w)
		if !ok {
			return
		}
		send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
			"content": []any{}, "usage": map[string]any{"input_tokens": in, "output_tokens": 1, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}}})
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		send("ping", map[string]any{"type": "ping"})
		for i, wd := range ws {
			if mode == "drop" && i == 3 {
				return
			}
			if !sleep(r, delay) {
				return
			}
			if i > 0 {
				wd = " " + wd
			}
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": wd}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": out}})
		send("message_stop", map[string]any{"type": "message_stop"})
	}
}

// Gemini ----------------------------------------------------------------------------------

func geminiHandler(ctl *control, delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		call := r.PathValue("call")
		model, method, ok := strings.Cut(call, ":")
		errBody := func(status int, msg string) any {
			st := map[int]string{429: "RESOURCE_EXHAUSTED", 503: "UNAVAILABLE", 500: "INTERNAL", 401: "UNAUTHENTICATED", 400: "INVALID_ARGUMENT"}[status]
			return map[string]any{"error": map[string]any{"code": status, "message": msg, "status": st}}
		}
		if !ok || (method != "generateContent" && method != "streamGenerateContent") {
			writeJSON(w, 404, errBody(404, "unknown method"))
			return
		}
		if r.Header.Get("x-goog-api-key") == "" {
			writeJSON(w, 401, errBody(401, "missing api key"))
			return
		}
		var req struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Contents) == 0 {
			writeJSON(w, 400, errBody(400, "contents is required"))
			return
		}
		mode := ctl.mode("gemini", model)
		if failure(w, r, mode, errBody) {
			return
		}
		chars := 0
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				chars += len(p.Text)
			}
		}
		in, ws := estimateTokens(chars), reply("gemini", model)
		out := int64(len(ws))
		usage := map[string]any{"promptTokenCount": in, "candidatesTokenCount": out, "totalTokenCount": in + out}
		cand := func(text string, finish string) map[string]any {
			c := map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}, "index": 0}
			if finish != "" {
				c["finishReason"] = finish
			}
			return c
		}
		if method == "generateContent" {
			writeJSON(w, 200, map[string]any{"candidates": []any{cand(strings.Join(ws, " "), "STOP")}, "usageMetadata": usage, "modelVersion": model, "responseId": fmt.Sprint(time.Now().UnixNano())})
			return
		}
		send, ok := sse(w)
		if !ok {
			return
		}
		const per = 4
		for i := 0; i < len(ws); i += per {
			if mode == "drop" && i >= 8 {
				return
			}
			if !sleep(r, delay*per) {
				return
			}
			end := min(i+per, len(ws))
			text := strings.Join(ws[i:end], " ")
			if i > 0 {
				text = " " + text
			}
			finish := ""
			um := map[string]any{"promptTokenCount": in}
			if end == len(ws) {
				finish, um = "STOP", usage
			}
			send("", map[string]any{"candidates": []any{cand(text, finish)}, "usageMetadata": um, "modelVersion": model})
		}
	}
}
