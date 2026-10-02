//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeLogArchiveRetention(t *testing.T) {
	testRuntimeLogArchiveRetention(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPgRuntimeLogArchiveRetentionGuardsAndSmallPool(t *testing.T) {
	migrated := pgtest.OpenMigrated(t)
	config := migrated.Config()
	config.MaxConns = 2
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := state.NewPgStore(pool)
	app, _, instance := seedRuntimeLogArchive(t, s, "eu-expiry-guard")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	binding, err := s.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, instance.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: "2026-06-02", StorageKey: region.RuntimeLogArchiveKey(app.ComputeRegion, instance.ID, "2026-06-02")}
	if err := s.ReserveRuntimeLogArchive(ctx, ref); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.AcquireRuntimeLogArchivePublicationLock(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock(context.WithoutCancel(ctx))
	if err := s.RetireRuntimeLogArchive(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishRuntimeLogArchive(ctx, instance.ID, ref.Day, 14, strings.Repeat("a", 64)); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
		t.Fatalf("expired publication deadlocked or committed: %v", err)
	}
	_, err = pool.Exec(ctx, "UPDATE runtime_log_archive_bindings SET expired_before='2026-06-01' WHERE instance_id=$1", instance.ID)
	requireRegionConstraint(t, err, "runtime_log_archive_expiry_monotonic")
	if err := s.DeleteCollectedRuntimeLogArchive(ctx, ref); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO runtime_log_archives(instance_id,archive_day,storage_key) VALUES($1,$2,$3)", instance.ID, ref.Day, ref.StorageKey)
	requireRegionConstraint(t, err, "runtime_log_archive_expired")
}

func TestPgRuntimeLogArchiveExpiryMigrationReplayAndDowngrade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	app, _, instance := seedRuntimeLogArchive(t, store, "eu-expiry-replay")
	binding, err := store.CaptureRuntimeLogArchiveBinding(t.Context(), instance.ID, instance.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: "2026-06-02", StorageKey: region.RuntimeLogArchiveKey(app.ComputeRegion, instance.ID, "2026-06-02")}
	if err := store.ReserveRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=20261002102304135"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if expired, err := store.RuntimeLogArchiveDayExpired(t.Context(), instance.ID, ref.Day); err != nil || !expired {
		t.Fatalf("replay reset expiry: %v %v", expired, err)
	}
	refs, err := store.ListAppRuntimeLogArchives(t.Context(), app.ID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("replay discarded retry reference: %+v %v", refs, err)
	}
	source, err := migrations.FS.ReadFile("20261002102304135_runtime_log_archive_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing downgrade")
	}
	_, err = pool.Exec(t.Context(), parts[1])
	if err == nil || !strings.Contains(err.Error(), "cannot discard runtime log archive expiry fences") {
		t.Fatalf("downgrade discarded expiry: %v", err)
	}
	if err := store.ReserveRuntimeLogArchive(t.Context(), ref); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
		t.Fatalf("guarded downgrade revived day: %v", err)
	}
}
