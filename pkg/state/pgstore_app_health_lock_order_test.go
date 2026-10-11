package state_test

// adr: 735

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The health collector and a project apply touch the same account and app
// rows. ApplyProjectPlan locks the account row FOR UPDATE and then its apps
// FOR UPDATE. The collector used to lock the app first and reach the account
// through the app_health_* foreign keys afterwards, so `gregale apply` failed
// with "reconcile: ERROR: deadlock detected (SQLSTATE 40P01)" (PR #4376 CI,
// e2e TestApplyProject_Inputs_AuditWorkloadChanged).

func openHealthLockOrderStore(t *testing.T) (*pgxpool.Pool, *state.PgStore) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool, state.NewPgStore(pool)
}

// lockAccountForApply opens a transaction that holds the account row the way
// ApplyProjectPlan does before it locks the account's apps.
func lockAccountForApply(t *testing.T, pool *pgxpool.Pool, accountID string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(t.Context(), `select 1 from accounts where id = $1 for update`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `set local lock_timeout = '3s'`); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestClaimAppHealthSkipsAppWhoseAccountIsMidApply(t *testing.T) {
	pool, s := openHealthLockOrderStore(t)
	account, app := healthNotificationFixture(t, s)
	apply := lockAccountForApply(t, pool, account.ID)

	claimCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	if _, err := s.ClaimAppHealth(claimCtx, uuid.NewString(), now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim while the account is locked for apply = %v; want ErrNotFound without waiting", err)
	}
	// Apply continues to its app rows; nothing of the collector may hold them.
	if _, err := apply.Exec(t.Context(), `select 1 from apps where id = $1 for update`, app.ID); err != nil {
		t.Fatalf("apply could not lock its app after a collector claim: %v", err)
	}
	if err := apply.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), now)
	if err != nil || claim.AppID != app.ID {
		t.Fatalf("claim after apply = %+v, %v; want the app", claim, err)
	}
}

func TestFinishAppHealthTakesAccountBeforeAppRows(t *testing.T) {
	pool, s := openHealthLockOrderStore(t)
	account, app := healthNotificationFixture(t, s)
	now := time.Now().UTC().Truncate(time.Second)
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), now)
	if err != nil {
		t.Fatal(err)
	}
	apply := lockAccountForApply(t, pool, account.ID)

	finished := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		finished <- s.FinishAppHealth(ctx, claim, healthNotificationAssessment(app.ID, "unhealthy", now.Add(time.Millisecond)), now.Add(time.Millisecond))
	}()
	waitForLockWaiter(t, pool, finished)

	// The collector is now blocked. It must be blocked on the account, not
	// holding the app row that apply locks next.
	if _, err := apply.Exec(t.Context(), `select 1 from apps where id = $1 for update`, app.ID); err != nil {
		t.Fatalf("apply could not lock its app while the collector finished: %v", err)
	}
	if err := apply.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("FinishAppHealth after apply committed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("FinishAppHealth did not complete after apply committed")
	}
}

// waitForLockWaiter returns once another session of this database is waiting
// on a row or transaction lock.
func waitForLockWaiter(t *testing.T, pool *pgxpool.Pool, finished <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-finished:
			t.Fatalf("the collector finished without waiting on the account lock held by apply: %v", err)
		default:
		}
		var waiting int
		if err := pool.QueryRow(t.Context(), `select count(*) from pg_stat_activity
			where datname = current_database() and pid <> pg_backend_pid() and wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the collector never waited on the account lock held by apply")
}
