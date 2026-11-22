// Package openai implements the OpenAI Chat Completions adapter
// (https://platform.openai.com/docs/api-reference/chat).
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"service/internal/provider"
)

const DefaultBaseURL = "https://api.openai.com"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Type() string { return "openai" }

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Model               string         `json:"model"`
	Messages            []wireMessage  `json:"messages"`
	Temperature         *float64       `json:"temperature,omitempty"`
	TopP                *float64       `json:"top_p,omitempty"`
	MaxCompletionTokens int            `json:"max_completion_tokens,omitempty"`
	Stop                []string       `json:"stop,omitempty"`
	User                string         `json:"user,omitempty"`
	Stream              bool           `json:"stream,omitempty"`
	StreamOptions       *streamOptions `json:"stream_options,omitempty"`
}

type wireUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type wireResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type wireChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content *string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *wireError `json:"error"`
}

type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// NormalizeUsage converts OpenAI usage. prompt_tokens includes cached tokens and
// completion_tokens includes reasoning tokens.
func NormalizeUsage(u *wireUsage) provider.Usage {
	if u == nil {
		return provider.Usage{Status: provider.UsageUnknown}
	}
	out := provider.Usage{Status: provider.UsageReported, OutputTokens: u.CompletionTokens}
	cached := int64(0)
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	}
	if cached > u.PromptTokens {
		cached = u.PromptTokens
	}
	out.CachedInputTokens = cached
	out.InputTokens = u.PromptTokens - cached
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return out
}

func extractError(body []byte) (string, string) {
	var w struct {
		Error wireError `json:"error"`
	}
	if json.Unmarshal(body, &w) != nil {
		return "", ""
	}
	code := w.Error.Type
	if s, ok := w.Error.Code.(string); ok && s != "" {
		code = s
	}
	return w.Error.Message, code
}

func buildRequest(req *provider.ChatRequest, stream bool) wireRequest {
	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = wireMessage{Role: m.Role, Content: m.Content}
	}
	w := wireRequest{
		Model:               req.Model,
		Messages:            msgs,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		MaxCompletionTokens: req.MaxTokens,
		Stop:                req.Stop,
		User:                req.User,
	}
	if stream {
		w.Stream = true
		w.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return w
}

func endpoint(cfg provider.Config) string {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return base + "/v1/chat/completions"
}

func headers(cfg provider.Config) map[string]string {
	return map[string]string{"Authorization": "Bearer " + cfg.APIKey}
}

func (a *Adapter) ChatCompletion(ctx context.Context, cfg provider.Config, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	resp, err := provider.PostJSON(ctx, cfg.Client(), endpoint(cfg), headers(cfg), buildRequest(req, false), extractError)
	if err != nil {
		return nil, err
	}
	var w wireResponse
	if err := provider.DecodeJSON(ctx, resp, &w); err != nil {
		return nil, err
	}
	if len(w.Choices) == 0 {
		return nil, &provider.Error{Kind: provider.KindInvalidResponse, Message: "response has no choices", Sent: true}
	}
	out := &provider.ChatResponse{
		ID:           w.ID,
		Model:        w.Model,
		FinishReason: w.Choices[0].FinishReason,
		Usage:        NormalizeUsage(w.Usage),
	}
	if c := w.Choices[0].Message.Content; c != nil {
		out.Content = *c
	}
	return out, nil
}

func (a *Adapter) StreamChatCompletion(ctx context.Context, cfg provider.Config, req *provider.ChatRequest) (provider.Stream, error) {
	resp, err := provider.PostJSON(ctx, cfg.Client(), endpoint(cfg), headers(cfg), buildRequest(req, true), extractError)
	if err != nil {
		return nil, err
	}
	return &stream{ctx: ctx, body: resp.Body, sse: provider.NewSSEReader(resp.Body), usage: provider.Usage{Status: provider.UsageUnknown}}, nil
}

type stream struct {
	ctx   context.Context
	body  io.ReadCloser
	sse   *provider.SSEReader
	usage provider.Usage
	done  bool
}

func (s *stream) Recv() (*provider.StreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	for {
		ev, err := s.sse.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF // OpenAI always terminates with [DONE]
			}
			return nil, provider.StreamReadError(s.ctx, err)
		}
		data := strings.TrimSpace(string(ev.Data))
		if data == "[DONE]" {
			s.done = true
			return nil, io.EOF
		}
		if data == "" {
			continue
		}
		var c wireChunk
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			return nil, &provider.Error{Kind: provider.KindInvalidResponse, Message: "decode stream chunk: " + err.Error(), Sent: true}
		}
		if c.Error != nil {
			return nil, &provider.Error{Kind: provider.KindStreamInterrupted, Message: c.Error.Message, ProviderCode: c.Error.Type, Sent: true}
		}
		if c.Usage != nil {
			s.usage = NormalizeUsage(c.Usage)
		}
		if len(c.Choices) == 0 {
			continue // usage-only chunk
		}
		ch := &provider.StreamChunk{}
		if c.Choices[0].Delta.Content != nil {
			ch.Delta = *c.Choices[0].Delta.Content
		}
		if fr := c.Choices[0].FinishReason; fr != nil {
			ch.FinishReason = *fr
		}
		if ch.Delta == "" && ch.FinishReason == "" {
			continue // role-only delta
		}
		return ch, nil
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
