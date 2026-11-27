package dbguest_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// The fixture is a disposable primary initialized with max_connections=12
// and login role diagnostic_app. The observer connects as the superuser.
func TestDiagnosticsReportsLockWaitAndConnectionCapacity(t *testing.T) {
	dsn := os.Getenv("FAAS_POSTGRES_DIAGNOSTICS_DSN")
	if dsn == "" {
		t.Skip("FAAS_POSTGRES_DIAGNOSTICS_DSN is unset")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE TABLE diagnostic_rows (id integer PRIMARY KEY); INSERT INTO diagnostic_rows VALUES (1); GRANT SELECT, UPDATE ON diagnostic_rows TO diagnostic_app"); err != nil {
		t.Fatal(err)
	}
	limit := readActivity(t, ctx, admin).ConnectionLimit
	if err := admin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if limit < 2 || limit > 20 {
		t.Fatalf("unexpected fixture connection limit %d", limit)
	}
	login := cfg.Copy()
	login.User = "diagnostic_app"
	conns := make([]*pgx.Conn, 0, limit)
	for range limit {
		c, err := pgx.ConnectConfig(ctx, login.Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		conns = append(conns, c)
	}
	extra, err := pgx.ConnectConfig(ctx, login.Copy())
	if extra != nil {
		_ = extra.Close(ctx)
	}
	var rejected *pgconn.PgError
	if !errors.As(err, &rejected) || rejected.Code != "53300" {
		t.Fatalf("expected connection exhaustion, got %v", err)
	}

	holder, blocked := conns[0], conns[1]
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	const update = "UPDATE diagnostic_rows SET id=1 WHERE id=1"
	if _, err := tx.Exec(ctx, update); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := blocked.Exec(ctx, update)
		done <- err
	}()

	observer, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(ctx)
	pid, blocker := int32(blocked.PgConn().PID()), int32(holder.PgConn().PID())
	var waiting bool
	for !waiting {
		if err := observer.QueryRow(ctx, "SELECT $1::int = ANY(pg_blocking_pids($2))", blocker, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			time.Sleep(15 * time.Millisecond)
		}
	}
	sample := readActivity(t, ctx, observer)
	if sample.SampledAt.IsZero() || sample.Connections != limit || sample.ConnectionLimit != limit ||
		sample.LockWaits != 1 || sample.ActiveTransactions != 1 || sample.IdleInTransaction != 1 ||
		len(sample.BlockedSessions) != 1 || sample.BlockedSessions[0].PID != pid ||
		!slices.Equal(sample.BlockedSessions[0].BlockingPIDs, []int32{blocker}) {
		t.Fatalf("unexpected activity: %+v", sample)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func readActivity(t *testing.T, ctx context.Context, observer *pgx.Conn) dbguest.ActivitySample {
	t.Helper()
	results, err := observer.PgConn().Exec(ctx, dbguest.ActivitySQL).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if len(result.Rows) == 1 {
			var sample dbguest.ActivitySample
			if err := json.Unmarshal(result.Rows[0][0], &sample); err != nil {
				t.Fatal(err)
			}
			return sample
		}
	}
	t.Fatal("PostgreSQL returned no activity sample")
	return dbguest.ActivitySample{}
}
