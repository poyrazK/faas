//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPgRuntimeLogArchives(t *testing.T) {
	testRuntimeLogArchives(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPgRuntimeLogArchiveDirectWriteGuards(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	app, dep, i := seedRuntimeLogArchive(t, s, "eu-logs-direct")
	binding, err := s.CaptureRuntimeLogArchiveBinding(t.Context(), i.ID, i.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: "2026-10-02", StorageKey: region.RuntimeLogArchiveKey(app.ComputeRegion, i.ID, "2026-10-02")}
	if err := s.ReserveRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), "UPDATE runtime_log_archive_bindings SET compute_region='other' WHERE instance_id=$1", i.ID)
	requireRegionConstraint(t, err, "runtime_log_archive_binding_immutable")
	_, err = pool.Exec(t.Context(), "UPDATE runtime_log_archives SET storage_key=$2 WHERE instance_id=$1", i.ID, region.RuntimeLogArchiveKey("", i.ID, ref.Day))
	requireRegionConstraint(t, err, "runtime_log_archive_key_immutable")
	_, err = pool.Exec(t.Context(), "INSERT INTO runtime_log_archives(instance_id,archive_day,storage_key) VALUES($1,'2026-10-03',$2)", i.ID, region.RuntimeLogArchiveKey("other", i.ID, "2026-10-03"))
	requireRegionConstraint(t, err, "runtime_log_archive_key_identity")
	if err := s.DeleteInstance(t.Context(), i.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployments WHERE id=$1", dep.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.RuntimeLogArchiveByInstanceDay(t.Context(), i.ID, ref.Day)
	if err != nil || got.DeploymentID != dep.ID {
		t.Fatalf("deployment GC lost binding: %+v %v", got, err)
	}
}

type archivePoolDeleteFunc func(context.Context, string) error

func (f archivePoolDeleteFunc) Delete(ctx context.Context, key string) error { return f(ctx, key) }

func TestPgRuntimeLogArchiveAccountCleanupSmallPool(t *testing.T) {
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
	app, _, instance := seedRuntimeLogArchive(t, s, "eu-logs-small-pool")
	binding, err := s.CaptureRuntimeLogArchiveBinding(t.Context(), instance.ID, instance.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	key := region.RuntimeLogArchiveKey(app.ComputeRegion, instance.ID, "2026-10-02")
	if err := s.ReserveRuntimeLogArchive(t.Context(), state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: "2026-10-02", StorageKey: key}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccountDeletionPending(t.Context(), app.AccountID); err != nil {
		t.Fatal(err)
	}
	deleted := 0
	g := grace.New(grace.Params{Store: s, Now: func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }, LogArchives: archivePoolDeleteFunc(func(_ context.Context, actual string) error {
		if actual != key {
			t.Fatalf("wrong archive key: %s", actual)
		}
		deleted++
		return nil
	})})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := g.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("archives deleted=%d", deleted)
	}
	if _, err := s.AccountByID(t.Context(), app.AccountID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account cleanup exhausted pool or retained account: %v", err)
	}
}
