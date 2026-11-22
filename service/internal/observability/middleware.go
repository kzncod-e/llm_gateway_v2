package observability

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	CtxRequestID = "request_id"
	CtxTeamID    = "team_id"
	HeaderReqID  = "X-Request-ID"
)

// RequestID assigns a UUIDv7 request ID. Client-supplied IDs are not trusted as
// primary keys; a valid incoming one is echoed as X-Client-Request-ID.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.Must(uuid.NewV7())
		if in := c.GetHeader(HeaderReqID); in != "" && len(in) <= 128 {
			c.Header("X-Client-Request-ID", in)
		}
		c.Set(CtxRequestID, id)
		c.Header(HeaderReqID, id.String())
		c.Next()
	}
}

func GetRequestID(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(CtxRequestID); ok {
		return v.(uuid.UUID)
	}
	return uuid.Must(uuid.NewV7())
}

var httpTracer = otel.Tracer("service/internal/http")

// Trace starts a server span per request named after the route template.
func Trace() gin.HandlerFunc {
	prop := otel.GetTextMapPropagator()
	return func(c *gin.Context) {
		ctx := prop.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		ctx, span := httpTracer.Start(ctx, c.Request.Method+" "+route, trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(semconv.HTTPRequestMethodKey.String(c.Request.Method), semconv.HTTPRoute(route)))
		defer span.End()
		if id, ok := c.Get(CtxRequestID); ok {
			span.SetAttributes(attribute.String("gateway.request_id", id.(uuid.UUID).String()))
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		status := c.Writer.Status()
		span.SetAttributes(semconv.HTTPResponseStatusCode(status))
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
}

// AccessLog writes one structured line per request. It logs no headers, bodies
// or query strings, so credentials and prompts cannot leak into logs.
func AccessLog(log *slog.Logger, m *Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		d := time.Since(start)
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		if m != nil {
			m.ObserveHTTP(c.Request.Method, route, status, d)
		}
		attrs := []any{"method", c.Request.Method, "route", route, "status", status, "duration_ms", d.Milliseconds(), "ip", c.ClientIP()}
		if id, ok := c.Get(CtxRequestID); ok {
			attrs = append(attrs, "request_id", fmt.Sprint(id))
		}
		if t, ok := c.Get(CtxTeamID); ok {
			attrs = append(attrs, "team_id", fmt.Sprint(t))
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "errors", c.Errors.String())
		}
		switch {
		case route == "/healthz" || route == "/readyz" || route == "/metrics":
			log.Debug("http request", attrs...)
		case status >= 500:
			log.Error("http request", attrs...)
		default:
			log.Info("http request", attrs...)
		}
	}
}

// Recover converts panics into a JSON 500 without leaking internals.
func Recover(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				if r == http.ErrAbortHandler {
					panic(r)
				}
				log.Error("panic", "panic", fmt.Sprint(r), "route", c.FullPath())
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "server_error", "code": "internal_error", "message": "internal server error"}})
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	}
}

// SecurityHeaders sets conservative headers on every API response.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Next()
	}
}

// BodyLimit caps request body size.
func BodyLimit(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > n {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "request_too_large",
				"message": fmt.Sprintf("request body exceeds %d bytes", n)}})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}
