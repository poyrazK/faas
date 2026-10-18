//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgOffboardingCredentialsAndFence(t *testing.T) {
	offboardingCredentials(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgOffboardingOIDCCredential(t *testing.T) {
	offboardingOIDCCredential(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgOffboardingRejectsConcurrentKeyMint(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "UPDATE org_memberships SET removed_at = now() WHERE org_id = $1 AND account_id = $2", f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	var pid int32
	if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		hash := make([]byte, 32)
		_, err := store.CreateOrgAPIKey(t.Context(), f.org.ID, f.actor.ID, hash, "racing-mint", []string{"apps:read"}, nil)
		done <- err
	}()
	waitForOffboardingBlock(t, pool, pid)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a concurrent mint survived completed removal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential mint did not finish after removal")
	}
	if keys, err := store.ListOrgAPIKeys(t.Context(), f.org.ID); err != nil || len(keys) != 0 {
		t.Fatalf("post-removal active credentials: %+v %v", keys, err)
	}
}

func TestPostgresOrgOffboardingRollbackAndRejoin(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	key := offboardingKey(t, store, f.org.ID, f.actor.ID, "rollback-key")
	before, err := store.OrgAccessSnapshot(t.Context(), f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "UPDATE org_memberships SET removed_at = now() WHERE org_id = $1 AND account_id = $2", f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := tx.QueryRow(t.Context(), "SELECT status FROM api_keys WHERE id = $1", key.ID).Scan(&status); err != nil || status != "revoked" {
		t.Fatalf("credentials not revoked within removal transaction: %s %v", status, err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AuthenticateKey(t.Context(), key.Hash); err != nil {
		t.Fatalf("rollback revoked credential: %v", err)
	}
	if after, err := store.OrgAccessSnapshot(t.Context(), f.org.ID, f.actor.ID); err != nil || after != before {
		t.Fatalf("rollback changed authorization: %+v %v", after, err)
	}
	if err := store.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	// Rejoining must never revive a request or credential from the previous
	// membership, including a request paused before removal.
	if _, err := pool.Exec(t.Context(), "UPDATE org_memberships SET removed_at = NULL WHERE org_id = $1 AND account_id = $2", f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := state.WithOrgAccessFence(t.Context(), state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: before.Revision})
	defer cancel(nil)
	if err := store.UpsertAppEnvInScope(ctx, f.actor.ID, f.app.ID, "default", "STALE", "forbidden"); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("rejoined stale request: %v", err)
	}
	if _, _, err := store.AuthenticateKey(t.Context(), key.Hash); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("rejoin revived credential: %v", err)
	}
}

func TestPostgresOrgOffboardingOrdersNotificationAndRemoval(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	snapshot, err := store.OrgAccessSnapshot(t.Context(), f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := state.WithOrgAccessFence(t.Context(), state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
	defer cancel(nil)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err := db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyAppWake, `{"app_id":"`+f.app.ID+`"}`); err != nil {
		t.Fatal(err)
	}
	var pid int32
	if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	removed := make(chan error, 1)
	go func() { removed <- state.NewPgStore(pool).RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID) }()
	waitForOffboardingBlock(t, pool, pid)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-removed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("removal did not finish after the earlier intent committed")
	}
	if err := db.Notify(ctx, pool, db.NotifyAppWake, `{"app_id":"`+f.app.ID+`"}`); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("post-removal wake notification: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM notification_outbox WHERE channel = $1", db.NotifyAppWake).Scan(&count); err != nil || count != 1 {
		t.Fatalf("notification persisted after removal: count=%d %v", count, err)
	}
}

func waitForOffboardingBlock(t *testing.T, pool *pgxpool.Pool, blocker int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))", blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("removal did not contend with the intent's membership lock")
		case <-ticker.C:
		}
	}
}
