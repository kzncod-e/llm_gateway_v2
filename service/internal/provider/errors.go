package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// ErrorKind is the normalized classification of a provider failure.
type ErrorKind string

const (
	KindRateLimited       ErrorKind = "rate_limited"       // 429
	KindOverloaded        ErrorKind = "overloaded"         // 529 / 503 overloaded
	KindServer            ErrorKind = "server_error"       // 5xx
	KindTimeout           ErrorKind = "timeout"            // deadline exceeded, 408, 504
	KindNetwork           ErrorKind = "network_error"      // connection reset, DNS, refused
	KindAuth              ErrorKind = "auth_error"         // 401 / 403
	KindBadRequest        ErrorKind = "bad_request"        // 400 / 413 / 422
	KindNotFound          ErrorKind = "not_found"          // 404 (unknown model)
	KindCanceled          ErrorKind = "canceled"           // caller canceled (client went away)
	KindInvalidResponse   ErrorKind = "invalid_response"   // unparseable provider payload
	KindStreamInterrupted ErrorKind = "stream_interrupted" // stream ended without completion
)

// Error is the only error type adapters return.
type Error struct {
	Kind         ErrorKind
	StatusCode   int // upstream HTTP status, 0 if none
	Message      string
	ProviderCode string
	RetryAfter   time.Duration
	// Sent is false when the request certainly never reached the provider
	// (e.g. connection refused), so no usage can have been incurred.
	Sent bool
}

func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Kind, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// AsError converts any error into *Error (unknown errors become network errors).
func AsError(err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return &Error{Kind: KindNetwork, Message: err.Error(), Sent: true}
}

// Retryable reports whether retrying (possibly on another provider) can help.
func Retryable(err error) bool {
	switch AsError(err).Kind {
	case KindRateLimited, KindOverloaded, KindServer, KindTimeout, KindNetwork, KindStreamInterrupted, KindInvalidResponse:
		return true
	default:
		return false
	}
}

// Fallbackable reports whether another provider might succeed where this one failed
// without retrying the same one. Auth/not-found errors are provider-specific
// misconfigurations: switching providers can help, retrying the same cannot.
func Fallbackable(err error) bool {
	if Retryable(err) {
		return true
	}
	k := AsError(err).Kind
	return k == KindAuth || k == KindNotFound
}

// BreakerFailure reports whether the failure indicates an unhealthy provider.
func BreakerFailure(err error) bool {
	switch AsError(err).Kind {
	case KindRateLimited, KindOverloaded, KindServer, KindTimeout, KindNetwork, KindAuth, KindStreamInterrupted, KindInvalidResponse:
		return true
	default:
		return false
	}
}

// FromStatus classifies an upstream HTTP error response.
func FromStatus(status int, message, code string, header http.Header) *Error {
	e := &Error{StatusCode: status, Message: message, ProviderCode: code, Sent: true}
	switch {
	case status == http.StatusTooManyRequests:
		e.Kind = KindRateLimited
	case status == 529:
		e.Kind = KindOverloaded
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		e.Kind = KindTimeout
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Kind = KindAuth
	case status == http.StatusNotFound:
		e.Kind = KindNotFound
	case status >= 500:
		e.Kind = KindServer
	default:
		e.Kind = KindBadRequest
	}
	if header != nil {
		e.RetryAfter = parseRetryAfter(header.Get("Retry-After"))
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	return e
}

// FromTransport classifies a transport-level error, using ctx to distinguish
// caller cancellation from attempt timeouts.
func FromTransport(ctx context.Context, err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return &Error{Kind: KindTimeout, Message: "provider request timed out", Sent: true}
		}
		return &Error{Kind: KindCanceled, Message: "request canceled", Sent: true}
	}
	sent := true
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		sent = false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		sent = false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Kind: KindTimeout, Message: "provider connection timed out", Sent: sent}
	}
	return &Error{Kind: KindNetwork, Message: err.Error(), Sent: sent}
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
