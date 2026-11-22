// Package ratelimit enforces per-team request-rate and concurrency limits in Redis.
// Both checks are single Lua scripts, so they are atomic across gateway instances.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrRateExceeded        = errors.New("rate limit exceeded")
	ErrConcurrencyExceeded = errors.New("concurrency limit exceeded")
	ErrUnavailable         = errors.New("rate limiter unavailable")
)

// LimitError carries the reason and a retry hint.
type LimitError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *LimitError) Error() string { return e.Err.Error() }
func (e *LimitError) Unwrap() error { return e.Err }

// Sliding-window counter: weighted previous window + current window.
var rpmScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local prev = tonumber(redis.call('GET', KEYS[2]) or '0')
local limit = tonumber(ARGV[1])
local weight = tonumber(ARGV[2])
if math.floor(prev * weight) + cur + 1 > limit then
  return 0
end
redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]))
return 1
`)

// Concurrency leases: sorted set of lease id -> expiry (ms). Expired leases are
// pruned before counting so a crashed instance cannot leak capacity forever.
var acquireScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[3]) then
  return 0
end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[4])
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[5]))
return 1
`)

type Limiter struct {
	rdb      redis.UniversalClient
	now      func() time.Time
	FailOpen bool
}

func New(rdb redis.UniversalClient, failOpen bool) *Limiter {
	return &Limiter{rdb: rdb, now: time.Now, FailOpen: failOpen}
}

// AllowRate consumes one unit from a per-minute sliding window identified by key.
func (l *Limiter) AllowRate(ctx context.Context, key string, perMinute int) error {
	now := l.now()
	window := now.Unix() / 60
	elapsed := float64(now.Unix()%60) + float64(now.Nanosecond())/1e9
	weight := 1 - elapsed/60
	cur := fmt.Sprintf("rl:rpm:{%s}:%d", key, window)
	prev := fmt.Sprintf("rl:rpm:{%s}:%d", key, window-1)
	res, err := rpmScript.Run(ctx, l.rdb, []string{cur, prev}, perMinute, strconv.FormatFloat(weight, 'f', 6, 64), 120).Int()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if res == 0 {
		return &LimitError{Err: ErrRateExceeded, RetryAfter: time.Duration(60-now.Unix()%60) * time.Second}
	}
	return nil
}

// AcquireConcurrency takes a lease that expires after ttl. Call release when done.
func (l *Limiter) AcquireConcurrency(ctx context.Context, key string, limit int, ttl time.Duration) (release func(), err error) {
	now := l.now()
	id := uuid.NewString()
	zkey := fmt.Sprintf("rl:conc:{%s}", key)
	res, err := acquireScript.Run(ctx, l.rdb, []string{zkey},
		now.UnixMilli(), now.Add(ttl).UnixMilli(), limit, id, (ttl + time.Minute).Milliseconds()).Int()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if res == 0 {
		return nil, &LimitError{Err: ErrConcurrencyExceeded, RetryAfter: time.Second}
	}
	return func() {
		// Release with a fresh context: the request context may be canceled.
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = l.rdb.ZRem(rctx, zkey, id).Err()
	}, nil
}

// Acquire applies the RPM limit and then the concurrency limit for a team.
// When Redis is unavailable and FailOpen is set, the request is allowed and
// degraded is true so callers can log/count it.
func (l *Limiter) Acquire(ctx context.Context, team string, rpm, concurrency int, ttl time.Duration) (release func(), degraded bool, err error) {
	noop := func() {}
	if err := l.AllowRate(ctx, team, rpm); err != nil {
		if errors.Is(err, ErrUnavailable) && l.FailOpen {
			return noop, true, nil
		}
		return nil, false, err
	}
	rel, err := l.AcquireConcurrency(ctx, team, concurrency, ttl)
	if err != nil {
		if errors.Is(err, ErrUnavailable) && l.FailOpen {
			return noop, true, nil
		}
		return nil, false, err
	}
	return rel, false, nil
}

func (l *Limiter) Ping(ctx context.Context) error { return l.rdb.Ping(ctx).Err() }
