// Package breaker implements a small, concurrency-safe circuit breaker with
// CLOSED, OPEN and HALF_OPEN states, and a registry keyed by provider.
package breaker

import (
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	HalfOpen
	Open
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case HalfOpen:
		return "half_open"
	default:
		return "open"
	}
}

type Settings struct {
	FailureThreshold int           // consecutive failures that open the circuit
	OpenDuration     time.Duration // how long to stay open before probing
	HalfOpenMax      int           // concurrent probes allowed in half-open
}

// Outcome reports the result of a call admitted by Allow.
type Outcome int

const (
	Success Outcome = iota
	Failure
	Neutral // neither healthy nor unhealthy (client error, cancellation)
)

// Snapshot is a read-only view of a breaker for APIs and metrics.
type Snapshot struct {
	State               State
	ConsecutiveFailures int
	OpenedAt            time.Time
	LastFailureAt       time.Time
	LastError           string
}

type Breaker struct {
	mu       sync.Mutex
	s        Settings
	now      func() time.Time
	onChange func(from, to State)

	state         State
	failures      int
	openedAt      time.Time
	probes        int
	lastFailureAt time.Time
	lastError     string
}

func New(s Settings, now func() time.Time, onChange func(from, to State)) *Breaker {
	if s.FailureThreshold <= 0 {
		s.FailureThreshold = 5
	}
	if s.OpenDuration <= 0 {
		s.OpenDuration = 30 * time.Second
	}
	if s.HalfOpenMax <= 0 {
		s.HalfOpenMax = 1
	}
	if now == nil {
		now = time.Now
	}
	return &Breaker{s: s, now: now, onChange: onChange}
}

// Allow reports whether a call may proceed. If it may, the returned done function
// must be called exactly once with the outcome.
func (b *Breaker) Allow() (done func(Outcome, string), ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == Open && b.now().Sub(b.openedAt) >= b.s.OpenDuration {
		b.transition(HalfOpen)
	}
	switch b.state {
	case Open:
		return nil, false
	case HalfOpen:
		if b.probes >= b.s.HalfOpenMax {
			return nil, false
		}
		b.probes++
		return b.doneFunc(true), true
	default:
		return b.doneFunc(false), true
	}
}

func (b *Breaker) doneFunc(probe bool) func(Outcome, string) {
	var once sync.Once
	return func(o Outcome, errMsg string) {
		once.Do(func() { b.record(o, probe, errMsg) })
	}
}

func (b *Breaker) record(o Outcome, probe bool, errMsg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if probe && b.state == HalfOpen {
		b.probes--
	}
	switch o {
	case Success:
		b.failures = 0
		if b.state == HalfOpen {
			b.transition(Closed)
		}
	case Failure:
		b.failures++
		b.lastFailureAt = b.now()
		b.lastError = errMsg
		switch {
		case b.state == HalfOpen:
			b.trip()
		case b.state == Closed && b.failures >= b.s.FailureThreshold:
			b.trip()
		}
	case Neutral:
		// A neutral probe frees its slot; the next caller probes again.
	}
}

func (b *Breaker) trip() {
	b.openedAt = b.now()
	b.probes = 0
	b.transition(Open)
}

func (b *Breaker) transition(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	if to == Closed {
		b.failures = 0
		b.probes = 0
	}
	if to == HalfOpen {
		b.probes = 0
	}
	if b.onChange != nil {
		b.onChange(from, to)
	}
}

func (b *Breaker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state
	if st == Open && b.now().Sub(b.openedAt) >= b.s.OpenDuration {
		st = HalfOpen // would transition on next Allow
	}
	return Snapshot{State: st, ConsecutiveFailures: b.failures, OpenedAt: b.openedAt, LastFailureAt: b.lastFailureAt, LastError: b.lastError}
}

// Reset forces the breaker closed (admin action).
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transition(Closed)
}

// Registry holds one breaker per key (provider ID).
type Registry struct {
	mu       sync.Mutex
	s        Settings
	now      func() time.Time
	onChange func(key string, from, to State)
	m        map[string]*Breaker
}

func NewRegistry(s Settings, now func() time.Time, onChange func(key string, from, to State)) *Registry {
	return &Registry{s: s, now: now, onChange: onChange, m: map[string]*Breaker{}}
}

func (r *Registry) Get(key string) *Breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.m[key]
	if !ok {
		var cb func(from, to State)
		if r.onChange != nil {
			cb = func(from, to State) { r.onChange(key, from, to) }
		}
		b = New(r.s, r.now, cb)
		r.m[key] = b
	}
	return b
}

func (r *Registry) Snapshots() map[string]Snapshot {
	r.mu.Lock()
	keys := make([]string, 0, len(r.m))
	bs := make([]*Breaker, 0, len(r.m))
	for k, b := range r.m {
		keys = append(keys, k)
		bs = append(bs, b)
	}
	r.mu.Unlock()
	out := make(map[string]Snapshot, len(keys))
	for i, k := range keys {
		out[k] = bs[i].Snapshot()
	}
	return out
}
