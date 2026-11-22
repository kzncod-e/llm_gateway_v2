package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setup(t *testing.T) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb, false), mr
}

func TestRPMLimitConcurrent(t *testing.T) {
	l, _ := setup(t)
	fixed := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC) // start of a window: prev weight 1, prev empty
	l.now = func() time.Time { return fixed }

	var ok, limited atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := l.AllowRate(context.Background(), "team-a", 20)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrRateExceeded):
				limited.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 20 || limited.Load() != 30 {
		t.Fatalf("ok=%d limited=%d", ok.Load(), limited.Load())
	}
	// other teams are unaffected
	if err := l.AllowRate(context.Background(), "team-b", 20); err != nil {
		t.Fatal(err)
	}
}

func TestRPMSlidingWindow(t *testing.T) {
	l, _ := setup(t)
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return base }
	for i := 0; i < 10; i++ {
		if err := l.AllowRate(context.Background(), "t", 10); err != nil {
			t.Fatal(err)
		}
	}
	// Half way into the next minute, half of the previous window still counts (5).
	l.now = func() time.Time { return base.Add(90 * time.Second) }
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.AllowRate(context.Background(), "t", 10) == nil {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed %d, want 5", allowed)
	}
}

func TestConcurrencyLeases(t *testing.T) {
	l, mr := setup(t)
	ctx := context.Background()
	var releases []func()
	for i := 0; i < 3; i++ {
		r, err := l.AcquireConcurrency(ctx, "t", 3, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, r)
	}
	if _, err := l.AcquireConcurrency(ctx, "t", 3, time.Minute); !errors.Is(err, ErrConcurrencyExceeded) {
		t.Fatalf("expected limit, got %v", err)
	}
	releases[0]()
	if _, err := l.AcquireConcurrency(ctx, "t", 3, time.Minute); err != nil {
		t.Fatalf("release should free capacity: %v", err)
	}

	// Leases expire even if never released (crashed instance).
	start := time.Now()
	l.now = func() time.Time { return start.Add(2 * time.Minute) }
	mr.FastForward(2 * time.Minute)
	if _, err := l.AcquireConcurrency(ctx, "t", 1, time.Minute); err != nil {
		t.Fatalf("expired leases should be pruned: %v", err)
	}
}

func TestRedisUnavailable(t *testing.T) {
	l, mr := setup(t)
	mr.Close()
	_, _, err := l.Acquire(context.Background(), "t", 10, 10, time.Minute)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable (fail closed), got %v", err)
	}
	l.FailOpen = true
	rel, degraded, err := l.Acquire(context.Background(), "t", 10, 10, time.Minute)
	if err != nil || !degraded || rel == nil {
		t.Fatalf("fail-open should allow: %v %v", err, degraded)
	}
}
