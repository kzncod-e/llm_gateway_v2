// Package testutil provides an isolated Postgres schema per test for
// integration tests. Tests are skipped when TEST_DATABASE_URL is unset.
package testutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"service/internal/database"
)

// DB returns a pool bound to a fresh, migrated schema that is dropped afterwards.
func DB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("pool_max_conns", "40")
	u.RawQuery = q.Encode()
	pool, err := database.Connect(ctx, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool, Logger()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)); err != nil {
			t.Logf("drop schema: %v", err)
		}
		admin.Close()
	})
	return pool
}

// Logger discards output.
func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
