// Package gateway is the data-plane engine: it orchestrates rate limiting, budget
// reservation, routing, retries, fallback, circuit breaking, streaming and
// usage recording for each request.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"service/internal/breaker"
	"service/internal/budget"
	"service/internal/pricing"
	"service/internal/provider"
	"service/internal/ratelimit"
	"service/internal/usage"
)

var tracer = otel.Tracer("service/internal/gateway")

// Limiter enforces per-team rate and concurrency limits.
type Limiter interface {
	Acquire(ctx context.Context, team string, rpm, concurrency int, ttl time.Duration) (release func(), degraded bool, err error)
}

// BudgetReserver reserves and releases spend.
type BudgetReserver interface {
	Reserve(ctx context.Context, teamID, requestID uuid.UUID, amount int64) (*budget.Reservation, bool, error)
	Release(ctx context.Context, res *budget.Reservation) error
}

// Recorder persists request outcomes.
type Recorder interface {
	Record(ctx context.Context, r *usage.RequestRecord) error
}

// Observer receives metric events. All methods must be cheap and non-blocking.
type Observer interface {
	RequestFinished(status string, stream bool, code string, d time.Duration)
	AttemptFinished(provider, model, outcome string, d time.Duration)
	Fallback(from, to string)
	RateLimited(reason string)
	BudgetRejected()
	Tokens(provider string, u provider.Usage)
	StreamTTFB(provider string, d time.Duration)
	BreakerState(provider string, state breaker.State)
	RecordFailed()
}

type Options struct {
	DefaultMaxTokens int
	RequestTimeout   time.Duration
	StreamTimeout    time.Duration
	MaxPendingWrites int
}

type Engine struct {
	catalog  *Catalog
	adapters map[string]provider.Adapter
	breakers *breaker.Registry
	limiter  Limiter
	budget   BudgetReserver
	recorder Recorder
	obs      Observer
	log      *slog.Logger
	opts     Options
	client   *http.Client

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	rnd   func() float64

	pending chan struct{}
	wg      sync.WaitGroup
}

type Deps struct {
	Catalog    *Catalog
	Adapters   []provider.Adapter
	Breakers   *breaker.Registry
	Limiter    Limiter        // optional
	Budget     BudgetReserver // optional
	Recorder   Recorder
	Observer   Observer // optional
	Logger     *slog.Logger
	HTTPClient *http.Client // optional, for provider calls
}

func NewEngine(d Deps, o Options) *Engine {
	if o.DefaultMaxTokens <= 0 {
		o.DefaultMaxTokens = 4096
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 120 * time.Second
	}
	if o.StreamTimeout <= 0 {
		o.StreamTimeout = 10 * time.Minute
	}
	if o.MaxPendingWrites <= 0 {
		o.MaxPendingWrites = 1024
	}
	e := &Engine{
		catalog: d.Catalog, adapters: map[string]provider.Adapter{}, breakers: d.Breakers,
		limiter: d.Limiter, budget: d.Budget, recorder: d.Recorder, obs: d.Observer, log: d.Logger,
		opts: o, client: d.HTTPClient, now: time.Now, sleep: sleepCtx, pending: make(chan struct{}, o.MaxPendingWrites),
	}
	if e.obs == nil {
		e.obs = NopObserver{}
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	for _, a := range d.Adapters {
		e.adapters[a.Type()] = a
	}
	return e
}

// Catalog exposes the routing catalog (used by /v1/models).
func (e *Engine) Catalog() *Catalog { return e.catalog }

// Breakers exposes the breaker registry (reliability API).
func (e *Engine) Breakers() *breaker.Registry { return e.breakers }

// Call describes one logical gateway request.
type Call struct {
	RequestID    uuid.UUID
	TeamID       uuid.UUID
	APIKeyID     *uuid.UUID
	UserID       *uuid.UUID
	Source       string // api | playground
	RPMLimit     int
	Concurrency  int
	ProviderHint string
	Req          *ChatCompletionRequest
}

// Meta is response metadata exposed as headers.
type Meta struct {
	RequestID uuid.UUID
	Provider  string
	Model     string
	Attempts  int
}

// Sink abstracts the HTTP response so the engine is transport-agnostic.
type Sink interface {
	WriteError(meta Meta, e *APIError)
	WriteResponse(meta Meta, resp *ChatCompletionResponse)
	// StartStream commits the response to SSE. After this call only the
	// returned writer may be used.
	StartStream(meta Meta) StreamWriter
}

type StreamWriter interface {
	Send(v any) error
	Done() error
}

// plan is a route target with its price and worst-case cost.
type plan struct {
	model    *ModelEntry
	price    *pricing.Price
	estimate int64
}

func (p *plan) provider() *ProviderEntry { return p.model.Provider }

// exec holds per-request state.
type exec struct {
	e         *Engine
	call      *Call
	rec       *usage.RequestRecord
	start     time.Time
	preq      provider.ChatRequest
	inputEst  int64
	meta      Meta
	sink      Sink
	clientCtx context.Context
}

// Execute runs the request end to end and writes the response to sink.
func (e *Engine) Execute(ctx context.Context, call *Call, sink Sink) {
	start := e.now()
	x := &exec{e: e, call: call, start: start, sink: sink, clientCtx: ctx, meta: Meta{RequestID: call.RequestID}}
	x.rec = &usage.RequestRecord{
		ID: call.RequestID, TeamID: call.TeamID, APIKeyID: call.APIKeyID, UserID: call.UserID, Source: call.Source,
		RequestedModel: call.Req.Model, Stream: call.Req.Stream, CreatedAt: start,
	}

	ctx, span := tracer.Start(ctx, "gateway.execute")
	span.SetAttributes(attribute.String("gateway.request_id", call.RequestID.String()), attribute.Bool("gateway.stream", call.Req.Stream))
	defer span.End()

	// 1. Rate limiting (not recorded in Postgres, see ADR-013).
	if e.limiter != nil {
		rlCtx, rlSpan := tracer.Start(ctx, "gateway.ratelimit")
		leaseTTL := e.opts.RequestTimeout
		if call.Req.Stream {
			leaseTTL = e.opts.StreamTimeout
		}
		release, degraded, err := e.limiter.Acquire(rlCtx, call.TeamID.String(), call.RPMLimit, call.Concurrency, leaseTTL+30*time.Second)
		rlSpan.End()
		if err != nil {
			var le *ratelimit.LimitError
			if errors.As(err, &le) {
				reason, code := "rpm", "rate_limit_exceeded"
				msg := fmt.Sprintf("team rate limit of %d requests per minute exceeded", call.RPMLimit)
				if errors.Is(err, ratelimit.ErrConcurrencyExceeded) {
					reason, code = "concurrency", "concurrency_limit_exceeded"
					msg = fmt.Sprintf("team concurrency limit of %d in-flight requests exceeded", call.Concurrency)
				}
				e.obs.RateLimited(reason)
				ae := newErr(http.StatusTooManyRequests, "rate_limit_error", code, msg)
				ae.RetryAfter = le.RetryAfter
				sink.WriteError(x.meta, ae)
				e.obs.RequestFinished("rate_limited", call.Req.Stream, code, e.now().Sub(start))
				return
			}
			e.log.Error("rate limiter unavailable", "error", err, "request_id", call.RequestID)
			sink.WriteError(x.meta, ErrServiceUnavailable("rate limiter unavailable"))
			e.obs.RequestFinished("error", call.Req.Stream, "service_unavailable", e.now().Sub(start))
			return
		}
		if degraded {
			e.obs.RateLimited("degraded_fail_open")
			e.log.Warn("rate limiter unavailable, failing open", "request_id", call.RequestID)
		}
		defer release()
	}

	timeout := e.opts.RequestTimeout
	if call.Req.Stream {
		timeout = e.opts.StreamTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 2. Routing.
	route, err := e.catalog.Snapshot().Resolve(call.Req.Model, call.ProviderHint)
	if err != nil {
		x.reject(errModelNotFound(call.Req.Model))
		return
	}
	span.SetAttributes(attribute.String("gateway.route_strategy", route.Strategy))

	x.preq = call.Req.toProvider(e.opts.DefaultMaxTokens)
	x.inputEst = pricing.EstimateInputTokens(x.preq.Messages)
	plans := make([]*plan, 0, len(route.Targets))
	var maxEstimate int64
	for _, m := range route.Targets {
		p := &plan{model: m}
		if price, ok := m.Prices.At(start); ok {
			p.price = &price
			p.estimate = pricing.Estimate(price, x.inputEst, int64(x.preq.MaxTokens))
			if p.estimate > maxEstimate {
				maxEstimate = p.estimate
			}
		}
		plans = append(plans, p)
	}

	// 3. Budget reservation.
	if e.budget != nil {
		bCtx, bSpan := tracer.Start(ctx, "gateway.budget.reserve")
		res, hasBudget, err := e.budget.Reserve(bCtx, call.TeamID, call.RequestID, maxEstimate)
		bSpan.End()
		var ex *budget.ExceededError
		switch {
		case errors.As(err, &ex):
			e.obs.BudgetRejected()
			x.reject(newErr(http.StatusPaymentRequired, "budget_error", "budget_exceeded",
				fmt.Sprintf("team %s budget exhausted (this request may cost up to $%.6f)", ex.Period, float64(maxEstimate)/pricing.NanosPerUSD)))
			return
		case err != nil:
			e.log.Error("budget reservation failed", "error", err, "request_id", call.RequestID)
			sink.WriteError(x.meta, ErrServiceUnavailable("budget service unavailable"))
			e.obs.RequestFinished("error", call.Req.Stream, "service_unavailable", e.now().Sub(start))
			return
		}
		x.rec.Reservation = res
		if hasBudget {
			priced := plans[:0]
			for _, p := range plans {
				if p.price != nil {
					priced = append(priced, p)
				}
			}
			plans = priced
			if len(plans) == 0 {
				if err := e.budget.Release(context.WithoutCancel(ctx), res); err != nil {
					e.log.Error("release reservation", "error", err)
				}
				x.rec.Reservation = nil
				x.reject(newErr(http.StatusServiceUnavailable, "server_error", "pricing_not_configured",
					"no priced model is available for this team's budget; ask an administrator to configure pricing"))
				return
			}
		}
	}

	// 4. Attempts.
	x.run(ctx, plans, route.Retry)
}

// reject finishes a request that never reached a provider.
func (x *exec) reject(ae *APIError) {
	x.sink.WriteError(x.meta, ae)
	x.finish(usage.StatusRejected, ae.Status, ae)
}

func (x *exec) finish(status string, httpStatus int, ae *APIError) {
	x.rec.Status = status
	x.rec.HTTPStatus = httpStatus
	if ae != nil {
		x.rec.ErrorCode, x.rec.ErrorMessage = ae.Code, ae.Message
	}
	x.rec.CompletedAt = x.e.now()
	code := ""
	if ae != nil {
		code = ae.Code
	}
	x.e.obs.RequestFinished(status, x.call.Req.Stream, code, x.rec.CompletedAt.Sub(x.start))
	x.e.record(x.rec)
}

// record persists asynchronously so the client is not held by the DB write.
// When too many writes are pending it records synchronously (backpressure).
func (e *Engine) record(r *usage.RequestRecord) {
	if e.recorder == nil {
		return
	}
	do := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ctx, span := tracer.Start(ctx, "gateway.record")
		defer span.End()
		if err := e.recorder.Record(ctx, r); err != nil {
			span.RecordError(err)
			e.obs.RecordFailed()
			e.log.Error("failed to record request", "error", err, "request_id", r.ID, "team_id", r.TeamID)
		}
	}
	select {
	case e.pending <- struct{}{}:
		e.wg.Add(1)
		go func() {
			defer func() { <-e.pending; e.wg.Done() }()
			do()
		}()
	default:
		do()
	}
}

// Drain waits for pending usage writes (graceful shutdown).
func (e *Engine) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var errBreakerOpen = &provider.Error{Kind: provider.KindServer, Message: "circuit breaker open"}

// run executes the attempt loop across targets.
func (x *exec) run(ctx context.Context, plans []*plan, rp RetryPolicy) {
	e := x.e
	var lastErr *provider.Error
	var lastPlan *plan
	total, skipped := 0, 0

targets:
	for _, p := range plans {
		br := e.breakers.Get(p.provider().ID.String())
		for k := 1; k <= rp.MaxAttemptsPerTarget; k++ {
			if total >= rp.TotalCap || ctx.Err() != nil {
				break targets
			}
			done, ok := br.Allow()
			if !ok {
				skipped++
				x.rec.Attempts = append(x.rec.Attempts, usage.AttemptRecord{
					ID: uuid.Must(uuid.NewV7()), Number: len(x.rec.Attempts) + 1, ProviderID: p.provider().ID, ModelID: p.model.ID,
					ProviderSlug: p.provider().Slug, ModelName: p.model.Name, Status: usage.StatusSkipped,
					ErrorKind: "circuit_open", ErrorMessage: "circuit breaker open", StartedAt: e.now(),
				})
				if lastErr == nil {
					lastErr = errBreakerOpen
				}
				e.obs.AttemptFinished(p.provider().Slug, p.model.Name, "circuit_open", 0)
				continue targets
			}
			if lastPlan != nil && lastPlan != p {
				x.rec.FallbackCount++
				e.obs.Fallback(lastPlan.provider().Slug, p.provider().Slug)
			}
			lastPlan = p
			total++

			committed, err := x.attempt(ctx, p, done)
			if err == nil || committed {
				return // success, or a streamed response that ended (well or badly) after commit
			}
			lastErr = err
			if !provider.Retryable(err) {
				if provider.Fallbackable(err) {
					continue targets
				}
				break targets
			}
			if k < rp.MaxAttemptsPerTarget {
				if err.RetryAfter > rp.MaxRetryAfter {
					continue targets // the provider asked us to back off longer than we are willing to wait
				}
				if e.sleep(ctx, rp.Backoff(k, err.RetryAfter, e.rnd)) != nil {
					break targets
				}
			}
		}
	}

	// All targets failed.
	if lastErr == nil {
		lastErr = &provider.Error{Kind: provider.KindServer, Message: "no attempt was made"}
	}
	if ctx.Err() != nil && x.clientCtx.Err() != nil {
		lastErr = &provider.Error{Kind: provider.KindCanceled, Message: "client closed request"}
	} else if ctx.Err() != nil {
		lastErr = &provider.Error{Kind: provider.KindTimeout, Message: "request deadline exceeded"}
	}
	var ae *APIError
	if total == 0 && skipped > 0 {
		ae = newErr(http.StatusServiceUnavailable, "server_error", "no_healthy_provider", "all providers for this model are temporarily unavailable (circuit open)")
	} else {
		ae = fromProviderError(lastErr)
	}
	x.meta.Attempts = total
	status := usage.StatusError
	if lastErr.Kind == provider.KindCanceled {
		status = usage.StatusClientCancelled
	}
	x.sink.WriteError(x.meta, ae)
	x.finish(status, ae.Status, ae)
}

// usageFor builds the ledger entry for an attempt.
func (x *exec) usageFor(p *plan, u provider.Usage) *usage.UsageRecord {
	ur := &usage.UsageRecord{Usage: u}
	if p.price != nil {
		id := p.price.ID
		ur.PricingID = &id
	}
	switch u.Status {
	case provider.UsageReported:
		if p.price != nil {
			c := pricing.Cost(*p.price, u)
			ur.CostNanos, ur.ChargeNanos = &c, c
		}
		x.e.obs.Tokens(p.provider().Slug, u)
	case provider.UsageUnknown:
		ur.ChargeNanos = p.estimate // conservative: never treat unknown as zero (ADR-007)
	}
	return ur
}

// failedUsage decides what usage an attempt that failed may have incurred.
func (x *exec) failedUsage(p *plan, pe *provider.Error, partial *provider.Usage) *usage.UsageRecord {
	switch {
	case pe.StatusCode > 0 && pe.Kind != provider.KindStreamInterrupted:
		return x.usageFor(p, provider.Usage{Status: provider.UsageNone}) // provider rejected it: not billable
	case !pe.Sent:
		return nil // never reached the provider
	default:
		u := provider.Usage{Status: provider.UsageUnknown}
		if partial != nil {
			u = *partial
			u.Status = provider.UsageUnknown
		}
		return x.usageFor(p, u)
	}
}

func breakerOutcome(err error) breaker.Outcome {
	if err == nil {
		return breaker.Success
	}
	if provider.BreakerFailure(err) {
		return breaker.Failure
	}
	return breaker.Neutral
}

func outcomeLabel(err *provider.Error) string {
	if err == nil {
		return "success"
	}
	return string(err.Kind)
}

var errFirstChunkTimeout = errors.New("timed out waiting for first chunk")

// attempt performs one provider call. committed is true once a streaming
// response has been started, after which the request is finished regardless of err.
func (x *exec) attempt(ctx context.Context, p *plan, done func(breaker.Outcome, string)) (committed bool, perr *provider.Error) {
	e := x.e
	prov := p.provider()
	adapter, ok := e.adapters[prov.Type]
	ar := usage.AttemptRecord{
		ID: uuid.Must(uuid.NewV7()), Number: len(x.rec.Attempts) + 1, ProviderID: prov.ID, ModelID: p.model.ID,
		ProviderSlug: prov.Slug, ModelName: p.model.Name, StartedAt: e.now(),
	}
	actx, span := tracer.Start(ctx, "gateway.attempt")
	span.SetAttributes(attribute.String("gateway.provider", prov.Slug), attribute.String("gateway.model", p.model.Name), attribute.Int("gateway.attempt", ar.Number))

	finishAttempt := func(status string, err *provider.Error, u *usage.UsageRecord) {
		ar.Status = status
		ar.Duration = e.now().Sub(ar.StartedAt)
		ar.Usage = u
		if err != nil {
			ar.ErrorKind, ar.ErrorMessage, ar.Retryable = string(err.Kind), err.Message, provider.Retryable(err)
			if err.StatusCode > 0 {
				sc := err.StatusCode
				ar.HTTPStatus = &sc
			}
			span.SetStatus(codes.Error, string(err.Kind))
		} else {
			sc := 200
			ar.HTTPStatus = &sc
		}
		msg := ""
		var outcomeErr error
		if err != nil {
			msg, outcomeErr = err.Error(), err
		}
		done(breakerOutcome(outcomeErr), msg)
		e.obs.BreakerState(prov.Slug, e.breakers.Get(prov.ID.String()).Snapshot().State)
		e.obs.AttemptFinished(prov.Slug, p.model.Name, outcomeLabel(err), ar.Duration)
		x.rec.Attempts = append(x.rec.Attempts, ar)
		span.End()
	}

	if !ok {
		err := &provider.Error{Kind: provider.KindBadRequest, Message: "no adapter for provider type " + prov.Type}
		finishAttempt(usage.StatusError, err, nil)
		return false, err
	}

	req := x.preq
	req.Model = p.model.Name
	cfg := provider.Config{BaseURL: prov.BaseURL, APIKey: prov.APIKey, HTTPClient: e.client}

	if !x.call.Req.Stream {
		actx, cancel := context.WithTimeout(actx, prov.Timeout)
		defer cancel()
		resp, err := adapter.ChatCompletion(actx, cfg, &req)
		if err != nil {
			pe := provider.FromTransport(actx, err)
			if pe.Kind == provider.KindTimeout && x.clientCtx.Err() != nil {
				pe = &provider.Error{Kind: provider.KindCanceled, Message: "client closed request", Sent: true}
			}
			finishAttempt(statusFor(pe), pe, x.failedUsage(p, pe, nil))
			return false, pe
		}
		u := x.usageFor(p, resp.Usage)
		finishAttempt(usage.StatusSuccess, nil, u)
		x.succeed(p, resp, u)
		return false, nil
	}

	// Streaming: wait for the first chunk before committing (ADR-009).
	sctx, cancel := context.WithCancelCause(actx)
	defer cancel(nil)
	timer := time.AfterFunc(prov.Timeout, func() { cancel(errFirstChunkTimeout) })
	classify := func(err error) *provider.Error {
		pe := provider.FromTransport(sctx, err)
		if errors.Is(context.Cause(sctx), errFirstChunkTimeout) {
			pe = &provider.Error{Kind: provider.KindTimeout, Message: "timed out waiting for the first chunk", Sent: true}
		} else if x.clientCtx.Err() != nil {
			pe = &provider.Error{Kind: provider.KindCanceled, Message: "client closed request", Sent: true}
		}
		return pe
	}

	stream, err := adapter.StreamChatCompletion(sctx, cfg, &req)
	if err != nil {
		timer.Stop()
		pe := classify(err)
		finishAttempt(statusFor(pe), pe, x.failedUsage(p, pe, nil))
		return false, pe
	}
	defer stream.Close()
	first, err := stream.Recv()
	timer.Stop()
	ended := false
	if err != nil {
		if errors.Is(err, errEOF) {
			ended = true
		} else {
			pe := classify(err)
			partial := stream.Usage()
			finishAttempt(statusFor(pe), pe, x.failedUsage(p, pe, &partial))
			return false, pe
		}
	}
	ttfb := e.now().Sub(ar.StartedAt)
	ar.TTFB = &ttfb
	x.rec.TTFB = durPtr(e.now().Sub(x.start))
	e.obs.StreamTTFB(prov.Slug, ttfb)

	status, perr := x.pipe(sctx, p, stream, first, ended)
	u := stream.Usage()
	var ur *usage.UsageRecord
	if perr != nil {
		ur = x.failedUsage(p, perr, &u)
		if ur == nil || ur.Usage.Status == provider.UsageNone {
			ur = x.usageFor(p, provider.Usage{Status: provider.UsageUnknown})
		}
	} else {
		ur = x.usageFor(p, u)
	}
	finishAttempt(status, perr, ur)
	x.rec.FinalProviderID, x.rec.FinalModelID = &prov.ID, &p.model.ID
	var ae *APIError
	if perr != nil {
		ae = &APIError{Code: string(perr.Kind), Message: perr.Message}
	}
	x.finish(status, http.StatusOK, ae)
	return true, perr
}

func durPtr(d time.Duration) *time.Duration { return &d }

func statusFor(pe *provider.Error) string {
	if pe.Kind == provider.KindCanceled {
		return usage.StatusClientCancelled
	}
	return usage.StatusError
}

// succeed writes a non-streaming response.
func (x *exec) succeed(p *plan, resp *provider.ChatResponse, u *usage.UsageRecord) {
	prov := p.provider()
	x.meta.Provider, x.meta.Model, x.meta.Attempts = prov.Slug, p.model.Name, countNonSkipped(x.rec.Attempts)
	model := resp.Model
	if model == "" {
		model = p.model.Name
	}
	fr := resp.FinishReason
	if fr == "" {
		fr = "stop"
	}
	out := &ChatCompletionResponse{
		ID: "chatcmpl-" + x.call.RequestID.String(), Object: "chat.completion", Created: x.start.Unix(), Model: model,
		Choices: []Choice{{Index: 0, Message: ResponseMessage{Role: "assistant", Content: resp.Content}, FinishReason: &fr}},
		Usage:   toAPIUsage(resp.Usage, u.CostNanos),
	}
	x.sink.WriteResponse(x.meta, out)
	x.rec.FinalProviderID, x.rec.FinalModelID = &prov.ID, &p.model.ID
	x.finish(usage.StatusSuccess, http.StatusOK, nil)
}

func countNonSkipped(as []usage.AttemptRecord) int {
	n := 0
	for _, a := range as {
		if a.Status != usage.StatusSkipped {
			n++
		}
	}
	return n
}
