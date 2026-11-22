// Package anthropic implements the Anthropic Messages API adapter
// (https://docs.anthropic.com/en/api/messages, streaming: /en/api/messages-streaming).
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"service/internal/provider"
)

const (
	DefaultBaseURL = "https://api.anthropic.com"
	APIVersion     = "2023-06-01"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Type() string { return "anthropic" }

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type wireRequest struct {
	Model         string        `json:"model"`
	System        string        `json:"system,omitempty"`
	Messages      []wireMessage `json:"messages"`
	MaxTokens     int           `json:"max_tokens"`
	Temperature   *float64      `json:"temperature,omitempty"`
	TopP          *float64      `json:"top_p,omitempty"`
	StopSequences []string      `json:"stop_sequences,omitempty"`
	Stream        bool          `json:"stream,omitempty"`
	Metadata      *struct {
		UserID string `json:"user_id"`
	} `json:"metadata,omitempty"`
}

type wireUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
}

type wireResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string     `json:"stop_reason"`
	Usage      *wireUsage `json:"usage"`
}

type wireEvent struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string     `json:"id"`
		Model string     `json:"model"`
		Usage *wireUsage `json:"usage"`
	} `json:"message"`
	Delta *struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// NormalizeUsage converts Anthropic usage. input_tokens excludes cache reads and
// cache writes, which are reported (and priced) separately.
func NormalizeUsage(u *wireUsage) provider.Usage {
	if u == nil || u.InputTokens == nil || u.OutputTokens == nil {
		return provider.Usage{Status: provider.UsageUnknown}
	}
	return provider.Usage{
		Status:            provider.UsageReported,
		InputTokens:       val(u.InputTokens),
		CachedInputTokens: val(u.CacheReadInputTokens),
		CacheWriteTokens:  val(u.CacheCreationInputTokens),
		OutputTokens:      val(u.OutputTokens),
	}
}

// MapStopReason converts Anthropic stop reasons to OpenAI finish reasons.
func MapStopReason(r string) string {
	switch r {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "refusal":
		return "content_filter"
	case "":
		return ""
	default:
		return "stop"
	}
}

func extractError(body []byte) (string, string) {
	var w struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &w) != nil {
		return "", ""
	}
	return w.Error.Message, w.Error.Type
}

func buildRequest(req *provider.ChatRequest, stream bool) wireRequest {
	system, rest := provider.SplitSystem(req.Messages)
	rest = provider.MergeSameRole(rest)
	msgs := make([]wireMessage, len(rest))
	for i, m := range rest {
		msgs[i] = wireMessage{Role: m.Role, Content: m.Content}
	}
	w := wireRequest{
		Model:         req.Model,
		System:        system,
		Messages:      msgs,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.Stop,
		Stream:        stream,
	}
	if req.User != "" {
		w.Metadata = &struct {
			UserID string `json:"user_id"`
		}{UserID: req.User}
	}
	return w
}

func endpoint(cfg provider.Config) string {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return base + "/v1/messages"
}

func headers(cfg provider.Config) map[string]string {
	return map[string]string{"x-api-key": cfg.APIKey, "anthropic-version": APIVersion}
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
	var sb strings.Builder
	for _, c := range w.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return &provider.ChatResponse{
		ID:           w.ID,
		Model:        w.Model,
		Content:      sb.String(),
		FinishReason: MapStopReason(w.StopReason),
		Usage:        NormalizeUsage(w.Usage),
	}, nil
}

func (a *Adapter) StreamChatCompletion(ctx context.Context, cfg provider.Config, req *provider.ChatRequest) (provider.Stream, error) {
	resp, err := provider.PostJSON(ctx, cfg.Client(), endpoint(cfg), headers(cfg), buildRequest(req, true), extractError)
	if err != nil {
		return nil, err
	}
	return &stream{ctx: ctx, body: resp.Body, sse: provider.NewSSEReader(resp.Body)}, nil
}

type stream struct {
	ctx  context.Context
	body io.ReadCloser
	sse  *provider.SSEReader

	input, cacheRead, cacheWrite, output int64
	gotStart, gotDelta, done            bool
}

func (s *stream) Recv() (*provider.StreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	for {
		ev, err := s.sse.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF // a complete stream ends with message_stop
			}
			return nil, provider.StreamReadError(s.ctx, err)
		}
		if len(ev.Data) == 0 {
			continue
		}
		var e wireEvent
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return nil, &provider.Error{Kind: provider.KindInvalidResponse, Message: "decode stream event: " + err.Error(), Sent: true}
		}
		switch e.Type {
		case "message_start":
			if e.Message != nil && e.Message.Usage != nil {
				u := e.Message.Usage
				s.input, s.cacheRead, s.cacheWrite = val(u.InputTokens), val(u.CacheReadInputTokens), val(u.CacheCreationInputTokens)
				s.output = val(u.OutputTokens)
				s.gotStart = true
			}
		case "content_block_delta":
			if e.Delta != nil && e.Delta.Type == "text_delta" && e.Delta.Text != "" {
				return &provider.StreamChunk{Delta: e.Delta.Text}, nil
			}
		case "message_delta":
			if e.Usage != nil {
				// message_delta usage is cumulative.
				if e.Usage.OutputTokens != nil {
					s.output = *e.Usage.OutputTokens
					s.gotDelta = true
				}
				if e.Usage.InputTokens != nil {
					s.input = *e.Usage.InputTokens
				}
				if e.Usage.CacheReadInputTokens != nil {
					s.cacheRead = *e.Usage.CacheReadInputTokens
				}
				if e.Usage.CacheCreationInputTokens != nil {
					s.cacheWrite = *e.Usage.CacheCreationInputTokens
				}
			}
			if e.Delta != nil && e.Delta.StopReason != "" {
				return &provider.StreamChunk{FinishReason: MapStopReason(e.Delta.StopReason)}, nil
			}
		case "message_stop":
			s.done = true
			return nil, io.EOF
		case "error":
			pe := &provider.Error{Kind: provider.KindStreamInterrupted, Message: "provider stream error", Sent: true}
			if e.Error != nil {
				pe.Message, pe.ProviderCode = e.Error.Message, e.Error.Type
				if e.Error.Type == "overloaded_error" {
					pe.Kind = provider.KindOverloaded
				}
			}
			return nil, pe
		}
	}
}

func (s *stream) Usage() provider.Usage {
	u := provider.Usage{
		Status:            provider.UsageUnknown,
		InputTokens:       s.input,
		CachedInputTokens: s.cacheRead,
		CacheWriteTokens:  s.cacheWrite,
		OutputTokens:      s.output,
	}
	if s.done && s.gotStart && s.gotDelta {
		u.Status = provider.UsageReported
	}
	return u
}

func (s *stream) Close() error { return s.body.Close() }

var _ provider.Adapter = (*Adapter)(nil)
