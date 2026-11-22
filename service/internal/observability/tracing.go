package observability

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// SetupTracing configures a global OTLP/HTTP tracer provider. With an empty
// endpoint tracing stays a no-op. The returned function flushes and shuts down.
func SetupTracing(ctx context.Context, endpoint, serviceName string, log *slog.Logger) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	res := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName), semconv.ServiceInstanceID(host))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	otel.SetTracerProvider(tp)
	log.Info("tracing enabled", "endpoint", endpoint)
	return tp.Shutdown, nil
}

// PgxTracer creates a span per database query. SQL text is included because it
// contains only placeholders; argument values are never recorded.
type PgxTracer struct{ tracer trace.Tracer }

func NewPgxTracer() *PgxTracer { return &PgxTracer{tracer: otel.Tracer("service/internal/database")} }

type spanKey struct{}

func (t *PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanFromContext(ctx).SpanContext().IsValid() {
		return ctx // only trace queries that belong to a request
	}
	sql := data.SQL
	if len(sql) > 500 {
		sql = sql[:500]
	}
	ctx, span := t.tracer.Start(ctx, "db.query", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system", "postgresql"), attribute.String("db.statement", sql)))
	return context.WithValue(ctx, spanKey{}, span)
}

func (t *PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(spanKey{}).(trace.Span)
	if !ok {
		return
	}
	if data.Err != nil && data.Err != pgx.ErrNoRows {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, "query failed")
	}
	span.SetAttributes(attribute.Int64("db.rows_affected", data.CommandTag.RowsAffected()))
	span.End()
}

// NewLogger returns a JSON slog logger at the given level.
func NewLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
