package gateway

import (
	"context"
	"errors"
	"io"
	"time"

	"service/internal/breaker"
	"service/internal/pricing"
	"service/internal/provider"
	"service/internal/usage"
)

var errEOF = io.EOF

// pipe forwards a committed stream to the client. It returns the attempt status
// and the error that ended the stream (nil for a clean completion).
func (x *exec) pipe(ctx context.Context, p *plan, s provider.Stream, first *provider.StreamChunk, ended bool) (string, *provider.Error) {
	prov := p.provider()
	x.meta.Provider, x.meta.Model, x.meta.Attempts = prov.Slug, p.model.Name, countNonSkipped(x.rec.Attempts)+1
	w := x.sink.StartStream(x.meta)

	id := "chatcmpl-" + x.call.RequestID.String()
	created := x.start.Unix()
	chunk := func(delta Delta, finish string) *ChatCompletionChunk {
		c := &ChatCompletionChunk{ID: id, Object: "chat.completion.chunk", Created: created, Model: p.model.Name,
			Choices: []ChunkChoice{{Index: 0, Delta: delta}}}
		if finish != "" {
			f := finish
			c.Choices[0].FinishReason = &f
		}
		return c
	}

	clientGone := func() (string, *provider.Error) {
		return usage.StatusClientCancelled, &provider.Error{Kind: provider.KindCanceled, Message: "client disconnected during stream", Sent: true}
	}

	// The first chunk carries the assistant role, as OpenAI does.
	firstDelta := Delta{Role: "assistant"}
	firstFinish := ""
	if first != nil {
		firstDelta.Content = first.Delta
		firstFinish = first.FinishReason
	}
	if err := w.Send(chunk(firstDelta, firstFinish)); err != nil {
		return clientGone()
	}

	for !ended {
		c, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if x.clientCtx.Err() != nil {
				return clientGone()
			}
			pe := provider.AsError(err)
			if ctx.Err() != nil {
				pe = &provider.Error{Kind: provider.KindTimeout, Message: "stream exceeded the maximum duration", Sent: true}
			}
			// After the first byte the provider is fixed: report and close (ADR-009).
			_ = w.Send(map[string]any{"error": map[string]any{
				"type": "server_error", "code": "stream_interrupted",
				"message": "the upstream provider stream was interrupted: " + string(pe.Kind),
			}})
			if pe.Kind != provider.KindTimeout {
				pe = &provider.Error{Kind: provider.KindStreamInterrupted, Message: pe.Message, Sent: true, ProviderCode: pe.ProviderCode}
			}
			return usage.StatusInterrupted, pe
		}
		if err := w.Send(chunk(Delta{Content: c.Delta}, c.FinishReason)); err != nil {
			return clientGone()
		}
	}

	if x.call.Req.StreamOptions != nil && x.call.Req.StreamOptions.IncludeUsage {
		u := s.Usage()
		var cost *int64
		if u.Status == provider.UsageReported && p.price != nil {
			c := pricing.Cost(*p.price, u)
			cost = &c
		}
		uc := &ChatCompletionChunk{ID: id, Object: "chat.completion.chunk", Created: created, Model: p.model.Name, Choices: []ChunkChoice{}, Usage: toAPIUsage(u, cost)}
		if uc.Usage != nil {
			if err := w.Send(uc); err != nil {
				return clientGone()
			}
		}
	}
	if err := w.Done(); err != nil {
		return clientGone()
	}
	return usage.StatusSuccess, nil
}

// NopObserver discards metric events.
type NopObserver struct{}

func (NopObserver) RequestFinished(string, bool, string, time.Duration) {}
func (NopObserver) AttemptFinished(string, string, string, time.Duration) {}
func (NopObserver) Fallback(string, string)                               {}
func (NopObserver) RateLimited(string)                                    {}
func (NopObserver) BudgetRejected()                                       {}
func (NopObserver) Tokens(string, provider.Usage)                         {}
func (NopObserver) StreamTTFB(string, time.Duration)                      {}
func (NopObserver) BreakerState(string, breaker.State)                    {}
func (NopObserver) RecordFailed()                                         {}
