package gateway

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

var ErrNoRoute = errors.New("no route for model")

const (
	StrategyPolicy   = "policy"
	StrategyExplicit = "explicit"
	StrategyModel    = "model"
)

// Route is the ordered list of targets for a request.
type Route struct {
	Strategy string
	Targets  []*ModelEntry
	Retry    RetryPolicy
}

// Resolve applies, in order: routing-policy alias, explicit provider
// ("slug/model" or providerHint), then model-name routing ordered by priority.
func (s *Snapshot) Resolve(model, providerHint string) (*Route, error) {
	if providerHint == "" {
		if p, ok := s.Policies[model]; ok && p.Enabled {
			r := &Route{Strategy: StrategyPolicy, Retry: p.Retry}
			for _, m := range p.Targets {
				if m.usable() {
					r.Targets = append(r.Targets, m)
				}
			}
			if len(r.Targets) == 0 {
				return nil, ErrNoRoute
			}
			return r, nil
		}
	}

	slug, name := providerHint, model
	if slug == "" {
		if sl, nm, ok := splitExplicit(model); ok {
			if _, exists := s.ProvidersBySlug[sl]; exists {
				slug, name = sl, nm
			}
		}
	}
	if slug != "" {
		for _, m := range s.ModelsByName[name] {
			if m.Provider.Slug == slug && m.usable() {
				return &Route{Strategy: StrategyExplicit, Targets: []*ModelEntry{m}, Retry: DefaultRetryPolicy()}, nil
			}
		}
		return nil, ErrNoRoute
	}

	r := &Route{Strategy: StrategyModel, Retry: DefaultRetryPolicy()}
	for _, m := range s.ModelsByName[model] {
		if m.usable() {
			r.Targets = append(r.Targets, m)
		}
	}
	if len(r.Targets) == 0 {
		return nil, ErrNoRoute
	}
	return r, nil
}

// RetryPolicy bounds retries per target and in total.
type RetryPolicy struct {
	MaxAttemptsPerTarget int
	Base                 time.Duration
	Max                  time.Duration
	TotalCap             int           // maximum attempts across all targets
	MaxRetryAfter        time.Duration // longer Retry-After hints move on to the next target
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttemptsPerTarget: 2, Base: 250 * time.Millisecond, Max: 2 * time.Second, TotalCap: 6, MaxRetryAfter: 5 * time.Second}
}

// Backoff returns the delay before retry number n (n >= 1) using exponential
// backoff with full jitter, raised to the provider's Retry-After hint if larger.
func (p RetryPolicy) Backoff(n int, retryAfter time.Duration, rnd func() float64) time.Duration {
	if rnd == nil {
		rnd = rand.Float64
	}
	d := p.Base
	for i := 1; i < n && d < p.Max; i++ {
		d *= 2
	}
	if d > p.Max {
		d = p.Max
	}
	d = time.Duration(rnd() * float64(d))
	if retryAfter > d {
		d = retryAfter
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
