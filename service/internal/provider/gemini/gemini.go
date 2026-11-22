// Package gemini implements the Google Gemini API adapter
// (https://ai.google.dev/api/generate-content).
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	"service/internal/provider"
)

const DefaultBaseURL = "https://generativelanguage.googleapis.com"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Type() string { return "gemini" }

type part struct {
	Text    string `json:"text,omitempty"`
	Thought bool   `json:"thought,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type generationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type wireRequest struct {
	Contents          []content         `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount        *int64 `json:"promptTokenCount"`
	CandidatesTokenCount    *int64 `json:"candidatesTokenCount"`
	TotalTokenCount         *int64 `json:"totalTokenCount"`
	CachedContentTokenCount *int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      *int64 `json:"thoughtsTokenCount"`
}

type wireResponse struct {
	Candidates []struct {
		Content      *content `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
	ModelVersion  string         `json:"modelVersion"`
	ResponseID    string         `json:"responseId"`
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// NormalizeUsage converts Gemini usage metadata. promptTokenCount includes cached
// content tokens; thinking tokens are billed as output.
func NormalizeUsage(u *usageMetadata) provider.Usage {
	if u == nil || u.PromptTokenCount == nil {
		return provider.Usage{Status: provider.UsageUnknown}
	}
	prompt, cached := val(u.PromptTokenCount), val(u.CachedContentTokenCount)
	if cached > prompt {
		cached = prompt
	}
	thoughts := val(u.ThoughtsTokenCount)
	return provider.Usage{
		Status:            provider.UsageReported,
		InputTokens:       prompt - cached,
		CachedInputTokens: cached,
		OutputTokens:      val(u.CandidatesTokenCount) + thoughts,
		ReasoningTokens:   thoughts,
	}
}

// MapFinishReason converts Gemini finish reasons to OpenAI finish reasons.
func MapFinishReason(r string) string {
	switch r {
	case "":
		return ""
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return "content_filter"
	default:
		return "stop"
	}
}

func extractError(body []byte) (string, string) {
	var w struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	// Gemini sometimes wraps errors in an array.
	if json.Unmarshal(body, &w) != nil {
		var arr []struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &arr) != nil || len(arr) == 0 {
			return "", ""
		}
		return arr[0].Error.Message, arr[0].Error.Status
	}
	return w.Error.Message, w.Error.Status
}

func buildRequest(req *provider.ChatRequest) wireRequest {
	system, rest := provider.SplitSystem(req.Messages)
	rest = provider.MergeSameRole(rest)
	w := wireRequest{Contents: make([]content, len(rest))}
	for i, m := range rest {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		w.Contents[i] = content{Role: role, Parts: []part{{Text: m.Content}}}
	}
	if system != "" {
		w.SystemInstruction = &content{Parts: []part{{Text: system}}}
	}
	gc := generationConfig{Temperature: req.Temperature, TopP: req.TopP, MaxOutputTokens: req.MaxTokens, StopSequences: req.Stop}
	w.GenerationConfig = &gc
	return w
}

func endpoint(cfg provider.Config, model string, stream bool) string {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	m := url.PathEscape(model)
	if stream {
		return base + "/v1beta/models/" + m + ":streamGenerateContent?alt=sse"
	}
	return base + "/v1beta/models/" + m + ":generateContent"
}

func headers(cfg provider.Config) map[string]string {
	return map[string]string{"x-goog-api-key": cfg.APIKey}
}

func textOf(c *content) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range c.Parts {
		if !p.Thought {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func (a *Adapter) ChatCompletion(ctx context.Context, cfg provider.Config, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	resp, err := provider.PostJSON(ctx, cfg.Client(), endpoint(cfg, req.Model, false), headers(cfg), buildRequest(req), extractError)
	if err != nil {
		return nil, err
	}
	var w wireResponse
	if err := provider.DecodeJSON(ctx, resp, &w); err != nil {
		return nil, err
	}
	out := &provider.ChatResponse{ID: w.ResponseID, Model: w.ModelVersion, Usage: NormalizeUsage(w.UsageMetadata)}
	if len(w.Candidates) == 0 {
		if w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
			out.FinishReason = "content_filter"
			return out, nil
		}
		return nil, &provider.Error{Kind: provider.KindInvalidResponse, Message: "response has no candidates", Sent: true}
	}
	out.Content = textOf(w.Candidates[0].Content)
	out.FinishReason = MapFinishReason(w.Candidates[0].FinishReason)
	return out, nil
}

func (a *Adapter) StreamChatCompletion(ctx context.Context, cfg provider.Config, req *provider.ChatRequest) (provider.Stream, error) {
	resp, err := provider.PostJSON(ctx, cfg.Client(), endpoint(cfg, req.Model, true), headers(cfg), buildRequest(req), extractError)
	if err != nil {
		return nil, err
	}
	return &stream{ctx: ctx, body: resp.Body, sse: provider.NewSSEReader(resp.Body), usage: provider.Usage{Status: provider.UsageUnknown}}, nil
}

type stream struct {
	ctx      context.Context
	body     io.ReadCloser
	sse      *provider.SSEReader
	usage    provider.Usage
	finished bool // a finishReason was received
	done     bool
	pending  *provider.StreamChunk
}

func (s *stream) Recv() (*provider.StreamChunk, error) {
	if s.pending != nil {
		c := s.pending
		s.pending = nil
		return c, nil
	}
	if s.done {
		return nil, io.EOF
	}
	for {
		ev, err := s.sse.Next()
		if err != nil {
			if errors.Is(err, io.EOF) && s.finished {
				// Gemini has no terminal sentinel: the body ending after a
				// finishReason is a clean completion.
				s.done = true
				return nil, io.EOF
			}
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, provider.StreamReadError(s.ctx, err)
		}
		if len(ev.Data) == 0 {
			continue
		}
		var w wireResponse
		if err := json.Unmarshal(ev.Data, &w); err != nil {
			return nil, &provider.Error{Kind: provider.KindInvalidResponse, Message: "decode stream chunk: " + err.Error(), Sent: true}
		}
		if w.UsageMetadata != nil {
			s.usage = NormalizeUsage(w.UsageMetadata)
		}
		if len(w.Candidates) == 0 {
			if w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
				s.finished = true
				return &provider.StreamChunk{FinishReason: "content_filter"}, nil
			}
			continue
		}
		c := w.Candidates[0]
		text := textOf(c.Content)
		fr := MapFinishReason(c.FinishReason)
		if fr != "" {
			s.finished = true
		}
		switch {
		case text != "" && fr != "":
			// Emit text first, then the finish reason as its own chunk.
			s.pending = &provider.StreamChunk{FinishReason: fr}
			return &provider.StreamChunk{Delta: text}, nil
		case text != "" || fr != "":
			return &provider.StreamChunk{Delta: text, FinishReason: fr}, nil
		}
	}
}

func (s *stream) Usage() provider.Usage {
	if !s.done && s.usage.Status == provider.UsageReported {
		u := s.usage
		u.Status = provider.UsageUnknown
		return u
	}
	return s.usage
}

func (s *stream) Close() error { return s.body.Close() }

var _ provider.Adapter = (*Adapter)(nil)
