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
	_, err = pool.Exec(ctx, "INSERT INTO runtime_log_archive_collections(instance_id,archive_day) VALUES($1,$2)", instance.ID, ref.Day)
	requireRegionConstraint(t, err, "runtime_log_archive_collection_retired")
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
	// A failed descriptor DELETE must roll back its collection receipt.
	_, err = pool.Exec(ctx, `CREATE FUNCTION fail_archive_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected descriptor delete'; END; $$;
        CREATE TRIGGER fail_archive_delete BEFORE DELETE ON runtime_log_archives FOR EACH ROW EXECUTE FUNCTION fail_archive_delete();`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCollectedRuntimeLogArchive(ctx, ref); err == nil {
		t.Fatal("faulted collection committed")
	}
	if got, err := s.RuntimeLogArchiveDayCollected(ctx, instance.ID, ref.Day); err != nil || got {
		t.Fatalf("failed transaction granted collection: %v %v", got, err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM runtime_log_archive_collections WHERE instance_id=$1", instance.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("receipt escaped rollback: %d %v", receipts, err)
	}
	_, err = pool.Exec(ctx, `DROP TRIGGER fail_archive_delete ON runtime_log_archives; DROP FUNCTION fail_archive_delete();`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCollectedRuntimeLogArchive(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if got, err := s.RuntimeLogArchiveDayCollected(ctx, instance.ID, ref.Day); err != nil || !got {
		t.Fatalf("small-pool collection: %v %v", got, err)
	}
	_, err = pool.Exec(ctx, "UPDATE runtime_log_archive_collections SET collected_at=collected_at+interval '1 second' WHERE instance_id=$1", instance.ID)
	requireRegionConstraint(t, err, "runtime_log_archive_collection_immutable")
	_, err = pool.Exec(ctx, "INSERT INTO runtime_log_archive_collections(instance_id,archive_day) VALUES($1,'2026-05-01')", instance.ID)
	requireRegionConstraint(t, err, "runtime_log_archive_collection_retired")

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

func TestPgRuntimeLogArchiveCollectionMigrationReplayAndDowngrade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	app, _, instance := seedRuntimeLogArchive(t, store, "eu-collection-replay")
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
	if err := store.DeleteCollectedRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=20261002164048463"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeLogArchiveDayCollected(t.Context(), instance.ID, ref.Day); err != nil || !got {
		t.Fatalf("replay lost collection: %v %v", got, err)
	}
	source, err := migrations.FS.ReadFile("20261002164048463_runtime_log_archive_collection_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), parts[1]); err == nil || !strings.Contains(err.Error(), "cannot discard runtime log archive collection receipts") {
		t.Fatalf("downgrade lost receipts: %v", err)
	}
	if got, err := store.RuntimeLogArchiveDayCollected(t.Context(), instance.ID, ref.Day); err != nil || !got {
		t.Fatalf("guarded downgrade lost collection: %v %v", got, err)
	}
	// Final app/account deletion cascades receipts with the retained binding.
	if _, err := pool.Exec(t.Context(), "DELETE FROM runtime_log_archive_bindings WHERE instance_id=$1", instance.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeLogArchiveDayCollected(t.Context(), instance.ID, ref.Day); err != nil || got {
		t.Fatalf("binding cascade retained receipt: %v %v", got, err)
	}
}
