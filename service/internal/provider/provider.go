// Package provider defines the adapter abstraction between the gateway engine and
// upstream LLM providers, plus the normalized request, response, usage and error types.
package provider

import (
	"context"
	"net/http"
	"time"
)

// Message is a normalized chat message. Content is plain text.
type Message struct {
	Role    string // system | user | assistant
	Content string
}

// ChatRequest is the provider-neutral request passed to adapters.
type ChatRequest struct {
	Model       string // upstream model name
	Messages    []Message
	Temperature *float64
	TopP        *float64
	MaxTokens   int // always set by the engine
	Stop        []string
	User        string
}

// ChatResponse is the provider-neutral non-streaming result.
type ChatResponse struct {
	ID           string
	Model        string
	Content      string
	FinishReason string // stop | length | content_filter | ""
	Usage        Usage
}

// StreamChunk is one normalized streaming delta.
type StreamChunk struct {
	Delta        string
	FinishReason string
}

// Config carries per-provider connection settings resolved from the catalog.
type Config struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// Adapter is implemented by every provider integration.
type Adapter interface {
	Type() string
	ChatCompletion(ctx context.Context, cfg Config, req *ChatRequest) (*ChatResponse, error)
	// StreamChatCompletion returns once the upstream accepted the request (HTTP 2xx).
	// Errors before that point are returned directly.
	StreamChatCompletion(ctx context.Context, cfg Config, req *ChatRequest) (Stream, error)
}

// Stream yields normalized chunks. Recv returns io.EOF after a clean, complete end.
// Any other error means the stream was interrupted.
type Stream interface {
	Recv() (*StreamChunk, error)
	// Usage returns the usage known so far. Status is UsageReported only if the
	// provider sent final usage for a completed stream.
	Usage() Usage
	Close() error
}

// UsageStatus tells whether token counts are trustworthy.
type UsageStatus string

const (
	UsageReported UsageStatus = "reported" // provider reported final usage
	UsageUnknown  UsageStatus = "unknown"  // tokens may have been consumed but are not known
	UsageNone     UsageStatus = "none"     // provider rejected the request; nothing billable
)

// Usage holds normalized token counts. Categories are disjoint:
// total = Input + CachedInput + CacheWrite + Output.
type Usage struct {
	Status            UsageStatus
	InputTokens       int64 // uncached input tokens
	CachedInputTokens int64 // input tokens served from cache
	CacheWriteTokens  int64 // input tokens written to cache (Anthropic)
	OutputTokens      int64 // generated tokens including reasoning
	ReasoningTokens   int64 // informational subset of OutputTokens
}

func (u Usage) TotalInput() int64 { return u.InputTokens + u.CachedInputTokens + u.CacheWriteTokens }
func (u Usage) Total() int64      { return u.TotalInput() + u.OutputTokens }

// DefaultHTTPClient is used when Config.HTTPClient is nil. Timeouts are enforced
// through request contexts, not the client, so that streams can run long.
var DefaultHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
		ForceAttemptHTTP2:     true,
	},
}

func (c Config) Client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return DefaultHTTPClient
}
