package gateway

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"service/internal/breaker"
	"service/internal/budget"
	"service/internal/pricing"
	"service/internal/provider"
	"service/internal/ratelimit"
	"service/internal/usage"
)

// --- fakes ---------------------------------------------------------------------

type step struct {
	resp   *provider.ChatResponse
	err    error
	chunks []string // stream chunks
	midErr error    // error after chunks (nil = clean end)
	usage  provider.Usage
	block  bool // block until ctx done
}

type fakeAdapter struct {
	typ   string
	mu    sync.Mutex
	steps map[string][]step // by upstream model name
	calls map[string]int
}

func newFake(typ string) *fakeAdapter {
	return &fakeAdapter{typ: typ, steps: map[string][]step{}, calls: map[string]int{}}
}

func (f *fakeAdapter) on(model string, s ...step) { f.steps[model] = append(f.steps[model], s...) }

func (f *fakeAdapter) next(model string) step {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls[model]
	f.calls[model]++
	ss := f.steps[model]
	if len(ss) == 0 {
		return step{err: &provider.Error{Kind: provider.KindServer, StatusCode: 500, Message: "no script", Sent: true}}
	}
	if i >= len(ss) {
		return ss[len(ss)-1]
	}
	return ss[i]
}

func (f *fakeAdapter) count(model string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[model]
}

func (f *fakeAdapter) Type() string { return f.typ }

func (f *fakeAdapter) ChatCompletion(ctx context.Context, _ provider.Config, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	s := f.next(req.Model)
	if s.block {
		<-ctx.Done()
		return nil, provider.FromTransport(ctx, ctx.Err())
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (f *fakeAdapter) StreamChatCompletion(ctx context.Context, _ provider.Config, req *provider.ChatRequest) (provider.Stream, error) {
	s := f.next(req.Model)
	if s.err != nil {
		return nil, s.err
	}
	return &fakeStream{ctx: ctx, s: s}, nil
}

type fakeStream struct {
	ctx context.Context
	s   step
	i   int
}

func (fs *fakeStream) Recv() (*provider.StreamChunk, error) {
	if fs.i < len(fs.s.chunks) {
		c := fs.s.chunks[fs.i]
		fs.i++
		return &provider.StreamChunk{Delta: c}, nil
	}
	if fs.s.block {
		<-fs.ctx.Done()
		return nil, provider.StreamReadError(fs.ctx, fs.ctx.Err())
	}
	if fs.s.midErr != nil {
		return nil, fs.s.midErr
	}
	return nil, io.EOF
}

func (fs *fakeStream) Usage() provider.Usage {
	if fs.s.midErr != nil || fs.s.block {
		return provider.Usage{Status: provider.UsageUnknown}
	}
	return fs.s.usage
}
func (fs *fakeStream) Close() error { return nil }

type memRecorder struct {
	mu   sync.Mutex
	recs []*usage.RequestRecord
}

func (m *memRecorder) Record(_ context.Context, r *usage.RequestRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, r)
	return nil
}

type fakeBudget struct {
	has      bool
	exceed   bool
	reserved []int64
	released int
}

func (b *fakeBudget) Reserve(_ context.Context, team, req uuid.UUID, amount int64) (*budget.Reservation, bool, error) {
	if !b.has {
		return nil, false, nil
	}
	if b.exceed {
		return nil, true, &budget.ExceededError{Period: "month", Limit: 1}
	}
	b.reserved = append(b.reserved, amount)
	return &budget.Reservation{ID: uuid.New(), TeamID: team, Amount: amount}, true, nil
}

func (b *fakeBudget) Release(context.Context, *budget.Reservation) error { b.released++; return nil }

type fakeLimiter struct{ err error }

func (l *fakeLimiter) Acquire(context.Context, string, int, int, time.Duration) (func(), bool, error) {
	if l.err != nil {
		return nil, false, l.err
	}
	return func() {}, false, nil
}

type sink struct {
	mu      sync.Mutex
	errResp *APIError
	resp    *ChatCompletionResponse
	meta    Meta
	events  []any
	done    bool
	failAt  int // fail Send after this many events (0 = never)
	onSend  func(n int)
}

func (s *sink) WriteError(m Meta, e *APIError)                  { s.meta, s.errResp = m, e }
func (s *sink) WriteResponse(m Meta, r *ChatCompletionResponse) { s.meta, s.resp = m, r }
func (s *sink) StartStream(m Meta) StreamWriter                 { s.meta = m; return s }
func (s *sink) Done() error                                     { s.done = true; return nil }
func (s *sink) Send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, v)
	if s.onSend != nil {
		s.onSend(len(s.events))
	}
	if s.failAt > 0 && len(s.events) >= s.failAt {
		return errors.New("broken pipe")
	}
	return nil
}

func (s *sink) text() string {
	var sb strings.Builder
	for _, e := range s.events {
		if c, ok := e.(*ChatCompletionChunk); ok && len(c.Choices) > 0 {
			sb.WriteString(c.Choices[0].Delta.Content)
		}
	}
	return sb.String()
}

func (s *sink) hasErrorEvent() bool {
	for _, e := range s.events {
		if m, ok := e.(map[string]any); ok && m["error"] != nil {
			return true
		}
	}
	return false
}

// --- fixture -------------------------------------------------------------------

type fixture struct {
	e       *Engine
	rec     *memRecorder
	oa, an  *fakeAdapter
	sleeps  []time.Duration
	budget  *fakeBudget
	limiter *fakeLimiter
}

var price = pricing.Price{ID: uuid.New(), InputPerM: 1_000_000_000, OutputPerM: 2_000_000_000} // $1 / $2 per 1M

func newFixture(t *testing.T, threshold int) *fixture {
	t.Helper()
	pOA := ProviderEntry{ID: uuid.New(), Slug: "openai", Type: "openai", Enabled: true, Priority: 10, Timeout: 50 * time.Millisecond}
	pAN := ProviderEntry{ID: uuid.New(), Slug: "anthropic", Type: "anthropic", Enabled: true, Priority: 20, Timeout: 50 * time.Millisecond}
	mOA, mAN, mFree := uuid.New(), uuid.New(), uuid.New()
	p1, p2 := price, price
	p1.ModelID, p2.ModelID = mOA, mAN
	snap := BuildSnapshot(SnapshotData{
		Providers: []ProviderEntry{pOA, pAN},
		Models: []ModelRow{
			{ID: mOA, ProviderID: pOA.ID, Name: "gpt", Enabled: true},
			{ID: mAN, ProviderID: pAN.ID, Name: "claude", Enabled: true},
			{ID: mFree, ProviderID: pOA.ID, Name: "unpriced", Enabled: true},
		},
		Prices:   []pricing.Price{p1, p2},
		Policies: []PolicyRow{{ID: uuid.New(), Alias: "smart", Enabled: true, MaxAttemptsPerTarget: 2, RetryBaseMs: 10, RetryMaxMs: 100, TargetModelIDs: []uuid.UUID{mOA, mAN}}},
	}, time.Now())

	f := &fixture{rec: &memRecorder{}, oa: newFake("openai"), an: newFake("anthropic"), budget: &fakeBudget{}, limiter: &fakeLimiter{}}
	f.e = NewEngine(Deps{
		Catalog:  NewStaticCatalog(snap),
		Adapters: []provider.Adapter{f.oa, f.an},
		Breakers: breaker.NewRegistry(breaker.Settings{FailureThreshold: threshold, OpenDuration: time.Hour}, nil, nil),
		Limiter:  f.limiter,
		Budget:   f.budget,
		Recorder: f.rec,
	}, Options{DefaultMaxTokens: 100})
	f.e.sleep = func(_ context.Context, d time.Duration) error { f.sleeps = append(f.sleeps, d); return nil }
	f.e.rnd = func() float64 { return 1 }
	return f
}

func (f *fixture) do(t *testing.T, ctx context.Context, model string, stream bool) (*sink, *usage.RequestRecord) {
	t.Helper()
	s := &sink{}
	req := &ChatCompletionRequest{Model: model, Stream: stream, StreamOptions: &StreamOptions{IncludeUsage: true},
		Messages: []ChatMessage{{Role: "user", Content: "hello world"}}}
	if e := req.Validate(); e != nil {
		t.Fatal(e)
	}
	f.e.Execute(ctx, &Call{RequestID: uuid.New(), TeamID: uuid.New(), Source: "api", RPMLimit: 10, Concurrency: 10, Req: req}, s)
	if err := f.e.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	if len(f.rec.recs) == 0 {
		return s, nil
	}
	return s, f.rec.recs[len(f.rec.recs)-1]
}

func okResp(text string) *provider.ChatResponse {
	return &provider.ChatResponse{Content: text, FinishReason: "stop", Usage: provider.Usage{Status: provider.UsageReported, InputTokens: 1000, OutputTokens: 500}}
}

func e503() error {
	return &provider.Error{Kind: provider.KindServer, StatusCode: 503, Message: "unavailable", Sent: true}
}

// --- tests ---------------------------------------------------------------------

func TestNonStreamSuccess(t *testing.T) {
	f := newFixture(t, 5)
	f.oa.on("gpt", step{resp: okResp("hi")})
	s, rec := f.do(t, context.Background(), "gpt", false)
	if s.resp == nil || s.resp.Choices[0].Message.Content != "hi" || s.resp.Usage.TotalTokens != 1500 {
		t.Fatalf("response %+v err %+v", s.resp, s.errResp)
	}
	if rec.Status != usage.StatusSuccess || len(rec.Attempts) != 1 || rec.Attempts[0].Usage.Usage.Status != provider.UsageReported {
		t.Fatalf("record %+v", rec)
	}
	tot := rec.Totals()
	// 1000 in * $1/M + 500 out * $2/M = 1000 + 1000 micro = 2,000,000 nanos
	if tot.CostStatus != usage.CostExact || *tot.CostNanos != 2_000_000 || tot.ChargeNanos != 2_000_000 {
		t.Fatalf("totals %+v", tot)
	}
	if s.meta.Provider != "openai" || s.meta.Attempts != 1 {
		t.Fatalf("meta %+v", s.meta)
	}
}

func TestRetryThenFallback(t *testing.T) {
	f := newFixture(t, 10)
	f.oa.on("gpt", step{err: e503()}, step{err: e503()})
	f.an.on("claude", step{resp: okResp("from claude")})
	s, rec := f.do(t, context.Background(), "smart", false)
	if s.resp == nil || s.resp.Choices[0].Message.Content != "from claude" {
		t.Fatalf("expected fallback response, got %+v", s.errResp)
	}
	if f.oa.count("gpt") != 2 || f.an.count("claude") != 1 {
		t.Fatalf("calls oa=%d an=%d", f.oa.count("gpt"), f.an.count("claude"))
	}
	if len(rec.Attempts) != 3 || rec.FallbackCount != 1 || rec.Attempts[0].Status != usage.StatusError || rec.Attempts[2].Status != usage.StatusSuccess {
		t.Fatalf("record %+v", rec)
	}
	if len(f.sleeps) != 1 || f.sleeps[0] != 10*time.Millisecond {
		t.Fatalf("expected one backoff of base delay, got %v", f.sleeps)
	}
	// failed HTTP attempts are recorded as not billable
	if rec.Attempts[0].Usage.Usage.Status != provider.UsageNone {
		t.Fatal("5xx attempt should have usage none")
	}
}

func TestNonRetryableStopsImmediately(t *testing.T) {
	f := newFixture(t, 5)
	f.oa.on("gpt", step{err: &provider.Error{Kind: provider.KindBadRequest, StatusCode: 400, Message: "bad", Sent: true}})
	s, rec := f.do(t, context.Background(), "smart", false)
	if s.errResp == nil || s.errResp.Status != 400 || f.an.count("claude") != 0 || f.oa.count("gpt") != 1 {
		t.Fatalf("err %+v", s.errResp)
	}
	if rec.Status != usage.StatusError || len(rec.Attempts) != 1 {
		t.Fatalf("record %+v", rec)
	}
}

func TestRetryAfterHonoured(t *testing.T) {
	f := newFixture(t, 10)
	f.oa.on("gpt", step{err: &provider.Error{Kind: provider.KindRateLimited, StatusCode: 429, RetryAfter: 3 * time.Second, Sent: true}}, step{resp: okResp("ok")})
	s, _ := f.do(t, context.Background(), "gpt", false)
	if s.resp == nil || len(f.sleeps) != 1 || f.sleeps[0] != 3*time.Second {
		t.Fatalf("sleeps %v resp %v", f.sleeps, s.errResp)
	}
	// Too long Retry-After falls back instead of waiting.
	f2 := newFixture(t, 10)
	f2.oa.on("gpt", step{err: &provider.Error{Kind: provider.KindRateLimited, StatusCode: 429, RetryAfter: time.Minute, Sent: true}})
	f2.an.on("claude", step{resp: okResp("ok")})
	s2, _ := f2.do(t, context.Background(), "smart", false)
	if s2.resp == nil || len(f2.sleeps) != 0 || f2.oa.count("gpt") != 1 {
		t.Fatalf("should skip to fallback: sleeps=%v", f2.sleeps)
	}
}

func TestAllFail(t *testing.T) {
	f := newFixture(t, 10)
	f.oa.on("gpt", step{err: e503()})
	f.an.on("claude", step{err: e503()})
	s, rec := f.do(t, context.Background(), "smart", false)
	if s.errResp == nil || s.errResp.Status != 502 || s.errResp.Code != "upstream_error" || len(rec.Attempts) != 4 {
		t.Fatalf("err %+v attempts %d", s.errResp, len(rec.Attempts))
	}
}

func TestCircuitBreakerSkipsUnhealthyProvider(t *testing.T) {
	f := newFixture(t, 2)
	f.oa.on("gpt", step{err: e503()})
	f.an.on("claude", step{resp: okResp("ok")})
	f.do(t, context.Background(), "smart", false) // 2 failures → openai circuit opens
	before := f.oa.count("gpt")
	s, rec := f.do(t, context.Background(), "smart", false)
	if f.oa.count("gpt") != before {
		t.Fatal("open circuit must prevent calls")
	}
	if s.resp == nil || rec.Attempts[0].Status != usage.StatusSkipped || rec.Attempts[0].ErrorKind != "circuit_open" {
		t.Fatalf("record %+v", rec.Attempts)
	}
	// Explicit routing to the open provider yields 503 no_healthy_provider.
	s, _ = f.do(t, context.Background(), "openai/gpt", false)
	if s.errResp == nil || s.errResp.Code != "no_healthy_provider" || s.errResp.Status != 503 {
		t.Fatalf("err %+v", s.errResp)
	}
}

func TestTimeoutYieldsUnknownUsageChargedAtEstimate(t *testing.T) {
	f := newFixture(t, 10)
	f.budget.has = true
	f.oa.on("gpt", step{block: true})
	s, rec := f.do(t, context.Background(), "openai/gpt", false)
	if s.errResp == nil || s.errResp.Code != "upstream_timeout" {
		t.Fatalf("err %+v", s.errResp)
	}
	a := rec.Attempts[0]
	if a.ErrorKind != string(provider.KindTimeout) || a.Usage.Usage.Status != provider.UsageUnknown {
		t.Fatalf("attempt %+v", a)
	}
	tot := rec.Totals()
	// Timeouts are retried once, so two attempts each count their estimate.
	if len(rec.Attempts) != 2 || tot.ChargeNanos != 2*f.budget.reserved[0] || tot.CostStatus != usage.CostEstimated {
		t.Fatalf("unknown usage must be charged the estimate: %+v reserved %v", tot, f.budget.reserved)
	}
}

func TestBudgetExceededRejectsBeforeProvider(t *testing.T) {
	f := newFixture(t, 5)
	f.budget.has, f.budget.exceed = true, true
	f.oa.on("gpt", step{resp: okResp("x")})
	s, rec := f.do(t, context.Background(), "gpt", false)
	if s.errResp == nil || s.errResp.Status != 402 || s.errResp.Code != "budget_exceeded" || f.oa.count("gpt") != 0 {
		t.Fatalf("err %+v", s.errResp)
	}
	if rec.Status != usage.StatusRejected {
		t.Fatalf("status %s", rec.Status)
	}
}

func TestUnpricedModelWithBudget(t *testing.T) {
	f := newFixture(t, 5)
	f.budget.has = true
	s, _ := f.do(t, context.Background(), "unpriced", false)
	if s.errResp == nil || s.errResp.Code != "pricing_not_configured" || f.budget.released != 1 {
		t.Fatalf("err %+v released %d", s.errResp, f.budget.released)
	}
	// Without a budget the unpriced model works, cost unknown.
	f2 := newFixture(t, 5)
	f2.oa.on("unpriced", step{resp: okResp("x")})
	s2, rec := f2.do(t, context.Background(), "unpriced", false)
	if s2.resp == nil || rec.Totals().CostStatus != usage.CostUnknown || rec.Totals().CostNanos != nil {
		t.Fatalf("totals %+v", rec.Totals())
	}
}

func TestRateLimitedNotRecorded(t *testing.T) {
	f := newFixture(t, 5)
	f.limiter.err = &ratelimit.LimitError{Err: ratelimit.ErrRateExceeded, RetryAfter: time.Second}
	s, rec := f.do(t, context.Background(), "gpt", false)
	if s.errResp == nil || s.errResp.Status != 429 || s.errResp.Code != "rate_limit_exceeded" || rec != nil {
		t.Fatalf("err %+v rec %v", s.errResp, rec)
	}
	f.limiter.err = &ratelimit.LimitError{Err: ratelimit.ErrConcurrencyExceeded}
	s, _ = f.do(t, context.Background(), "gpt", false)
	if s.errResp.Code != "concurrency_limit_exceeded" {
		t.Fatalf("code %s", s.errResp.Code)
	}
	f.limiter.err = ratelimit.ErrUnavailable
	s, _ = f.do(t, context.Background(), "gpt", false)
	if s.errResp.Status != 503 {
		t.Fatalf("redis down must fail closed: %+v", s.errResp)
	}
}

func TestUnknownModel(t *testing.T) {
	f := newFixture(t, 5)
	s, rec := f.do(t, context.Background(), "nope", false)
	if s.errResp == nil || s.errResp.Status != 404 || rec.Status != usage.StatusRejected {
		t.Fatalf("err %+v", s.errResp)
	}
}

// --- streaming -------------------------------------------------------------------

func TestStreamSuccessWithUsage(t *testing.T) {
	f := newFixture(t, 5)
	f.oa.on("gpt", step{chunks: []string{"Hel", "lo"}, usage: provider.Usage{Status: provider.UsageReported, InputTokens: 10, OutputTokens: 2}})
	s, rec := f.do(t, context.Background(), "gpt", true)
	if s.text() != "Hello" || !s.done || s.hasErrorEvent() {
		t.Fatalf("text %q done %v", s.text(), s.done)
	}
	last := s.events[len(s.events)-1].(*ChatCompletionChunk)
	if last.Usage == nil || last.Usage.TotalTokens != 12 || last.Usage.CostNanos == nil {
		t.Fatalf("expected final usage chunk, got %+v", last)
	}
	if first := s.events[0].(*ChatCompletionChunk); first.Choices[0].Delta.Role != "assistant" {
		t.Fatal("first chunk must carry the assistant role")
	}
	if rec.Status != usage.StatusSuccess || rec.HTTPStatus != 200 || rec.TTFB == nil || rec.Totals().CostStatus != usage.CostExact {
		t.Fatalf("record %+v", rec)
	}
}

func TestStreamFailoverBeforeFirstChunk(t *testing.T) {
	f := newFixture(t, 10)
	// openai: HTTP 503 on open, then a stream that dies before the first chunk
	f.oa.on("gpt", step{err: e503()}, step{midErr: &provider.Error{Kind: provider.KindStreamInterrupted, Message: "eof", Sent: true}})
	f.an.on("claude", step{chunks: []string{"from ", "claude"}, usage: provider.Usage{Status: provider.UsageReported, InputTokens: 1, OutputTokens: 1}})
	s, rec := f.do(t, context.Background(), "smart", true)
	if s.text() != "from claude" || !s.done || s.hasErrorEvent() {
		t.Fatalf("client must only see the fallback stream: %q", s.text())
	}
	if len(rec.Attempts) != 3 || rec.FallbackCount != 1 || s.meta.Provider != "anthropic" {
		t.Fatalf("attempts %d fallbacks %d", len(rec.Attempts), rec.FallbackCount)
	}
	if rec.Attempts[1].Usage == nil || rec.Attempts[1].Usage.Usage.Status != provider.UsageUnknown {
		t.Fatal("stream that died after acceptance must have unknown usage")
	}
}

func TestStreamInterruptedAfterCommit(t *testing.T) {
	f := newFixture(t, 10)
	f.budget.has = true
	f.oa.on("gpt", step{chunks: []string{"partial"}, midErr: &provider.Error{Kind: provider.KindStreamInterrupted, Message: "reset", Sent: true}})
	f.an.on("claude", step{chunks: []string{"should not be used"}})
	s, rec := f.do(t, context.Background(), "smart", true)
	if s.text() != "partial" || s.done || !s.hasErrorEvent() {
		t.Fatalf("expected partial text + error event, no [DONE]: %q done=%v", s.text(), s.done)
	}
	if f.an.count("claude") != 0 {
		t.Fatal("must not fall back after the first chunk")
	}
	if rec.Status != usage.StatusInterrupted || rec.Attempts[0].Usage.Usage.Status != provider.UsageUnknown {
		t.Fatalf("record %+v", rec)
	}
	if tot := rec.Totals(); tot.ChargeNanos == 0 || tot.CostStatus != usage.CostEstimated {
		t.Fatalf("interrupted stream must not be free: %+v", tot)
	}
}

func TestStreamClientDisconnect(t *testing.T) {
	f := newFixture(t, 10)
	f.oa.on("gpt", step{chunks: []string{"a", "b"}, block: true})
	ctx, cancel := context.WithCancel(context.Background())
	s := &sink{onSend: func(n int) {
		if n == 2 {
			cancel()
		}
	}}
	req := &ChatCompletionRequest{Model: "gpt", Stream: true, Messages: []ChatMessage{{Role: "user", Content: "x"}}}
	f.e.Execute(ctx, &Call{RequestID: uuid.New(), TeamID: uuid.New(), Source: "api", RPMLimit: 1, Concurrency: 1, Req: req}, s)
	_ = f.e.Drain(context.Background())
	rec := f.rec.recs[0]
	if rec.Status != usage.StatusClientCancelled || rec.Attempts[0].Usage.Usage.Status != provider.UsageUnknown {
		t.Fatalf("record status %s attempts %+v", rec.Status, rec.Attempts)
	}
	if f.e.breakers.Get(rec.Attempts[0].ProviderID.String()).Snapshot().ConsecutiveFailures != 0 {
		t.Fatal("client cancellation must not count as provider failure")
	}
}

func TestBackoff(t *testing.T) {
	p := RetryPolicy{Base: 100 * time.Millisecond, Max: time.Second}
	one := func() float64 { return 1 }
	if p.Backoff(1, 0, one) != 100*time.Millisecond || p.Backoff(3, 0, one) != 400*time.Millisecond || p.Backoff(10, 0, one) != time.Second {
		t.Fatal("exponential backoff capped at max")
	}
	if p.Backoff(1, 0, func() float64 { return 0.5 }) != 50*time.Millisecond {
		t.Fatal("full jitter")
	}
	if p.Backoff(1, 2*time.Second, one) != 2*time.Second {
		t.Fatal("retry-after wins when larger")
	}
}

func TestValidate(t *testing.T) {
	two := 2
	hot := 3.0
	cases := map[string]ChatCompletionRequest{
		"no model":    {Messages: []ChatMessage{{Role: "user", Content: "x"}}},
		"no messages": {Model: "m"},
		"bad role":    {Model: "m", Messages: []ChatMessage{{Role: "tool", Content: "x"}}},
		"only system": {Model: "m", Messages: []ChatMessage{{Role: "system", Content: "x"}}},
		"n":           {Model: "m", N: &two, Messages: []ChatMessage{{Role: "user", Content: "x"}}},
		"temperature": {Model: "m", Temperature: &hot, Messages: []ChatMessage{{Role: "user", Content: "x"}}},
		"tools":       {Model: "m", Tools: []byte(`[{}]`), Messages: []ChatMessage{{Role: "user", Content: "x"}}},
	}
	for name, r := range cases {
		if r.Validate() == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	ok := ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "developer", Content: "s"}, {Role: "user", Content: "x"}}}
	if ok.Validate() != nil || ok.Messages[0].Role != "system" {
		t.Fatal("developer role maps to system")
	}
}
