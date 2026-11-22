package breaker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func fail(t *testing.T, b *Breaker) {
	t.Helper()
	done, ok := b.Allow()
	if !ok {
		t.Fatal("expected allow")
	}
	done(Failure, "boom")
}

func TestOpensAfterThresholdAndRecovers(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	var transitions []string
	b := New(Settings{FailureThreshold: 3, OpenDuration: 10 * time.Second}, c.now, func(f, to State) {
		transitions = append(transitions, f.String()+"->"+to.String())
	})

	fail(t, b)
	fail(t, b)
	if b.Snapshot().State != Closed {
		t.Fatal("should still be closed")
	}
	// a success resets the consecutive counter
	done, _ := b.Allow()
	done(Success, "")
	fail(t, b)
	fail(t, b)
	fail(t, b)
	if b.Snapshot().State != Open {
		t.Fatal("should be open")
	}
	if _, ok := b.Allow(); ok {
		t.Fatal("open breaker must reject")
	}

	c.add(10 * time.Second)
	probe, ok := b.Allow()
	if !ok {
		t.Fatal("should allow a half-open probe")
	}
	if _, ok := b.Allow(); ok {
		t.Fatal("only one concurrent probe allowed")
	}
	probe(Success, "")
	if b.Snapshot().State != Closed {
		t.Fatal("successful probe should close")
	}
	want := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(transitions) != 3 || transitions[0] != want[0] || transitions[1] != want[1] || transitions[2] != want[2] {
		t.Fatalf("transitions %v", transitions)
	}
}

func TestFailedProbeReopens(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	b := New(Settings{FailureThreshold: 1, OpenDuration: time.Second}, c.now, nil)
	fail(t, b)
	c.add(time.Second)
	probe, ok := b.Allow()
	if !ok {
		t.Fatal("expected probe")
	}
	probe(Failure, "still down")
	if s := b.Snapshot(); s.State != Open || s.LastError != "still down" {
		t.Fatalf("snapshot %+v", s)
	}
	if _, ok := b.Allow(); ok {
		t.Fatal("re-opened breaker must reject")
	}
}

func TestNeutralProbeFreesSlotAndDoneIsIdempotent(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	b := New(Settings{FailureThreshold: 1, OpenDuration: time.Second}, c.now, nil)
	fail(t, b)
	c.add(time.Second)
	probe, _ := b.Allow()
	probe(Neutral, "")
	probe(Failure, "ignored second call")
	if b.Snapshot().State != HalfOpen {
		t.Fatalf("neutral probe should keep half-open, got %v", b.Snapshot().State)
	}
	if _, ok := b.Allow(); !ok {
		t.Fatal("slot should be free after neutral probe")
	}
}

func TestConcurrentUse(t *testing.T) {
	b := New(Settings{FailureThreshold: 50, OpenDuration: time.Hour}, nil, nil)
	var wg sync.WaitGroup
	var admitted atomic.Int64
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if done, ok := b.Allow(); ok {
				admitted.Add(1)
				done(Failure, "x")
			}
		}()
	}
	wg.Wait()
	if b.Snapshot().State != Open {
		t.Fatal("should open under concurrent failures")
	}
	if admitted.Load() < 50 {
		t.Fatalf("at least threshold calls must be admitted, got %d", admitted.Load())
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(Settings{FailureThreshold: 1}, nil, nil)
	if r.Get("a") != r.Get("a") || r.Get("a") == r.Get("b") {
		t.Fatal("registry must return stable per-key breakers")
	}
	fail(t, r.Get("a"))
	snaps := r.Snapshots()
	if snaps["a"].State != Open || snaps["b"].State != Closed {
		t.Fatalf("snapshots %+v", snaps)
	}
}
