package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"service/internal/app"
	"service/internal/config"
	"service/internal/mockllm"
	"service/internal/security"
	"service/internal/testutil"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t       *testing.T
	srv     *httptest.Server
	app     *app.App
	demoKey string
	admin   *http.Client
	logs    *syncBuf
}

const adminEmail, adminPassword = "admin@example.com", "correct-horse-battery"

func setup(t *testing.T) *env {
	pool := testutil.DB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	mock := httptest.NewServer(mockllm.Handler(time.Millisecond))
	t.Cleanup(mock.Close)

	demoKey, _, _ := security.GenerateAPIKey()
	cfg := &config.Config{
		EncryptionKey: bytes.Repeat([]byte{9}, 32), APIKeyPepper: []byte(strings.Repeat("p", 40)),
		AdminEmail: adminEmail, AdminPassword: adminPassword, SeedDemo: true, MockBaseURL: mock.URL, DemoAPIKey: demoKey,
		SessionTTL: time.Hour, DefaultTeamRPM: 1000, DefaultTeamConcurrency: 100, DefaultMaxOutputTokens: 1024,
		RequestTimeout: 20 * time.Second, StreamTimeout: 30 * time.Second, ReservationTTL: time.Hour, CatalogRefresh: time.Hour,
		BreakerFailureThreshold: 4, BreakerOpenDuration: time.Hour,
	}
	logs := &syncBuf{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := app.New(context.Background(), cfg, pool, rdb, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	e := &env{t: t, srv: srv, app: a, demoKey: demoKey, logs: logs}
	e.admin = e.login(adminEmail, adminPassword)
	return e
}

func (e *env) login(email, pw string) *http.Client {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp := e.do(c, "POST", "/api/v1/auth/login", map[string]any{"email": email, "password": pw}, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("login %s: %d %s", email, resp.StatusCode, body(resp))
	}
	resp.Body.Close()
	return c
}

func (e *env) do(c *http.Client, method, path string, in any, hdr map[string]string) *http.Response {
	e.t.Helper()
	var r io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func body(r *http.Response) string { b, _ := io.ReadAll(r.Body); r.Body.Close(); return string(b) }

func decode[T any](t *testing.T, r *http.Response, want int) T {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != want {
		t.Fatalf("status %d (want %d): %s", r.StatusCode, want, b)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

func (e *env) chat(key string, payload map[string]any) *http.Response {
	return e.do(http.DefaultClient, "POST", "/v1/chat/completions", payload, map[string]string{"Authorization": "Bearer " + key})
}

func msg(model string, stream bool) map[string]any {
	return map[string]any{"model": model, "stream": stream, "stream_options": map[string]any{"include_usage": true},
		"messages": []any{map[string]any{"role": "user", "content": "Explain microservices architecture"}}}
}

// readSSE returns data payloads of an event stream.
func readSSE(t *testing.T, r *http.Response) []string {
	t.Helper()
	defer r.Body.Close()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q: %s", ct, body(r))
	}
	var out []string
	sc := bufio.NewScanner(r.Body)
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			out = append(out, d)
		}
	}
	return out
}

type completion struct {
	Choices []struct {
		Message struct{ Content string } `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64  `json:"prompt_tokens"`
		CompletionTokens int64  `json:"completion_tokens"`
		TotalTokens      int64  `json:"total_tokens"`
		CostNanos        *int64 `json:"cost_nanos"`
	} `json:"usage"`
}

func TestEndToEnd(t *testing.T) {
	e := setup(t)

	t.Run("me", func(t *testing.T) {
		me := decode[map[string]any](t, e.do(e.admin, "GET", "/api/v1/auth/me", nil, nil), 200)
		if me["user"].(map[string]any)["role"] != "admin" || len(me["teams"].([]any)) != 1 {
			t.Fatalf("me %v", me)
		}
	})

	t.Run("auth errors", func(t *testing.T) {
		if r := e.chat("gwk_invalid", msg("gpt-4o", false)); r.StatusCode != 401 {
			t.Fatalf("invalid key: %d", r.StatusCode)
		}
		if r := e.chat(e.demoKey, msg("does-not-exist", false)); r.StatusCode != 404 {
			t.Fatalf("unknown model: %d %s", r.StatusCode, body(r))
		}
		if r := e.do(http.DefaultClient, "GET", "/api/v1/teams", nil, nil); r.StatusCode != 401 {
			t.Fatalf("no session: %d", r.StatusCode)
		}
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/teams", strings.NewReader("name=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if r, _ := e.admin.Do(req); r.StatusCode != 415 {
			t.Fatalf("csrf guard: %d", r.StatusCode)
		}
		if r := e.do(http.DefaultClient, "POST", "/api/v1/auth/login", map[string]any{"email": adminEmail, "password": "wrong-password!"}, nil); r.StatusCode != 401 {
			t.Fatalf("bad password: %d", r.StatusCode)
		}
	})

	t.Run("non-streaming per provider", func(t *testing.T) {
		for model, prov := range map[string]string{"gpt-4o": "openai-mock", "claude-sonnet-4-5": "anthropic-mock", "gemini-2.5-flash": "gemini-mock", "gemini-mock/gemini-2.5-flash": "gemini-mock"} {
			r := e.chat(e.demoKey, msg(model, false))
			if got := r.Header.Get("X-Gateway-Provider"); got != prov {
				t.Errorf("%s routed to %q", model, got)
			}
			c := decode[completion](t, r, 200)
			if !strings.Contains(c.Choices[0].Message.Content, "[mock") || c.Usage == nil || c.Usage.TotalTokens == 0 || c.Usage.CostNanos == nil || *c.Usage.CostNanos == 0 {
				t.Errorf("%s: %+v", model, c)
			}
		}
	})

	t.Run("fallback", func(t *testing.T) {
		r := e.chat(e.demoKey, msg("fallback-demo", false))
		if r.Header.Get("X-Gateway-Provider") != "anthropic-mock" || r.Header.Get("X-Gateway-Attempts") != "3" {
			t.Fatalf("headers %v", r.Header)
		}
		decode[completion](t, r, 200)
	})

	t.Run("streaming providers", func(t *testing.T) {
		for _, model := range []string{"smart", "claude-haiku-4-5", "fast"} {
			ev := readSSE(t, e.chat(e.demoKey, msg(model, true)))
			if len(ev) < 5 || ev[len(ev)-1] != "[DONE]" || !strings.Contains(ev[len(ev)-2], `"usage"`) {
				t.Fatalf("%s: events %v", model, ev)
			}
			var text strings.Builder
			for _, d := range ev[:len(ev)-2] {
				var ch struct {
					Choices []struct{ Delta struct{ Content string } } `json:"choices"`
				}
				_ = json.Unmarshal([]byte(d), &ch)
				if len(ch.Choices) > 0 {
					text.WriteString(ch.Choices[0].Delta.Content)
				}
			}
			if !strings.HasPrefix(text.String(), "[mock") {
				t.Fatalf("%s: text %q", model, text.String())
			}
		}
	})

	t.Run("stream interrupted mid-way", func(t *testing.T) {
		ev := readSSE(t, e.chat(e.demoKey, msg("mock-drop-stream", true)))
		last := ev[len(ev)-1]
		if !strings.Contains(last, "stream_interrupted") || strings.Contains(strings.Join(ev, ""), "[DONE]") {
			t.Fatalf("events %v", ev)
		}
	})

	if err := e.app.Engine.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Run("request logs", func(t *testing.T) {
		list := decode[struct {
			Data []struct {
				ID, Status, RequestedModel, CostStatus, UsageStatus string
				AttemptCount, FallbackCount                         int
				Requested                                           string `json:"requested_model"`
				Usage                                               string `json:"usage_status"`
				Cost                                                string `json:"cost_status"`
			} `json:"data"`
		}](t, e.do(e.admin, "GET", "/api/v1/requests?limit=100", nil, nil), 200)
		byModel := map[string]string{}
		var fallbackID string
		for _, r := range list.Data {
			byModel[r.Requested] = r.Status + "/" + r.Usage + "/" + r.Cost
			if r.Requested == "fallback-demo" {
				fallbackID = r.ID
			}
		}
		if byModel["gpt-4o"] != "success/reported/exact" || byModel["mock-drop-stream"] != "interrupted/unknown/estimated" || byModel["does-not-exist"] != "rejected/none/none" {
			t.Fatalf("logs %v", byModel)
		}
		d := decode[struct {
			FallbackCount int `json:"fallback_count"`
			Attempts      []struct {
				ProviderSlug string `json:"provider_slug"`
				Status       string
				HTTPStatus   *int    `json:"http_status"`
				UsageStatus  *string `json:"usage_status"`
			} `json:"attempts"`
		}](t, e.do(e.admin, "GET", "/api/v1/requests/"+fallbackID, nil, nil), 200)
		if len(d.Attempts) != 3 || d.FallbackCount != 1 || d.Attempts[0].Status != "error" || *d.Attempts[0].HTTPStatus != 503 ||
			*d.Attempts[0].UsageStatus != "none" || d.Attempts[2].ProviderSlug != "anthropic-mock" || d.Attempts[2].Status != "success" {
			t.Fatalf("detail %+v", d)
		}
	})

	t.Run("analytics", func(t *testing.T) {
		s := decode[struct {
			Summary struct {
				Requests, Succeeded, CostNanos, TotalTokens, Fallbacks int64
				Req                                                    int64 `json:"requests"`
				Cost                                                   int64 `json:"cost_nanos"`
				Tok                                                    int64 `json:"total_tokens"`
			} `json:"summary"`
		}](t, e.do(e.admin, "GET", "/api/v1/analytics/summary", nil, nil), 200)
		if s.Summary.Req < 8 || s.Summary.Cost == 0 || s.Summary.Tok == 0 {
			t.Fatalf("summary %+v", s)
		}
		b := decode[struct {
			Data []struct{ Label string } `json:"data"`
		}](t, e.do(e.admin, "GET", "/api/v1/analytics/breakdown?by=provider", nil, nil), 200)
		if len(b.Data) != 3 {
			t.Fatalf("breakdown %+v", b)
		}
		ts := decode[struct {
			Data []map[string]any `json:"data"`
		}](t, e.do(e.admin, "GET", "/api/v1/analytics/timeseries?interval=hour&from="+time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339), nil, nil), 200)
		if len(ts.Data) < 3 {
			t.Fatalf("timeseries %d points", len(ts.Data))
		}
	})

	// A new team with its own key, budget and limits.
	team := decode[map[string]any](t, e.do(e.admin, "POST", "/api/v1/teams", map[string]any{"name": "Billing Team", "rpm_limit": 3}, nil), 201)
	teamID := team["id"].(string)
	key := decode[map[string]any](t, e.do(e.admin, "POST", "/api/v1/teams/"+teamID+"/api-keys", map[string]any{"name": "ci"}, nil), 201)
	rawKey := key["key"].(string)

	t.Run("key listing never returns raw key", func(t *testing.T) {
		b := body(e.do(e.admin, "GET", "/api/v1/teams/"+teamID+"/api-keys", nil, nil))
		if strings.Contains(b, rawKey) || !strings.Contains(b, rawKey[:12]) {
			t.Fatalf("listing %s", b)
		}
		ps := body(e.do(e.admin, "GET", "/api/v1/providers", nil, nil))
		if strings.Contains(ps, "mock-key-") || strings.Contains(ps, "ciphertext") {
			t.Fatalf("provider credentials leaked: %s", ps)
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		codes := []int{}
		for i := 0; i < 4; i++ {
			r := e.chat(rawKey, msg("gpt-4o-mini", false))
			codes = append(codes, r.StatusCode)
			body(r)
		}
		if fmt.Sprint(codes) != "[200 200 200 429]" {
			t.Fatalf("codes %v", codes)
		}
	})

	t.Run("budget exhausted", func(t *testing.T) {
		decode[map[string]any](t, e.do(e.admin, "PATCH", "/api/v1/teams/"+teamID, map[string]any{"clear_rpm_limit": true}, nil), 200)
		decode[map[string]any](t, e.do(e.admin, "PUT", "/api/v1/teams/"+teamID+"/budget", map[string]any{"monthly_limit_nanos": 1}, nil), 200)
		r := e.chat(rawKey, msg("gpt-4o-mini", false))
		if r.StatusCode != 402 || !strings.Contains(body(r), "budget_exceeded") {
			t.Fatalf("expected 402, got %d", r.StatusCode)
		}
	})

	t.Run("revoked key", func(t *testing.T) {
		if r := e.do(e.admin, "DELETE", "/api/v1/teams/"+teamID+"/api-keys/"+key["id"].(string), nil, nil); r.StatusCode != 204 {
			t.Fatalf("revoke %d", r.StatusCode)
		}
		if r := e.chat(rawKey, msg("gpt-4o-mini", false)); r.StatusCode != 401 {
			t.Fatalf("revoked key accepted: %d", r.StatusCode)
		}
	})

	t.Run("team isolation", func(t *testing.T) {
		decode[map[string]any](t, e.do(e.admin, "POST", "/api/v1/users", map[string]any{"email": "dev@example.com", "name": "Dev", "password": "another-long-password"}, nil), 201)
		dev := e.login("dev@example.com", "another-long-password")
		if r := e.do(dev, "GET", "/api/v1/teams/"+teamID, nil, nil); r.StatusCode != 404 {
			t.Fatalf("non-member saw team: %d", r.StatusCode)
		}
		reqs := decode[struct{ Data []any }](t, e.do(dev, "GET", "/api/v1/requests", nil, nil), 200)
		if len(reqs.Data) != 0 {
			t.Fatalf("non-member sees %d requests", len(reqs.Data))
		}
		if r := e.do(dev, "POST", "/api/v1/providers", map[string]any{"name": "x", "type": "openai"}, nil); r.StatusCode != 403 {
			t.Fatalf("member created provider: %d", r.StatusCode)
		}
		if r := e.do(e.admin, "POST", "/api/v1/teams/"+teamID+"/members", map[string]any{"email": "dev@example.com", "role": "viewer"}, nil); r.StatusCode != 204 {
			t.Fatalf("add member: %d %s", r.StatusCode, body(r))
		}
		if r := e.do(dev, "GET", "/api/v1/teams/"+teamID, nil, nil); r.StatusCode != 200 {
			t.Fatalf("member cannot see team: %d", r.StatusCode)
		}
		if r := e.do(dev, "POST", "/api/v1/teams/"+teamID+"/api-keys", map[string]any{"name": "x"}, nil); r.StatusCode != 403 {
			t.Fatalf("viewer created key: %d", r.StatusCode)
		}
	})

	t.Run("circuit breaker opens", func(t *testing.T) {
		mockURL := e.app.Catalog.Snapshot().ProvidersBySlug["openai-mock"].BaseURL
		ctl := strings.TrimSuffix(mockURL, "/openai") + "/control"
		resp, err := http.Post(ctl, "application/json", strings.NewReader(`{"openai":"503"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// Explicit routing to openai: 2 failing attempts per request, threshold 4.
		for i := 0; i < 2; i++ {
			body(e.chat(e.demoKey, msg("openai-mock/gpt-4o", false)))
		}
		r := e.chat(e.demoKey, msg("openai-mock/gpt-4o", false))
		if r.StatusCode != 503 || !strings.Contains(body(r), "no_healthy_provider") {
			t.Fatalf("breaker did not open: %d", r.StatusCode)
		}
		// The "smart" policy skips the open provider and serves from Anthropic.
		r = e.chat(e.demoKey, msg("smart", false))
		if r.StatusCode != 200 || r.Header.Get("X-Gateway-Provider") != "anthropic-mock" {
			t.Fatalf("policy did not skip open circuit: %d %s", r.StatusCode, r.Header.Get("X-Gateway-Provider"))
		}
		body(r)
		rel := body(e.do(e.admin, "GET", "/api/v1/reliability", nil, nil))
		if !strings.Contains(rel, `"state":"open"`) {
			t.Fatalf("reliability does not show open circuit: %s", rel[:min(400, len(rel))])
		}
	})

	t.Run("metrics", func(t *testing.T) {
		m := body(e.do(http.DefaultClient, "GET", "/metrics", nil, nil))
		for _, name := range []string{"llm_gateway_requests_total", "llm_gateway_provider_requests_total", "llm_gateway_fallback_total",
			`llm_gateway_circuit_breaker_state{provider="openai-mock"} 2`, "llm_gateway_rate_limited_total", "llm_gateway_budget_rejections_total", "go_goroutines"} {
			if !strings.Contains(m, name) {
				t.Errorf("metrics missing %s", name)
			}
		}
		if strings.Contains(m, e.demoKey) || strings.Contains(m, teamID) {
			t.Error("metrics contain high-cardinality or secret values")
		}
	})

	t.Run("login throttling is per account", func(t *testing.T) {
		// Concurrent so all attempts land inside one sliding window (bcrypt is slow under -race).
		var mu sync.Mutex
		var wg sync.WaitGroup
		codes := map[int]int{}
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := e.do(http.DefaultClient, "POST", "/api/v1/auth/login", map[string]any{"email": "nobody@example.com", "password": "wrong-password!"}, nil)
				body(r)
				mu.Lock()
				codes[r.StatusCode]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		if codes[401]+codes[429] != 12 || codes[401] > 10 || codes[429] < 2 {
			t.Fatalf("codes %v", codes)
		}
		// Another account from the same IP is unaffected.
		e.login(adminEmail, adminPassword)
	})

	t.Run("logs contain no secrets or prompts", func(t *testing.T) {
		_ = e.app.Engine.Drain(context.Background())
		l := e.logs.String()
		for _, secret := range []string{e.demoKey, rawKey, "mock-key-openai-mock", adminPassword, "Explain microservices"} {
			if strings.Contains(l, secret) {
				t.Errorf("logs contain %q", secret[:min(12, len(secret))])
			}
		}
		if !strings.Contains(l, `"route":"/v1/chat/completions"`) {
			t.Error("expected access log lines")
		}
	})
}
