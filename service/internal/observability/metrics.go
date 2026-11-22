// Package observability provides Prometheus metrics, OpenTelemetry tracing,
// structured logging and the HTTP middleware that ties them together.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"service/internal/breaker"
	"service/internal/provider"
)

// Metrics holds all gateway metrics. Labels are bounded: provider slugs and model
// names come from admin configuration, statuses and codes from fixed enums.
// Team IDs, request IDs and API keys are never used as labels.
type Metrics struct {
	Registry *prometheus.Registry

	requests         *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	requestErrors    *prometheus.CounterVec
	providerRequests *prometheus.CounterVec
	providerDuration *prometheus.HistogramVec
	fallbacks        *prometheus.CounterVec
	breakerState     *prometheus.GaugeVec
	rateLimited      *prometheus.CounterVec
	budgetRejections prometheus.Counter
	tokens           *prometheus.CounterVec
	streamTTFB       *prometheus.HistogramVec
	recordFailures   prometheus.Counter
	httpRequests     *prometheus.CounterVec
	httpDuration     *prometheus.HistogramVec
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	latency := []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120}
	m := &Metrics{
		Registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_requests_total",
			Help: "Gateway chat requests by final status."}, []string{"status", "stream"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "llm_gateway_request_duration_seconds",
			Help: "End-to-end gateway request duration.", Buckets: latency}, []string{"stream"}),
		requestErrors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_request_errors_total",
			Help: "Gateway request errors by error code."}, []string{"code"}),
		providerRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_provider_requests_total",
			Help: "Provider attempts by outcome."}, []string{"provider", "model", "outcome"}),
		providerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "llm_gateway_provider_request_duration_seconds",
			Help: "Provider attempt duration.", Buckets: latency}, []string{"provider"}),
		fallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_fallback_total",
			Help: "Fallbacks from one provider to another."}, []string{"from_provider", "to_provider"}),
		breakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "llm_gateway_circuit_breaker_state",
			Help: "Circuit breaker state per provider (0=closed, 1=half_open, 2=open)."}, []string{"provider"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_rate_limited_total",
			Help: "Requests rejected (or degraded) by the gateway rate limiter."}, []string{"reason"}),
		budgetRejections: prometheus.NewCounter(prometheus.CounterOpts{Name: "llm_gateway_budget_rejections_total",
			Help: "Requests rejected because a team budget was exhausted."}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_tokens_total",
			Help: "Provider-reported tokens by category."}, []string{"provider", "type"}),
		streamTTFB: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "llm_gateway_stream_time_to_first_byte_seconds",
			Help: "Time from attempt start to first streamed chunk.", Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30}}, []string{"provider"}),
		recordFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "llm_gateway_usage_record_failures_total",
			Help: "Failures persisting request usage to Postgres."}),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_gateway_http_requests_total",
			Help: "HTTP requests by route template and status code."}, []string{"method", "route", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "llm_gateway_http_request_duration_seconds",
			Help: "HTTP request duration by route template.", Buckets: latency}, []string{"method", "route"}),
	}
	reg.MustRegister(m.requests, m.requestDuration, m.requestErrors, m.providerRequests, m.providerDuration, m.fallbacks,
		m.breakerState, m.rateLimited, m.budgetRejections, m.tokens, m.streamTTFB, m.recordFailures, m.httpRequests, m.httpDuration)
	return m
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// gateway.Observer implementation ------------------------------------------------------

func (m *Metrics) RequestFinished(status string, stream bool, code string, d time.Duration) {
	m.requests.WithLabelValues(status, boolLabel(stream)).Inc()
	m.requestDuration.WithLabelValues(boolLabel(stream)).Observe(d.Seconds())
	if code != "" && status != "success" {
		m.requestErrors.WithLabelValues(code).Inc()
	}
}

func (m *Metrics) AttemptFinished(prov, model, outcome string, d time.Duration) {
	m.providerRequests.WithLabelValues(prov, model, outcome).Inc()
	if outcome != "circuit_open" {
		m.providerDuration.WithLabelValues(prov).Observe(d.Seconds())
	}
}

func (m *Metrics) Fallback(from, to string)  { m.fallbacks.WithLabelValues(from, to).Inc() }
func (m *Metrics) RateLimited(reason string) { m.rateLimited.WithLabelValues(reason).Inc() }
func (m *Metrics) BudgetRejected()           { m.budgetRejections.Inc() }
func (m *Metrics) RecordFailed()             { m.recordFailures.Inc() }

func (m *Metrics) Tokens(prov string, u provider.Usage) {
	m.tokens.WithLabelValues(prov, "input").Add(float64(u.InputTokens))
	m.tokens.WithLabelValues(prov, "cached_input").Add(float64(u.CachedInputTokens))
	m.tokens.WithLabelValues(prov, "cache_write").Add(float64(u.CacheWriteTokens))
	m.tokens.WithLabelValues(prov, "output").Add(float64(u.OutputTokens))
}

func (m *Metrics) StreamTTFB(prov string, d time.Duration) {
	m.streamTTFB.WithLabelValues(prov).Observe(d.Seconds())
}

func (m *Metrics) BreakerState(prov string, s breaker.State) {
	m.breakerState.WithLabelValues(prov).Set(float64(s))
}

func (m *Metrics) ObserveHTTP(method, route string, code int, d time.Duration) {
	m.httpRequests.WithLabelValues(method, route, statusClass(code)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(d.Seconds())
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}
