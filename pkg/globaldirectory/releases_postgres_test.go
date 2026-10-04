//go:build !no_pg

package globaldirectory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalReleasePostgresContract(t *testing.T) {
	pool := directoryPG(t)
	releaseContract(t, NewPgStore(pool), directoryAccount(t, pool))
}
func TestGlobalReleasePostgresRace(t *testing.T) {
	pool := directoryPG(t)
	releaseRace(t, NewPgStore(pool), directoryAccount(t, pool))
}
func TestGlobalReleasePostgresRestartAndGuards(t *testing.T) {
	pool := directoryPG(t)
	owner := directoryAccount(t, pool)
	store := NewPgStore(pool)
	app, targets := releaseSetup(t, store, owner)
	r, err := store.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage)
	if err != nil {
		t.Fatal(err)
	}
	fresh := NewPgStore(pool)
	restored, err := fresh.GetRelease(t.Context(), owner, app.Slug, r.ReleaseID)
	if err != nil || restored.Generation != 1 || len(restored.Targets) != 2 {
		t.Fatalf("restart: %+v %v", restored, err)
	}
	_, err = pool.Exec(t.Context(), "INSERT INTO global_apps(owner_id,slug,desired_release_generation) VALUES($1,'forged-head',2)", owner)
	var insertErr *pgconn.PgError
	if !errors.As(err, &insertErr) || insertErr.ConstraintName != "global_release_identity" {
		t.Fatalf("initial head: %v", err)
	}
	for _, query := range []string{
		"UPDATE global_image_releases SET image_ref=image_ref||'a' WHERE global_app_id=$1",
		"UPDATE global_image_release_targets SET api_base='https://attacker.example.com' WHERE global_app_id=$1",
		"UPDATE global_apps SET desired_release_generation=0 WHERE id=$1",
		"DELETE FROM global_image_releases WHERE global_app_id=$1",
		"DELETE FROM global_image_release_targets WHERE global_app_id=$1",
	} {
		_, err := pool.Exec(t.Context(), query, app.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "global_release_identity" {
			t.Fatalf("guard %s: %v", query, err)
		}
	}
	_, err = pool.Exec(t.Context(), "TRUNCATE global_image_releases CASCADE")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "global_release_identity" {
		t.Fatalf("truncate: %v", err)
	}
	if err := fresh.RecordReleaseResult(t.Context(), owner, app.Slug, r.ReleaseID, targets[0].Region, ReleaseResult{Outcome: ReleaseAccepted, DeploymentID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), "UPDATE global_image_release_targets SET outcome='pending',deployment_id=NULL WHERE global_app_id=$1 AND region=$2", app.ID, targets[0].Region)
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "global_release_identity" {
		t.Fatalf("terminal downgrade: %v", err)
	}
	migration, err := os.ReadFile("../../migrations/20261004014000527_global_image_release_journal.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(migration), "-- +goose Down\n")[1]
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), down)
	_ = tx.Rollback(t.Context())
	if err == nil || !strings.Contains(err.Error(), "cannot downgrade populated") {
		t.Fatalf("down: %v", err)
	}
}

func TestGlobalReleasePostgresAtomicRollback(t *testing.T) {
	pool := directoryPG(t)
	owner := directoryAccount(t, pool)
	store := NewPgStore(pool)
	app, _ := releaseSetup(t, store, owner)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_test_release() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$;
 CREATE TRIGGER test_release_failure AFTER INSERT ON global_image_release_targets FOR EACH ROW EXECUTE FUNCTION reject_test_release();`)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage); err == nil {
		t.Fatal("injected write passed")
	}
	if _, err := store.GetRelease(t.Context(), owner, app.Slug, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial release: %v", err)
	}
	_, err = pool.Exec(t.Context(), "DROP TRIGGER test_release_failure ON global_image_release_targets; DROP FUNCTION reject_test_release();")
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage)
	if err != nil || r.Generation != 1 {
		t.Fatalf("retry: %+v %v", r, err)
	}
}

func TestGlobalReleasePostgresDetachLocksAppBeforeTarget(t *testing.T) {
	pool, store, owner, app, _, binding := directoryPGTargetFixture(t)
	if _, err := store.AttachTarget(t.Context(), owner, app.Slug, binding); err != nil {
		t.Fatal(err)
	}
	tx := beginDirectoryTx(t, pool)
	if _, err := tx.Exec(t.Context(), "SELECT id FROM global_apps WHERE id=$1 FOR UPDATE", app.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- store.DetachTarget(ctx, owner, app.Slug, binding.Region) }()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%global_apps%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("detachment did not wait for app lock")
	}
	// While detachment waits, attachment/allocation can still take target locks.
	// A DELETE-first implementation already owns this row and fails NOWAIT.
	if _, err := tx.Exec(ctx, "SELECT region FROM global_app_targets WHERE global_app_id=$1 FOR UPDATE NOWAIT", app.ID); err != nil {
		t.Fatalf("detachment locked target before app: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestGlobalReleasePostgresTargetBound(t *testing.T) {
	pool := directoryPG(t)
	releaseTargetBound(t, NewPgStore(pool), directoryAccount(t, pool))
}

func TestGlobalReleasePostgresGenerationBound(t *testing.T) {
	pool := directoryPG(t)
	owner := directoryAccount(t, pool)
	store := NewPgStore(pool)
	app, _ := releaseSetup(t, store, owner)
	// Seed the otherwise impractical exhaustion boundary in this isolated test DB.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE global_apps DISABLE TRIGGER global_apps_release_head_guard;
 UPDATE global_apps SET desired_release_generation=9007199254740991;
 ALTER TABLE global_apps ENABLE TRIGGER global_apps_release_head_guard;`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_, err := store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage)
	assertReleaseLimit(t, err, "generation", api.GlobalImageReleaseGenerationMax, api.GlobalImageReleaseGenerationMax+1)
	if _, err := store.GetRelease(t.Context(), owner, app.Slug, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial allocation", err)
	}
}
