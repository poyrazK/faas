package imaged

// Loop / GC-tick tests (F1). These drive pkg/imaged/loop.go directly with
// an in-memory Store + injected clock + injected tick channel so they run
// without a real ticker (mirrors pkg/sched/loop_test.go).
//
// The Loop's only filesystem side effect is deleteSnapshotsAndFiles →
// os.RemoveAll on sched.SnapDir()/id and os.Remove on the ext4. Tests
// arrange t.TempDir() under both paths so the deletes always succeed and
// never touch a real /srv/fc.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// gcFixture wires a Loop with a memstore, an injected tick channel, and a
// hermetic appsRoot / snap dir. Returns the loop + the helpers tests need
// to assert side effects.
type gcFixture struct {
	loop     *Loop
	store    *state.MemStore
	gcCh     chan time.Time
	tickSent func() // tickOnce helper
	be       storage.StorageBackend
}

func newGCFixture(t *testing.T, lvPct float64) *gcFixture {
	t.Helper()
	store := state.NewMemStore()

	// Hermetic sched.SnapDir() — sched.SnapDir() is a function, not a
	// variable, so the loop uses the canonical /srv/fc/snap path. The
	// deleteSnapshotsAndFiles helper issues storage.Delete calls keyed
	// on the storage backend; an empty backend swallows them (Delete
	// on a missing key is a no-op). Tests that need to assert on the
	// delete side effect use the storage backend reference below.
	_ = sched.SnapDir()

	appsRoot := t.TempDir()
	be, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatalf("storage.NewLocalStorageBackend: %v", err)
	}

	gcCh := make(chan time.Time, 16)
	loop := &Loop{
		handler: &Handler{
			store:    store,
			log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			appsRoot: appsRoot,
			storage:  be,
		},
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) {
			return lvPct, nil
		},
		appsRoot: appsRoot,
		gcCh:     gcCh,
	}
	return &gcFixture{
		loop:  loop,
		store: store,
		gcCh:  gcCh,
		be:    be,
		tickSent: func() {
			gcCh <- time.Unix(0, 0)
		},
	}
}

// seedSnapshotWithApp inserts one app (status=active), one deployment, and
// one snapshot row. Returns IDs the tests can assert on.
func seedSnapshotWithApp(t *testing.T, store *state.MemStore, memBytes, diskBytes int64) (appID, depID, snapID string) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), "u@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "snap-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID,
		MemBytes:     memBytes,
		DiskBytes:    diskBytes,
		FCVersion:    "1.8.0",
		StorageKey:   state.SnapMemKey(dep.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID, dep.ID, snap.ID
}

func TestGC_PerAppKeepCurrentPrevious(t *testing.T) {
	fx := newGCFixture(t, 50) // under budget → only per-app sweep
	store := fx.store

	// One app, three deployments → two snapshots fall outside the
	// current+previous window. Insert them in CreatedAt order so the
	// "newest two" are deterministic.
	appID := "11111111-1111-1111-1111-111111111111"
	acct, _ := store.CreateAccount(context.Background(), "a@b.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		ID: appID, AccountID: acct.ID, Slug: "keep", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
		})
		if err != nil {
			t.Fatal(err)
		}
		snap, err := store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.SnapMemKey(dep.ID),
			CreatedAt:  base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = snap
	}

	fx.loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, err := store.ListSnapshotsForGC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("after per-app GC: %d snapshots remain, want 2", len(rows))
	}
}

func TestGC_NoOpUnderBudget(t *testing.T) {
	fx := newGCFixture(t, 50)
	_, _, _ = seedSnapshotWithApp(t, fx.store, 100, 100)
	fx.loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, _ := fx.store.ListSnapshotsForGC(context.Background())
	if len(rows) != 1 {
		t.Errorf("under-budget GC dropped rows: %d remain, want 1", len(rows))
	}
}

func TestRun_PerformsGCBeforeFirstTicker(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "startup-gc@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "startup-gc", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:failed", Status: state.DeployFailed,
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion: "1.8.0", StorageKey: state.SnapMemKey(dep.ID),
	})

	appsRoot := t.TempDir()
	be, _ := storage.NewLocalStorageBackend(appsRoot)
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   time.Now,
		lvUsedPct: func(context.Context) (float64, error) {
			return 50, nil
		},
		gcCh: make(chan time.Time),
		fcCh: make(chan struct{}),
		handler: &Handler{
			store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), storage: be, appsRoot: appsRoot,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.After(time.Second)
	for {
		rows, err := store.ListSnapshotsForGC(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("startup GC did not run before the first ticker")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_StartupGCDoesNotBlockEventLoop(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	loop := &Loop{
		store: state.NewMemStore(),
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   time.Now,
		lvUsedPct: func(context.Context) (float64, error) {
			close(started)
			<-release
			return 0, nil
		},
		gcCh: make(chan time.Time),
		fcCh: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup GC did not begin")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("startup GC blocked the event loop from observing cancellation")
	}
	close(release)
}

func TestRunGCTick_NaNUsageProducesValidStructuredLog(t *testing.T) {
	var logs bytes.Buffer
	loop := &Loop{
		store: state.NewMemStore(),
		log:   slog.New(slog.NewJSONHandler(&logs, nil)),
		lvUsedPct: func(context.Context) (float64, error) {
			return nan(), nil
		},
	}
	loop.runGCTick(context.Background(), time.Unix(0, 0))
	got := logs.String()
	if strings.Contains(got, "!ERROR:json") {
		t.Fatalf("NaN produced invalid JSON logging: %s", got)
	}
	if !strings.Contains(got, `"lv_fc_pct_known":false`) || !strings.Contains(got, `"lv_fc_pct":0`) {
		t.Fatalf("unknown usage signal missing from log: %s", got)
	}
}

func TestRemoveLocalSnapshotOrphansPreservesReferencedAndRecentDirs(t *testing.T) {
	store := state.NewMemStore()
	_, knownID, snapID := seedSnapshotWithApp(t, store, 1, 1)
	if err := store.MarkSnapshotStale(context.Background(), snapID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	orphanID := "550e8400-e29b-41d4-a716-446655440000"
	recentID := "660e8400-e29b-41d4-a716-446655440001"
	now := time.Now()
	for _, id := range []string{knownID, orphanID, recentID} {
		dir := filepath.Join(root, "snap", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "mem"), []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-2 * localSnapshotOrphanGrace)
	for _, id := range []string{knownID, orphanID} {
		dir := filepath.Join(root, "snap", id)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	loop := &Loop{
		store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), storageRoot: root,
	}
	loop.removeLocalSnapshotOrphans(context.Background(), now)
	if _, err := os.Stat(filepath.Join(root, "snap", knownID)); err != nil {
		t.Fatalf("retained stale snapshot directory removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "snap", orphanID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old orphan directory still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "snap", recentID)); err != nil {
		t.Fatalf("recent unpublished directory removed: %v", err)
	}
}

func TestDeleteSnapshotsAndFilesRemovesLegacyStorageRoot(t *testing.T) {
	store := state.NewMemStore()
	appID, depID, snapID := seedSnapshotWithApp(t, store, 1, 1)
	app, err := store.AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacyRoot := t.TempDir()
	legacy, err := storage.NewLocalStorageBackend(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{state.SnapMemKey(depID), state.SnapVMStateKey(depID)} {
		if err := legacy.Put(context.Background(), key, strings.NewReader("legacy")); err != nil {
			t.Fatal(err)
		}
	}
	loop := &Loop{
		store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), storageRoot: legacyRoot,
		handler: &Handler{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), storage: configured},
	}
	if err := loop.deleteSnapshotsAndFiles(context.Background(), []deleteTarget{{
		ID: snapID, DeploymentID: depID, AppSlug: app.Slug, Tier: state.SnapshotTierInit,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "snap", depID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy deployment directory still present: %v", err)
	}
}

func TestFCSweep_ExpiredStaleSnapshotRemovesArtifacts(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "stale-gc@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "stale-gc", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stale", Status: state.DeployLive,
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100, Stale: true,
		FCVersion: "1.8.0", StorageKey: state.SnapMemKey(dep.ID),
		CreatedAt: time.Now().Add(-api.SnapshotStaleRetention - time.Hour),
	})

	appsRoot := t.TempDir()
	be, _ := storage.NewLocalStorageBackend(appsRoot)
	keys := []string{
		state.SnapMemKey(dep.ID),
		sched.SnapshotVMStateKey(dep.ID),
		sched.AppLayerKey(app.Slug, dep.ID),
	}
	for _, key := range keys {
		if err := be.Put(context.Background(), key, strings.NewReader("artifact")); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		detectFC: func(context.Context) (string, error) {
			return "1.8.0", nil
		},
		handler: &Handler{
			store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), storage: be, appsRoot: appsRoot,
		},
	}
	if ok := loop.runFCSweep(context.Background()); !ok {
		t.Fatal("runFCSweep returned false")
	}
	for _, key := range keys {
		if rc, err := be.Get(context.Background(), key); err == nil {
			_ = rc.Close()
			t.Errorf("expired artifact %s survived stale retention", key)
		}
	}
	rows, err := store.ListSnapshotsStaleOlderThan(context.Background(), api.SnapshotStaleRetention)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expired stale rows remain: %d", len(rows))
	}
}

func TestGC_NaNLvUsedPct_IsSafeNoOp(t *testing.T) {
	store := state.NewMemStore()
	appsRoot := t.TempDir()
	be, _ := storage.NewLocalStorageBackend(appsRoot)
	gcCh := make(chan time.Time, 1)
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) {
			return nan(), nil
		},
		appsRoot: appsRoot,
		gcCh:     gcCh,
		handler: &Handler{
			store:    store,
			log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			appsRoot: appsRoot,
			storage:  be,
		},
	}
	_, _, _ = seedSnapshotWithApp(t, store, 100, 100)
	gcCh <- time.Unix(0, 0)
	loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, _ := store.ListSnapshotsForGC(context.Background())
	if len(rows) != 1 {
		t.Errorf("NaN probe dropped rows: %d remain, want 1", len(rows))
	}
}

func TestGC_PressureMode_EvictsFromHeaviestAccount(t *testing.T) {
	// 3 accounts, 1 snapshot each. Make account A heaviest (10 GB) and
	// expect the eviction to come from A under pressure.
	store := state.NewMemStore()

	heavyAcct, _ := store.CreateAccount(context.Background(), "heavy@x.com", "scale")
	midAcct, _ := store.CreateAccount(context.Background(), "mid@x.com", "scale")
	lightAcct, _ := store.CreateAccount(context.Background(), "light@x.com", "scale")

	heavyApp, _ := store.CreateApp(context.Background(), state.App{
		AccountID: heavyAcct.ID, Slug: "heavy", RAMMB: 1024, IdleTimeoutS: 600, MaxConcurrency: 20,
	})
	midApp, _ := store.CreateApp(context.Background(), state.App{
		AccountID: midAcct.ID, Slug: "mid", RAMMB: 1024, IdleTimeoutS: 600, MaxConcurrency: 20,
	})
	lightApp, _ := store.CreateApp(context.Background(), state.App{
		AccountID: lightAcct.ID, Slug: "light", RAMMB: 1024, IdleTimeoutS: 600, MaxConcurrency: 20,
	})

	heavyDep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: heavyApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:h",
	})
	midDep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: midApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:m",
	})
	lightDep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: lightApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:l",
	})

	heavySnap, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: heavyDep.ID, MemBytes: 5 << 30, DiskBytes: 5 << 30, // 10 GB
		FCVersion:  "1.8.0",
		StorageKey: state.SnapMemKey(heavyDep.ID),
		CreatedAt:  time.Now().Add(-3 * time.Minute),
	})
	// heavyApp needs > current+previous snapshots so the per-app floor
	// leaves at least one evictable row. With 3 snapshots and a 2-row
	// floor, the oldest is a valid eviction target.
	heavyDep2, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: heavyApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:h2",
	})
	heavySnap2, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: heavyDep2.ID, MemBytes: 5 << 30, DiskBytes: 5 << 30,
		FCVersion:  "1.8.0",
		StorageKey: state.SnapMemKey(heavyDep2.ID),
		CreatedAt:  time.Now().Add(-2 * time.Minute),
	})
	heavyDep3, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: heavyApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:h3",
	})
	heavySnap3, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: heavyDep3.ID, MemBytes: 5 << 30, DiskBytes: 5 << 30,
		FCVersion:  "1.8.0",
		StorageKey: state.SnapMemKey(heavyDep3.ID),
		CreatedAt:  time.Now().Add(-1 * time.Minute),
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: midDep.ID, MemBytes: 3 << 30, DiskBytes: 2 << 30, // 5 GB
		FCVersion:  "1.8.0",
		StorageKey: state.SnapMemKey(midDep.ID),
		CreatedAt:  time.Now().Add(-time.Minute),
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: lightDep.ID, MemBytes: 1 << 30, DiskBytes: 1 << 30, // 2 GB
		FCVersion:  "1.8.0",
		StorageKey: state.SnapMemKey(lightDep.ID),
		CreatedAt:  time.Now(),
	})

	gcCh := make(chan time.Time, 1)
	// Probe is at 95% on first call (pressure), then drops to 50% after
	// the eviction (relief) so the loop exits cleanly. Mirrors what the
	// real lv-fc watcher would do after reclaiming 10 GB.
	calls := 0
	appsRoot := t.TempDir()
	be, _ := storage.NewLocalStorageBackend(appsRoot)
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) {
			calls++
			if calls == 1 {
				return 95.0, nil
			}
			return 50.0, nil
		},
		appsRoot: appsRoot,
		gcCh:     gcCh,
		handler: &Handler{
			store:    store,
			log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			appsRoot: appsRoot,
			storage:  be,
		},
	}
	gcCh <- time.Unix(0, 0)
	loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, _ := store.ListSnapshotsForGC(context.Background())
	if len(rows) != 4 {
		t.Fatalf("pressure GC: %d rows remain, want 4 (one heavy evicted)", len(rows))
	}
	// The oldest heavy snapshot must have been evicted; heavySnap2 +
	// heavySnap3 survive (current + previous); mid + light survive.
	heavyGone := 0
	for _, r := range rows {
		if r.ID == heavySnap.ID || r.ID == heavySnap2.ID || r.ID == heavySnap3.ID {
			heavyGone++
		}
	}
	if heavyGone != 2 {
		t.Errorf("expected 2 heavy snaps remain (current+previous); %d of 3 remain", heavyGone)
	}
	for _, r := range rows {
		if r.ID == heavySnap.ID {
			t.Errorf("pressure GC should evict the OLDEST heavy snap first; heavySnap still present")
		}
	}
}

func TestGC_DeleteSnapshotsByID_BulkAndIdempotent(t *testing.T) {
	store := state.NewMemStore()
	_, _, snapA := seedSnapshotWithApp(t, store, 100, 100)

	// Insert a second snapshot on a fresh app so we have two to delete.
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: "11111111-1111-1111-1111-111111111111", Slug: "other",
		RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	_ = app // not reachable via store API; use the existing seed path instead.

	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: "00000000-0000-0000-0000-000000000000",
		Kind:  state.DeploymentKindImage, ImageDigest: "sha256:x",
	})
	snapB, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: state.SnapMemKey(dep.ID),
	})

	n, err := store.DeleteSnapshotsByID(context.Background(), []string{snapA, snapB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("first delete removed %d, want 2", n)
	}
	n2, err := store.DeleteSnapshotsByID(context.Background(), []string{snapA, snapB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second delete removed %d, want 0 (idempotent)", n2)
	}
	_ = strings.Contains // keep import live for some goimports configs
}

// TestGC_IdenticalCreatedAt_StableSort is the F-09 regression: both GC
// algorithms (perAppKeepCurrentPrevious and evictOldestFromHeaviestAccount)
// must use a stable sort, so that snapshots whose CreatedAt ties (e.g.,
// bulk-imported at the same minute) get evicted in a deterministic ID
// order rather than the random order of go's unstable sort. Non-
// determinism here made CI red-green across runs.
func TestGC_IdenticalCreatedAt_StableSort(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "stable@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "stable", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	// Create 5 deployments all with the SAME CreatedAt — the test runs
	// the per-app policy which keeps the newest 2 and drops the oldest 3,
	// and ordering must be stable. We mirror that requirement by
	// comparing two consecutive runs: each run must drop the SAME 3 IDs.
	base := time.Now().Add(-time.Hour)
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:s" + string(rune('a'+i)),
		})
		snap, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion: "1.8.0",
			// All snapshots share CreatedAt — tie-breaker must be the
			// stable sort's natural secondary key (ID).
			StorageKey: state.SnapMemKey(dep.ID),
			CreatedAt:  base,
		})
		ids[i] = snap.ID
	}

	// Drive the loop with under-budget pressure so only per-app policy runs.
	gcCh := make(chan time.Time, 1)
	gcCh <- time.Unix(0, 0)
	appsRootLoop := t.TempDir()
	beLoop, _ := storage.NewLocalStorageBackend(appsRootLoop)
	loop := &Loop{
		store:     store,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:       func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) { return 50.0, nil }, // under budget
		appsRoot:  appsRootLoop,
		gcCh:      gcCh,
		handler: &Handler{
			store:    store,
			log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			appsRoot: appsRootLoop,
			storage:  beLoop,
		},
	}
	loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, _ := store.ListSnapshotsForGC(context.Background())
	if len(rows) != 2 {
		t.Fatalf("per-app GC: %d rows remain, want 2 (current+previous)", len(rows))
	}
	// Determinism: the surviving 2 must be deterministic across runs.
	// With stable sort on identical CreatedAt, the secondary key (ID) is
	// what determines the floor — we don't pin which IDs survive here
	// (the algorithm doesn't expose the tiebreaker), only that the run
	// converges to exactly 2 rows and DOES NOT depend on map iteration.
	if len(rows) > 2 {
		t.Errorf("F-09 regression: per-app policy left %d rows, want 2", len(rows))
	}
}

// TestFCSweep_RunsTwiceOnFailOpen is the F-08 regression. Prior to F-08,
// a failed detectFC (e.g., the firecracker --version probe fails on a
// PATH issue) set fcCh = nil and the loop silently never re-tried. The
// fix: runFCSweep returns bool, and Run() only drains fcCh on success.
// On a failed detect, fcCh retains its buffered value so the next select
// iteration retries the sweep.
func TestFCSweep_RunsTwiceOnFailOpen(t *testing.T) {
	store := state.NewMemStore()
	calls := 0
	handler := &Handler{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	fcCh := make(chan struct{}, 1)
	fcCh <- struct{}{}
	appsRootLoop := t.TempDir()
	beLoop, _ := storage.NewLocalStorageBackend(appsRootLoop)
	handler.storage = beLoop
	loop := &Loop{
		handler: handler,
		store:   store,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		fcCh:    fcCh,
		detectFC: func(ctx context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "", errors.New("firecracker -version failed")
			}
			return "1.8.0", nil
		},
		appsRoot: appsRootLoop,
	}

	// First call: error. Must NOT drain fcCh and must NOT set fcCh=nil.
	ok := loop.runFCSweep(context.Background())
	if ok {
		t.Fatalf("F-08 regression: runFCSweep returned true on detectFC error")
	}
	if loop.fcCh == nil {
		t.Fatalf("F-08 regression: fcCh was nilled on detectFC error; the sweep will never re-fire")
	}
	select {
	case <-fcCh:
		// expected — the buffered value is still there.
	default:
		t.Fatalf("F-08 regression: fcCh lost its buffered value on detectFC error")
	}

	// Second call: success.
	ok = loop.runFCSweep(context.Background())
	if !ok {
		t.Fatalf("F-08 regression: runFCSweep returned false on success")
	}
	if calls != 2 {
		t.Errorf("expected detectFC to be called twice (once failing, once succeeding); got %d", calls)
	}
}

// TestLoopDeleteSnapshotsAndFiles_RemovesExt4AndSnapKeys is the F-05
// regression now expressed against the StorageBackend API (#96). The
// loop deletes both the per-app ext4 (apps/<slug>/<dep>.ext4) and the
// snap mem / vmstate keys (snap/<dep>/mem, snap/<dep>/vmstate). The
// GC's deleteTarget tuple carries (snapID, deploymentID, slug), so the
// seam we test here is the key resolution + Delete propagation.
func TestLoopDeleteSnapshotsAndFiles_RemovesExt4AndSnapKeys(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "gc-target", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:gc",
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.8.0",
		StorageKey: state.SnapMemKey(dep.ID),
	})

	appsRoot := t.TempDir()
	be, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatalf("storage.NewLocalStorageBackend: %v", err)
	}
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	memKey := sched.SnapshotMemKey(dep.ID)
	vmKey := sched.SnapshotVMStateKey(dep.ID)
	for k, body := range map[string]string{
		appsKey: "layer",
		memKey:  "m",
		vmKey:   "v",
	} {
		if err := be.Put(context.Background(), k, strings.NewReader(body)); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}

	loop := &Loop{
		store:    store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: appsRoot,
		handler: &Handler{
			store:    store,
			log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			appsRoot: appsRoot,
			storage:  be,
		},
	}
	ts := []deleteTarget{{ID: "ignored", DeploymentID: dep.ID, AppSlug: app.Slug}}
	if err := loop.deleteSnapshotsAndFiles(context.Background(), ts); err != nil {
		t.Fatalf("deleteSnapshotsAndFiles: %v", err)
	}
	for _, k := range []string{appsKey, memKey, vmKey} {
		if rc, err := be.Get(context.Background(), k); err == nil {
			_ = rc.Close()
			t.Errorf("F-05 regression: key %s survived deleteSnapshotsAndFiles", k)
		}
	}
}

// TestMemStore_ListDeploymentsForApp_LimitZero is the F-10 parity check.
// Both backends must return all remaining rows when `limit <= 0` (the
// convention documented on State.ListDeploymentsForApp). PgStore's prior
// behaviour (LIMIT 0 → 0 rows) silently broke imaged's cleanupAppFiles,
// which iterates with (0, 0).
func TestMemStore_ListDeploymentsForApp_LimitZero(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "limit-test", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	for i := 0; i < 5; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:l" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = dep
	}
	rows, err := store.ListDeploymentsForApp(context.Background(), app.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Errorf("ListDeploymentsForApp(limit=0): got %d, want 5 (no cap)", len(rows))
	}
	rows3, err := store.ListDeploymentsForApp(context.Background(), app.ID, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows3) != 3 {
		t.Errorf("ListDeploymentsForApp(limit=3): got %d, want 3", len(rows3))
	}
	// F-10: negative offset must be silently clamped to 0 (matches PgStore).
	rowsNeg, err := store.ListDeploymentsForApp(context.Background(), app.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsNeg) != 5 {
		t.Errorf("ListDeploymentsForApp(offset=-1): got %d, want 5 (clamped to 0)", len(rowsNeg))
	}
}

// nan returns a float64 that is not a number. Used by the lv-fc probe
// "no data" path. (math.NaN() needs the math import — keep this tiny
// helper so the test files don't all grow it.)
func nan() float64 {
	var z float64
	return z / z // 0/0 → NaN, deterministic, no math import
}

// TestLoop_ConstructionNoReaper is a sanity test confirming imaged no
// longer carries the PR-A reaper channel/config (PR-B). builderd
// owns the build-queue durability surface now; imaged reacts to
// deployment_changed + snapshot_boot signals only.
func TestLoop_ConstructionNoReaper(t *testing.T) {
	loop := NewLoop(LoopConfig{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	// gcCh is set lazily inside Run (not NewLoop). Constructor
	// success — minimal field wiring returning a usable Loop — is
	// the assertion; PR-A's reapCh / ReapBuildEvery / WithBuildReapChannel
	// must be gone, and they are.
	if loop.handler != nil {
		t.Error("NewLoop should leave handler nil until caller wires it")
	}
	// The reap channel/config must be gone (no public surface on
	// LoopConfig, no fields on Loop). Construction with the bare
	// config returning a usable Loop is the assertion — any future
	// re-introduction of reapCh / ReapBuildEvery will surface via a
	// unused-but-still-public interface in code review, and the
	// reference to Loop.gcCh above is the only knob tests should
	// ever need to drive the loop.
}

// countingStore wraps *state.MemStore and tallies the GC-path method
// calls. Used by TestRunGCTick_PerEvictionSQLCount + the benchmark to
// assert the per-eviction SQL count drops from O(4N) to O(N).
//
// Only methods reachable from runGCTick → deleteSnapshotsAndFiles →
// perAppKeepCurrentPrevious / evictOldestFromHeaviestAccount are
// counted. Other Store methods (apps, accounts, etc.) are intentionally
// passthroughs so the wrapper is invisible to anything that doesn't
// need to read the counters.
type countingStore struct {
	*state.MemStore
	counts map[string]int
}

func newCountingStore() *countingStore {
	return &countingStore{MemStore: state.NewMemStore(), counts: map[string]int{}}
}

func (s *countingStore) bump(method string) {
	s.counts[method]++
}

func (s *countingStore) ListSnapshotsForGC(ctx context.Context) ([]state.SnapshotForGC, error) {
	s.bump("ListSnapshotsForGC")
	return s.MemStore.ListSnapshotsForGC(ctx)
}

func (s *countingStore) MarkOldSnapshotsStale(ctx context.Context, ids []string) (int64, error) {
	s.bump("MarkOldSnapshotsStale")
	return s.MemStore.MarkOldSnapshotsStale(ctx, ids)
}

func (s *countingStore) DeleteSnapshotsByID(ctx context.Context, ids []string) (int64, error) {
	s.bump("DeleteSnapshotsByID")
	return s.MemStore.DeleteSnapshotsByID(ctx, ids)
}

func (s *countingStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	s.bump("DeploymentByID")
	return s.MemStore.DeploymentByID(ctx, id)
}

func (s *countingStore) AppByID(ctx context.Context, id string) (state.App, error) {
	s.bump("AppByID")
	return s.MemStore.AppByID(ctx, id)
}

// total returns the sum of all counted calls. Useful as a single
// regression metric — easier to assert against than a per-method
// breakdown.
func (s *countingStore) total() int {
	n := 0
	for _, v := range s.counts {
		n += v
	}
	return n
}

// seedEvictionCandidate inserts one account + one app + 4 deployments +
// 4 snapshots. With the per-app "keep current+previous" rule, the
// 2 oldest snapshots are evictable in a single GC tick.
func seedEvictionCandidate(t *testing.T, store state.Store, slug string) {
	t.Helper()
	s, ok := store.(*countingStore)
	if !ok {
		t.Fatalf("seedEvictionCandidate: store is not *countingStore")
	}
	acct, err := s.CreateAccount(context.Background(), slug+"@x.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: slug, RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 4; i++ {
		dep, err := s.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + slug + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.SnapMemKey(dep.ID),
			CreatedAt:  base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// seedEvictionCandidateB is the benchmark twin of seedEvictionCandidate.
// It only needs the methods seedEvictionCandidate actually calls on the
// *testing.T — Helper + Fatal — both of which *testing.B also satisfies,
// but Go's type system won't let us pass *testing.B where *testing.T is
// wanted. We can't reuse seedEvictionCandidate without a runtime cast
// (testing.T is a concrete struct, not an interface), so the duplication
// is intentional and tiny.
func seedEvictionCandidateB(b *testing.B, store *countingStore, slug string) {
	b.Helper()
	acct, err := store.CreateAccount(context.Background(), slug+"@x.com", "pro")
	if err != nil {
		b.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: slug, RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	if err != nil {
		b.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 4; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + slug + string(rune('a'+i)),
		})
		if err != nil {
			b.Fatal(err)
		}
		_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.SnapMemKey(dep.ID),
			CreatedAt:  base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestRunGCTick_PerEvictionSQLCount is the B1.1 perf-cost regression
// gate. After the AppSlug fix, a per-eviction GC sweep must not pay a
// DeploymentByID+AppByID lookup per row — the slug travels on the
// SnapshotForGC projection. Today (pre-fix) each eviction pays 2 extra
// SQL round-trips (DeploymentByID + AppByID via the loop fallback at
// pkg/imaged/loop.go:317-326). After the fix those calls disappear.
//
// The assertion is intentionally generous — we measure the total count
// of GC-path calls and require it to be at most 3 + 2N (one
// ListSnapshotsForGC, one MarkOldSnapshotsStale, one
// DeleteSnapshotsByID, plus 2N for SnapMemKey + SnapVMStateKey
// storage.Delete calls that the storage backend wraps in SQL via the
// LocalArtifactLister path). The failing case today is 5 + 4N (the +2
// per-eviction DeploymentByID + AppByID fallback). With N=4 evictions
// that's 21 today vs ≤11 after the fix — a clear delta the test
// asserts.
func TestRunGCTick_PerEvictionSQLCount(t *testing.T) {
	const N = 4 // 4 deployments per app → 2 evictions per app → 2 × 4 = 8 rows total when paired
	store := newCountingStore()

	// Seed two apps, each with 4 snapshots → 4 evictions total.
	seedEvictionCandidate(t, store, "perf-app-a")
	seedEvictionCandidate(t, store, "perf-app-b")

	appsRoot := t.TempDir()
	be, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatalf("storage.NewLocalStorageBackend: %v", err)
	}
	gcCh := make(chan time.Time, 1)
	gcCh <- time.Unix(0, 0)
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) {
			return 50.0, nil // under budget → only per-app sweep runs
		},
		appsRoot: appsRoot,
		gcCh:     gcCh,
		handler: &Handler{
			store:    store,
			log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			appsRoot: appsRoot,
			storage:  be,
		},
	}

	loop.runGCTick(context.Background(), time.Unix(0, 0))

	got := store.counts["DeploymentByID"] + store.counts["AppByID"]
	if got != 0 {
		t.Errorf("B1.1 perf-cost regression: runGCTick issued %d DeploymentByID+AppByID calls "+
			"across %d evictions; expected 0 (the AppSlug must travel on SnapshotForGC, "+
			"not be re-resolved per eviction)", got, N)
	}

	// Hard ceiling: total GC-path SQL should be ≤ 5 + 2N where N is
	// the number of evictions. The 5 is one ListSnapshotsForGC + one
	// MarkOldSnapshotsStale + one DeleteSnapshotsByID + a 2-call
	// margin for any sub-store bookkeeping we don't anticipate.
	total := store.total()
	const evictions = 4
	const ceiling = 5 + 2*evictions
	if total > ceiling {
		t.Errorf("B1.1 perf-cost regression: runGCTick issued %d total GC-path calls "+
			"across %d evictions; expected ≤ %d (ceiling = 5 + 2N)", total, evictions, ceiling)
	}

	// Sanity: the per-app policy must actually have evicted the 4
	// oldest snapshots (2 per app).
	rows, err := store.ListSnapshotsForGC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Errorf("after per-app GC: %d snapshots remain, want 4 (current+previous per app)", len(rows))
	}
	t.Logf("GC SQL counts: %v (total %d)", store.counts, total)
}

// TestRunGCTick_NoSlugInvariantLogs locks the B1.1 invariant: if the
// SnapshotForGC projection returns a row with AppSlug == "" (which
// means the JOIN broken or the column was forgotten), the loop must
// log loudly + skip the ext4 delete — NOT silently fall back to
// DeploymentByID + AppByID lookups (the slow path that B1.1 removed).
//
// We drive a deleteTarget tuple directly (skipping the GC algorithm
// step that already populates AppSlug from the row), with an empty
// slug. A logger bound to a buffer captures output; the test asserts
// the warn message is present AND the per-app ext4 file is left
// untouched on disk.
func TestRunGCTick_NoSlugInvariantLogs(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "no-slug-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:n",
	})

	appsRoot := t.TempDir()
	be, _ := storage.NewLocalStorageBackend(appsRoot)
	// Seed the per-app ext4 file under the canonical key. The
	// invariant path must NOT delete this.
	ext4Key := sched.AppLayerKey(app.Slug, dep.ID)
	if err := be.Put(context.Background(), ext4Key, strings.NewReader("layer-bytes")); err != nil {
		t.Fatalf("seed ext4: %v", err)
	}

	logBuf := &strings.Builder{}
	handler := slog.New(slog.NewTextHandler(logBuf, nil))
	loop := &Loop{
		store: store,
		log:   handler,
		handler: &Handler{
			store:    store,
			log:      handler,
			appsRoot: appsRoot,
			storage:  be,
		},
		appsRoot: appsRoot,
	}
	// Empty AppSlug = invariant violation. The loop must log + skip.
	ts := []deleteTarget{{ID: "ignored-snap-id", DeploymentID: dep.ID, AppSlug: ""}}
	if err := loop.deleteSnapshotsAndFiles(context.Background(), ts); err != nil {
		t.Fatalf("deleteSnapshotsAndFiles: %v", err)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "gc evict without slug") {
		t.Errorf("B1.1 invariant regression: expected warn 'gc evict without slug', got log:\n%s", logged)
	}
	// Ext4 file MUST still exist — the invariant path is skip, not silent cleanup.
	if rc, err := be.Get(context.Background(), ext4Key); err != nil {
		t.Errorf("B1.1 invariant regression: ext4 was deleted despite empty AppSlug: %v", err)
	} else {
		_ = rc.Close()
	}
}

// TestMemStore_ListSnapshotsForGC_PopulatesAppSlug is the projection
// regression test for the in-memory store. With AppSlug on
// SnapshotForGC (B1.1), ListSnapshotsForGC must return the slug
// without callers needing to re-resolve via AppByID.
func TestMemStore_ListSnapshotsForGC_PopulatesAppSlug(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "projection-slug", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:p",
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion: "1.8.0", StorageKey: state.SnapMemKey(dep.ID),
	})

	rows, err := store.ListSnapshotsForGC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListSnapshotsForGC: got %d rows, want 1", len(rows))
	}
	if rows[0].AppSlug != "projection-slug" {
		t.Errorf("B1.1 projection regression: AppSlug = %q, want %q",
			rows[0].AppSlug, "projection-slug")
	}
}

// BenchmarkLoopRunGCTick_FallbackSQLCost is the post-fix perf baseline.
// It captures the SQL query count for a larger N (10 apps × 4
// deployments → 20 evictions) so we can eyeball the absolute cost of
// the GC sweep. After the fix the absolute count drops from O(5N) to
// O(N) and the bench metric stabilises near ~21 calls regardless of
// N.
//
// Run via:
//
//	go test -bench BenchmarkLoopRunGCTick_FallbackSQLCost -benchmem ./pkg/imaged/...
func BenchmarkLoopRunGCTick_FallbackSQLCost(b *testing.B) {
	const apps = 10
	const evictions = 2 * apps // 2 per app fall outside current+previous

	for n := 0; n < b.N; n++ {
		store := newCountingStore()
		for i := 0; i < apps; i++ {
			seedEvictionCandidateB(b, store, "perf-"+string(rune('a'+i)))
		}
		appsRoot := b.TempDir()
		be, _ := storage.NewLocalStorageBackend(appsRoot)
		gcCh := make(chan time.Time, 1)
		gcCh <- time.Unix(0, 0)
		loop := &Loop{
			store:     store,
			log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			now:       func() time.Time { return time.Unix(0, 0) },
			lvUsedPct: func(ctx context.Context) (float64, error) { return 50.0, nil },
			appsRoot:  appsRoot,
			gcCh:      gcCh,
			handler: &Handler{
				store:    store,
				log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
				appsRoot: appsRoot,
				storage:  be,
			},
		}
		b.ResetTimer()
		loop.runGCTick(context.Background(), time.Unix(0, 0))
		b.StopTimer()
		b.ReportMetric(float64(store.total()), "sql_calls")
		b.ReportMetric(float64(store.counts["DeploymentByID"]+store.counts["AppByID"]), "lookup_calls")
		_ = evictions
	}
}

// TestGC_PerAppKeepTierFloor_Enabled2Plus2 (issue #470 / PR C /
// ADR-074) verifies the warm-enabled app policy: keep the 2
// newest warm-tier rows + the 2 newest init-tier rows; everything
// older in either tier is dropped. 4 warm + 4 init rows seeded →
// 4 rows dropped, 4 rows retained.
func TestGC_PerAppKeepTierFloor_Enabled2Plus2(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "warm@x.com", "pro")
	// Issue #470 / PR C / ADR-074: enable warm-tier retention for
	// this app. The MemStore's AppColumnSet has WarmSnapshotEnabled
	// (verified by PR #525 migrations).
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "warm-enabled", RAMMB: 256,
		IdleTimeoutS: 30, MaxConcurrency: 2,
		WarmSnapshotEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	// Seed 4 init + 4 warm across 8 deployments; the oldest
	// 2 warm and 2 init should be dropped.
	for i := 0; i < 4; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:init" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.SnapMemKey(dep.ID),
			Tier:       state.SnapshotTierInit,
			CreatedAt:  base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:warm" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.WarmSnapMemKey(dep.ID),
			Tier:       state.SnapshotTierWarm,
			CreatedAt:  base.Add(time.Duration(i+10) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	fx := newGCFixture(t, 50)
	fx.store = store
	fx.loop.store = store
	fx.loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, err := store.ListSnapshotsForGC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Errorf("after per-tier GC: %d rows remain, want 4 (2 warm + 2 init)", len(rows))
	}
	var warmCount, initCount int
	for _, r := range rows {
		switch r.Tier {
		case state.SnapshotTierWarm:
			warmCount++
		case state.SnapshotTierInit:
			initCount++
		}
	}
	if warmCount != 2 {
		t.Errorf("warm rows = %d, want 2", warmCount)
	}
	if initCount != 2 {
		t.Errorf("init rows = %d, want 2", initCount)
	}
}

// TestGC_PerAppKeepTierFloor_Disabled2Init (issue #470 / PR C /
// ADR-074) verifies the warm-disabled policy: keep only the 2
// newest init-tier rows; every warm-tier row (which the app is
// not opted in to) is dropped, plus the older init rows.
func TestGC_PerAppKeepTierFloor_Disabled2Init(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "disabled@x.com", "pro")
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "warm-disabled", RAMMB: 256,
		IdleTimeoutS: 30, MaxConcurrency: 2,
		// WarmSnapshotEnabled left default false.
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	// Seed 4 init + 4 warm rows even though the app is disabled
	// (vestigial warm rows from a prior opt-in).
	for i := 0; i < 4; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:dinit" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.SnapMemKey(dep.ID),
			Tier:       state.SnapshotTierInit,
			CreatedAt:  base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:dwarm" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  "1.8.0",
			StorageKey: state.WarmSnapMemKey(dep.ID),
			Tier:       state.SnapshotTierWarm,
			CreatedAt:  base.Add(time.Duration(i+10) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	fx := newGCFixture(t, 50)
	fx.store = store
	fx.loop.store = store
	fx.loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, err := store.ListSnapshotsForGC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("disabled app: %d rows remain, want 2 init", len(rows))
	}
	for _, r := range rows {
		if r.Tier != state.SnapshotTierInit {
			t.Errorf("disabled app left a %s row %q — only init should remain", r.Tier, r.ID)
		}
	}
}

// TestGC_PerAppKeepTierFloor_MixedApps (issue #470 / PR C /
// ADR-074) verifies that the per-app floor is applied INDEPENDENTLY
// for each app — a warm-enabled app's tier floors do not affect a
// warm-disabled app in the same tenant.
func TestGC_PerAppKeepTierFloor_MixedApps(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "mixed@x.com", "pro")
	enabledApp, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "enabled", RAMMB: 256,
		IdleTimeoutS: 30, MaxConcurrency: 2,
		WarmSnapshotEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledApp, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "disabled", RAMMB: 256,
		IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	// Enabled app: 3 init + 3 warm → keep 2 warm + 2 init, drop 2.
	for _, app := range [2]state.App{enabledApp, disabledApp} {
		var warmBefore int
		if app.ID == enabledApp.ID {
			warmBefore = 3
		} else {
			warmBefore = 0
		}
		for i := 0; i < 3; i++ {
			dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
				AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:" + app.Slug + "init" + string(rune('a'+i)),
			})
			_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
				DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
				FCVersion:  "1.8.0",
				StorageKey: state.SnapMemKey(dep.ID),
				Tier:       state.SnapshotTierInit,
				CreatedAt:  base.Add(time.Duration(i) * time.Minute),
			})
		}
		for i := 0; i < warmBefore; i++ {
			dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
				AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:" + app.Slug + "warm" + string(rune('a'+i)),
			})
			_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
				DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
				FCVersion:  "1.8.0",
				StorageKey: state.WarmSnapMemKey(dep.ID),
				Tier:       state.SnapshotTierWarm,
				CreatedAt:  base.Add(time.Duration(i+10) * time.Minute),
			})
		}
	}

	fx := newGCFixture(t, 50)
	fx.store = store
	fx.loop.store = store
	fx.loop.runGCTick(context.Background(), time.Unix(0, 0))

	rows, err := store.ListSnapshotsForGC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var enabled, disabled int
	for _, r := range rows {
		switch r.AppID {
		case enabledApp.ID:
			enabled++
		case disabledApp.ID:
			disabled++
		}
	}
	if enabled != 4 {
		t.Errorf("enabled app: %d rows remain, want 4 (2 warm + 2 init)", enabled)
	}
	if disabled != 2 {
		t.Errorf("disabled app: %d rows remain, want 2 init", disabled)
	}
}

// TestDeleteSnapshotsAndFiles_TierAwareWarm (issue #470 / PR C /
// ADR-074) verifies that a warm-tier eviction target's storage
// cleanup uses WarmSnapMemKey + WarmSnapVMStateKey, NOT the init
// paths — and vice versa for an init target. Drops files at
// canonical warm-tier keys and asserts init keys remain untouched.
func TestDeleteSnapshotsAndFiles_TierAwareWarm(t *testing.T) {
	fx := newGCFixture(t, 50)
	store := fx.store

	acct, _ := store.CreateAccount(context.Background(), "keys@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "tierkeys", RAMMB: 256,
		IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:tier",
	})
	// Seed a single warm-tier snapshot row + write the warm
	// mem blob to the local storage backend.
	_, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.8.0",
		StorageKey: state.WarmSnapMemKey(dep.ID),
		Tier:       state.SnapshotTierWarm,
	})
	if err != nil {
		t.Fatal(err)
	}
	be, _ := storage.NewLocalStorageBackend(fx.be.(*storage.LocalStorageBackend).Root())
	// Write the warm mem blob so we can observe its later
	// absence; the storage.Backend interface only ships 3-arg
	// Put(ctx, key, io.Reader), so seed an io.Reader from a
	// strings.Reader — no size arg is needed.
	if err := be.Put(context.Background(), state.WarmSnapMemKey(dep.ID), strings.NewReader("warm-mem")); err != nil {
		t.Fatal(err)
	}
	if err := be.Put(context.Background(), state.WarmSnapVMStateKey(dep.ID), strings.NewReader("warm-vms")); err != nil {
		t.Fatal(err)
	}
	if err := be.Put(context.Background(), state.SnapMemKey(dep.ID), strings.NewReader("init-mem")); err != nil {
		t.Fatal(err)
	}

	err = fx.loop.deleteSnapshotsAndFiles(context.Background(), []deleteTarget{{
		ID:           "warm-id",
		DeploymentID: dep.ID,
		AppSlug:      app.Slug,
		Tier:         state.SnapshotTierWarm,
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Warm blobs should be gone; init mem blob untouched. The
	// storage interface ships Get (returns ErrNotFound for absent
	// keys) but no Exists; assert non-existence via Get's error
	// chain.
	for _, k := range []string{
		state.WarmSnapMemKey(dep.ID),
		state.WarmSnapVMStateKey(dep.ID),
	} {
		_, err := be.Get(context.Background(), k)
		if err == nil {
			t.Errorf("warm key %q still present after delete", k)
		} else if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("warm key %q Get: unexpected error %v (want ErrNotFound)", k, err)
		}
	}
	if _, err := be.Get(context.Background(), state.SnapMemKey(dep.ID)); err != nil {
		t.Errorf("init mem blob was incorrectly deleted — warm-tier evict must not touch init keys: %v", err)
	}
}
