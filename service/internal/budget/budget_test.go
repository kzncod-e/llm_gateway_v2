package budget_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"service/internal/budget"
	"service/internal/database"
	"service/internal/model"
	"service/internal/repository"
	"service/internal/testutil"
)

func setupTeam(t *testing.T, limit int64) (*repository.Repo, *budget.Service, uuid.UUID) {
	pool := testutil.DB(t)
	repo := repository.New(pool)
	team := &model.Team{ID: model.NewID(), Name: "T", Slug: "t-" + uuid.NewString()[:8]}
	if err := repo.CreateTeam(context.Background(), team); err != nil {
		t.Fatal(err)
	}
	if limit >= 0 {
		if err := repo.UpsertTeamBudget(context.Background(), &model.TeamBudget{TeamID: team.ID, MonthlyLimit: &limit, WarnPct: 80}); err != nil {
			t.Fatal(err)
		}
	}
	return repo, budget.New(pool, time.Minute), team.ID
}

// The core budget safety property: 100 concurrent requests each reserving 1/10
// of the budget; exactly 10 may pass.
func TestConcurrentReservationsNeverOverspend(t *testing.T) {
	_, svc, team := setupTeam(t, 1000)
	var ok, exceeded atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, has, err := svc.Reserve(context.Background(), team, uuid.New(), 100)
			switch {
			case err == nil && has:
				ok.Add(1)
			case errors.Is(err, budget.ErrExceeded):
				exceeded.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 || exceeded.Load() != 90 {
		t.Fatalf("ok=%d exceeded=%d, want 10/90", ok.Load(), exceeded.Load())
	}
	st, err := svc.Status(context.Background(), team)
	if err != nil {
		t.Fatal(err)
	}
	if st.MonthReserved != 1000 || st.MonthSpent != 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestSettleReleaseAndSweep(t *testing.T) {
	repo, svc, team := setupTeam(t, 1000)
	ctx := context.Background()
	pool := repo.Pool()

	res, _, err := svc.Reserve(ctx, team, uuid.New(), 400)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := pool.Begin(ctx)
	if err := budget.Settle(ctx, tx, team, res, 150, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.Status(ctx, team)
	if st.MonthSpent != 150 || st.MonthReserved != 0 || st.DaySpent != 150 || *st.MonthRemaining != 850 {
		t.Fatalf("after settle %+v", st)
	}

	// Settling twice must not double-release the reservation.
	tx, _ = pool.Begin(ctx)
	if err := budget.Settle(ctx, tx, team, res, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	if st, _ = svc.Status(ctx, team); st.MonthReserved != 0 || st.MonthSpent != 150 {
		t.Fatalf("double settle changed counters %+v", st)
	}

	r2, _, _ := svc.Reserve(ctx, team, uuid.New(), 300)
	if err := svc.Release(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if st, _ = svc.Status(ctx, team); st.MonthReserved != 0 {
		t.Fatalf("release failed %+v", st)
	}

	// An abandoned reservation is swept after expiry.
	if _, _, err := svc.Reserve(ctx, team, uuid.New(), 500); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE budget_reservations SET expires_at = now() - interval '1 second' WHERE status = 'active'`); err != nil {
		t.Fatal(err)
	}
	n, err := svc.SweepExpired(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep n=%d err=%v", n, err)
	}
	if st, _ = svc.Status(ctx, team); st.MonthReserved != 0 {
		t.Fatalf("sweep did not release %+v", st)
	}

	// Spend near the limit blocks further reservations.
	if _, _, err := svc.Reserve(ctx, team, uuid.New(), 851); !errors.Is(err, budget.ErrExceeded) {
		t.Fatalf("expected exceeded, got %v", err)
	}
}

func TestNoBudgetMeansNoReservation(t *testing.T) {
	_, svc, team := setupTeam(t, -1)
	res, has, err := svc.Reserve(context.Background(), team, uuid.New(), 1_000_000)
	if err != nil || has || res != nil {
		t.Fatalf("res=%v has=%v err=%v", res, has, err)
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	pool := testutil.DB(t)
	if err := database.Migrate(context.Background(), pool, testutil.Logger()); err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
}
