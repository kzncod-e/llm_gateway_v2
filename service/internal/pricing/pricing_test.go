package pricing

import (
	"math"
	"testing"
	"time"

	"service/internal/provider"
)

func TestCostCategories(t *testing.T) {
	// $2.50 / 1M input, $1.25 cached, $3.75 cache write, $10 / 1M output
	p := Price{InputPerM: 2_500_000_000, CachedInputPerM: 1_250_000_000, CacheWritePerM: 3_750_000_000, OutputPerM: 10_000_000_000}
	u := provider.Usage{InputTokens: 1000, CachedInputTokens: 2000, CacheWriteTokens: 100, OutputTokens: 500}
	// 1000*2.5e9/1e6=2.5e6, 2000*1.25e9/1e6=2.5e6, 100*3.75e9/1e6=375000, 500*1e10/1e6=5e6
	if got, want := Cost(p, u), int64(2_500_000+2_500_000+375_000+5_000_000); got != want {
		t.Fatalf("cost %d want %d", got, want)
	}
}

func TestRoundingHalfUp(t *testing.T) {
	// 1 token at $0.0375/1M = 37.5 nanos → 38
	if got := tokensCost(1, 37_500_000); got != 38 {
		t.Fatalf("got %d", got)
	}
	if got := tokensCost(3, 100_000); got != 0 { // 0.3 nanos → 0
		t.Fatalf("got %d", got)
	}
}

func TestNoOverflow(t *testing.T) {
	if got := tokensCost(math.MaxInt64, math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("expected saturation, got %d", got)
	}
	// 1e9 tokens at $150/1M would overflow a naive int64 multiply (1e9*1.5e11 = 1.5e20)
	if got := tokensCost(1_000_000_000, 150_000_000_000); got != 150_000_000_000_000 {
		t.Fatalf("got %d", got)
	}
}

func TestVersionsAt(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := NewVersions([]Price{
		{InputPerM: 1, EffectiveFrom: t0},
		{InputPerM: 2, EffectiveFrom: t0.AddDate(0, 6, 0)},
	})
	if _, ok := v.At(t0.Add(-time.Second)); ok {
		t.Fatal("no price before first version")
	}
	if p, _ := v.At(t0.AddDate(0, 1, 0)); p.InputPerM != 1 {
		t.Fatal("expected first version")
	}
	if p, _ := v.At(t0.AddDate(0, 6, 0)); p.InputPerM != 2 {
		t.Fatal("expected second version at boundary")
	}
}

func TestEstimateIsPessimistic(t *testing.T) {
	msgs := []provider.Message{{Role: "user", Content: "Explain microservices architecture"}}
	in := EstimateInputTokens(msgs)
	if in < 12 { // real tokenizers give ~4 tokens; estimate must be well above
		t.Fatalf("estimate too low: %d", in)
	}
	p := Price{InputPerM: 1_000_000_000, CacheWritePerM: 2_000_000_000, OutputPerM: 4_000_000_000}
	if got := Estimate(p, 1_000_000, 1_000_000); got != 6_000_000_000 {
		t.Fatalf("estimate %d", got)
	}
	if USDToNanos(1.5) != 1_500_000_000 {
		t.Fatal("usd conversion")
	}
}
