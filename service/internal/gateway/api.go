package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"service/internal/provider"
)

// ChatCompletionRequest is the OpenAI-compatible request accepted by the gateway.
type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Stop                StopList        `json:"stop,omitempty"`
	User                string          `json:"user,omitempty"`
	N                   *int            `json:"n,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	Functions           json.RawMessage `json:"functions,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatMessage struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
	Name    string         `json:"name,omitempty"`
}

// MessageContent accepts either a string or an array of text parts.
type MessageContent string

func (m *MessageContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*m = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*m = MessageContent(s)
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &parts); err != nil {
		return errors.New("content must be a string or an array of text parts")
	}
	var buf bytes.Buffer
	for _, p := range parts {
		if p.Type != "text" {
			return fmt.Errorf("content part type %q is not supported", p.Type)
		}
		buf.WriteString(p.Text)
	}
	*m = MessageContent(buf.String())
	return nil
}

// StopList accepts a string or an array of strings.
type StopList []string

func (s *StopList) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*s = nil
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = StopList{v}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return errors.New("stop must be a string or an array of strings")
	}
	*s = arr
	return nil
}

// Validate checks the request and normalizes roles. It returns an APIError on failure.
func (r *ChatCompletionRequest) Validate() *APIError {
	bad := func(format string, a ...any) *APIError { return ErrInvalidRequest(fmt.Sprintf(format, a...)) }
	if r.Model == "" || len(r.Model) > 200 {
		return bad("model is required (max 200 characters)")
	}
	if len(r.Tools) > 0 || len(r.Functions) > 0 || len(r.ToolChoice) > 0 {
		return bad("tool and function calling are not supported by this gateway")
	}
	if r.N != nil && *r.N != 1 {
		return bad("n must be 1")
	}
	if len(r.Messages) == 0 || len(r.Messages) > 1000 {
		return bad("messages must contain between 1 and 1000 items")
	}
	nonSystem := 0
	for i := range r.Messages {
		m := &r.Messages[i]
		switch m.Role {
		case "developer":
			m.Role = "system"
		case "system", "user", "assistant":
		default:
			return bad("messages[%d].role %q is not supported (system, developer, user, assistant)", i, m.Role)
		}
		if m.Role != "system" {
			nonSystem++
		}
	}
	if nonSystem == 0 {
		return bad("messages must contain at least one user or assistant message")
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		return bad("temperature must be between 0 and 2")
	}
	if r.TopP != nil && (*r.TopP < 0 || *r.TopP > 1) {
		return bad("top_p must be between 0 and 1")
	}
	for _, mt := range []*int{r.MaxTokens, r.MaxCompletionTokens} {
		if mt != nil && (*mt < 1 || *mt > 200_000) {
			return bad("max_tokens must be between 1 and 200000")
		}
	}
	if len(r.Stop) > 4 {
		return bad("stop accepts at most 4 sequences")
	}
	if len(r.User) > 256 {
		return bad("user must be at most 256 characters")
	}
	return nil
}

// maxTokens returns the requested output cap or the default.
func (r *ChatCompletionRequest) maxTokens(def int) int {
	if r.MaxCompletionTokens != nil {
		return *r.MaxCompletionTokens
	}
	if r.MaxTokens != nil {
		return *r.MaxTokens
	}
	return def
}

func (r *ChatCompletionRequest) toProvider(defaultMax int) provider.ChatRequest {
	msgs := make([]provider.Message, len(r.Messages))
	for i, m := range r.Messages {
		msgs[i] = provider.Message{Role: m.Role, Content: string(m.Content)}
	}
	return provider.ChatRequest{
		Messages:    msgs,
		Temperature: r.Temperature,
		TopP:        r.TopP,
		MaxTokens:   r.maxTokens(defaultMax),
		Stop:        r.Stop,
		User:        r.User,
	}
}

// Response types -----------------------------------------------------------------

type ChatCompletionResponse struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"`
	Created int64     `json:"created"`
	Model   string    `json:"model"`
	Choices []Choice  `json:"choices"`
	Usage   *APIUsage `json:"usage,omitempty"`
}

type Choice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason *string         `json:"finish_reason"`
}

type ResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *APIUsage     `json:"usage,omitempty"`
}

type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type APIUsage struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	TotalTokens             int64                    `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	// Gateway extension: estimated cost in nano-USD (omitted when unknown).
	CostNanos *int64 `json:"cost_nanos,omitempty"`
}

type PromptTokensDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

func toAPIUsage(u provider.Usage, cost *int64) *APIUsage {
	if u.Status != provider.UsageReported {
		return nil
	}
	return &APIUsage{
		PromptTokens:            u.TotalInput(),
		CompletionTokens:        u.OutputTokens,
		TotalTokens:             u.Total(),
		PromptTokensDetails:     &PromptTokensDetails{CachedTokens: u.CachedInputTokens, CacheWriteTokens: u.CacheWriteTokens},
		CompletionTokensDetails: &CompletionTokensDetails{ReasoningTokens: u.ReasoningTokens},
		CostNanos:               cost,
	}
}

// Errors --------------------------------------------------------------------------

// APIError is an OpenAI-style error returned to clients.
type APIError struct {
	Status     int           `json:"-"`
	Type       string        `json:"type"`
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	RetryAfter time.Duration `json:"-"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// Body returns the JSON envelope.
func (e *APIError) Body() map[string]any { return map[string]any{"error": e} }

func newErr(status int, typ, code, msg string) *APIError {
	return &APIError{Status: status, Type: typ, Code: code, Message: msg}
}

func ErrInvalidRequest(msg string) *APIError {
	return newErr(http.StatusBadRequest, "invalid_request_error", "invalid_request", msg)
}

func ErrUnauthorized(msg string) *APIError {
	return newErr(http.StatusUnauthorized, "authentication_error", "invalid_api_key", msg)
}

func ErrServiceUnavailable(msg string) *APIError {
	return newErr(http.StatusServiceUnavailable, "server_error", "service_unavailable", msg)
}

func errModelNotFound(model string) *APIError {
	return newErr(http.StatusNotFound, "invalid_request_error", "model_not_found", fmt.Sprintf("model %q is not available", model))
}

// fromProviderError maps the final provider failure to a client error.
func fromProviderError(pe *provider.Error) *APIError {
	switch pe.Kind {
	case provider.KindBadRequest:
		return newErr(http.StatusBadRequest, "invalid_request_error", "invalid_request", "provider rejected the request: "+pe.Message)
	case provider.KindRateLimited:
		e := newErr(http.StatusTooManyRequests, "rate_limit_error", "upstream_rate_limited", "all providers are rate limiting requests")
		e.RetryAfter = pe.RetryAfter
		return e
	case provider.KindTimeout:
		return newErr(http.StatusGatewayTimeout, "server_error", "upstream_timeout", "provider request timed out")
	case provider.KindCanceled:
		return newErr(499, "client_error", "client_closed_request", "client closed the request")
	default:
		return newErr(http.StatusBadGateway, "server_error", "upstream_error", "provider request failed: "+string(pe.Kind))
	}
}
