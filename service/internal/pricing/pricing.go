// Package pricing computes request cost from versioned per-model prices.
// All amounts are integer nano-USD (1 USD = 1e9 nanos).
package pricing

import (
	"math"
	"math/bits"
	"sort"
	"time"

	"github.com/google/uuid"

	"service/internal/provider"
)

const NanosPerUSD = 1_000_000_000

// Price is one effective-dated price version. Rates are nanos per 1M tokens.
type Price struct {
	ID              uuid.UUID
	ModelID         uuid.UUID
	EffectiveFrom   time.Time
	Currency        string
	InputPerM       int64
	CachedInputPerM int64
	CacheWritePerM  int64
	OutputPerM      int64
}

// Versions is a list of prices for one model, sorted by EffectiveFrom descending.
type Versions []Price

func NewVersions(ps []Price) Versions {
	v := append(Versions(nil), ps...)
	sort.Slice(v, func(i, j int) bool { return v[i].EffectiveFrom.After(v[j].EffectiveFrom) })
	return v
}

// At returns the price effective at t, if any.
func (v Versions) At(t time.Time) (Price, bool) {
	for _, p := range v {
		if !p.EffectiveFrom.After(t) {
			return p, true
		}
	}
	return Price{}, false
}

// tokensCost returns round_half_up(tokens * perM / 1e6) without overflow; saturates at MaxInt64.
func tokensCost(tokens, perM int64) int64 {
	if tokens <= 0 || perM <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(tokens), uint64(perM))
	lo, carry := bits.Add64(lo, 500_000, 0)
	hi += carry
	if hi >= 1_000_000 {
		return math.MaxInt64
	}
	q, _ := bits.Div64(hi, lo, 1_000_000)
	if q > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(q)
}

func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Cost computes the cost of reported usage at price p.
func Cost(p Price, u provider.Usage) int64 {
	c := tokensCost(u.InputTokens, p.InputPerM)
	c = satAdd(c, tokensCost(u.CachedInputTokens, p.CachedInputPerM))
	c = satAdd(c, tokensCost(u.CacheWriteTokens, p.CacheWritePerM))
	c = satAdd(c, tokensCost(u.OutputTokens, p.OutputPerM))
	return c
}

// EstimateInputTokens is a deliberately pessimistic token estimate used for
// budget reservations: one token per 3 bytes plus per-message overhead.
func EstimateInputTokens(msgs []provider.Message) int64 {
	var n int64 = 3
	for _, m := range msgs {
		n += int64(len(m.Content)+2)/3 + 8
	}
	return n
}

// Estimate returns the worst-case cost: all input uncached at the input rate
// (or the cache-write rate if higher), and maxOutput output tokens.
func Estimate(p Price, inputTokens, maxOutput int64) int64 {
	inRate := p.InputPerM
	if p.CacheWritePerM > inRate {
		inRate = p.CacheWritePerM
	}
	return satAdd(tokensCost(inputTokens, inRate), tokensCost(maxOutput, p.OutputPerM))
}

// USDToNanos converts a dollar amount (as used in the UI/API) to nanos.
func USDToNanos(usd float64) int64 { return int64(math.Round(usd * NanosPerUSD)) }
