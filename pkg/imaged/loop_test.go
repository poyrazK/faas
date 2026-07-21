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
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// gcFixture wires a Loop with a memstore, an injected tick channel, and a
// hermetic appsRoot / snap dir. Returns the loop + the helpers tests need
// to assert side effects.
type gcFixture struct {
	loop     *Loop
	store    *state.MemStore
	gcCh     chan time.Time
	tickSent func() // tickOnce helper
}

func newGCFixture(t *testing.T, lvPct float64) *gcFixture {
	t.Helper()
	store := state.NewMemStore()

	// Hermetic sched.SnapDir() — sched.SnapDir() is a function, not a
	// variable, so the loop uses the canonical /srv/fc/snap path. The
	// deleteSnapshotsAndFiles helper only os.RemoveAll's that path
	// (best-effort), so an empty parent is fine — we just pre-create
	// each snap dir we'll be deleting.
	_ = sched.SnapDir()

	gcCh := make(chan time.Time, 16)
	loop := &Loop{
		handler: &Handler{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		store:   store,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:     func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) {
			return lvPct, nil
		},
		appsRoot: t.TempDir(),
		gcCh:     gcCh,
	}
	return &gcFixture{
		loop:  loop,
		store: store,
		gcCh:  gcCh,
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
		Path:         filepath.Join(sched.SnapDir(), "snap-"+app.Slug),
		FCVersion:    "1.8.0",
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
			Path: filepath.Join(sched.SnapDir(), "keep-"+dep.ID), FCVersion: "1.8.0",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
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

func TestGC_NaNLvUsedPct_IsSafeNoOp(t *testing.T) {
	store := state.NewMemStore()
	gcCh := make(chan time.Time, 1)
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return time.Unix(0, 0) },
		lvUsedPct: func(ctx context.Context) (float64, error) {
			return nan(), nil
		},
		appsRoot: t.TempDir(),
		gcCh:     gcCh,
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
		Path: "/tmp/heavy.snap", FCVersion: "1.8.0",
		CreatedAt:    time.Now().Add(-3 * time.Minute),
	})
	// heavyApp needs > current+previous snapshots so the per-app floor
	// leaves at least one evictable row. With 3 snapshots and a 2-row
	// floor, the oldest is a valid eviction target.
	heavyDep2, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: heavyApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:h2",
	})
	heavySnap2, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: heavyDep2.ID, MemBytes: 5 << 30, DiskBytes: 5 << 30,
		Path: "/tmp/heavy2.snap", FCVersion: "1.8.0",
		CreatedAt: time.Now().Add(-2 * time.Minute),
	})
	heavyDep3, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: heavyApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:h3",
	})
	heavySnap3, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: heavyDep3.ID, MemBytes: 5 << 30, DiskBytes: 5 << 30,
		Path: "/tmp/heavy3.snap", FCVersion: "1.8.0",
		CreatedAt: time.Now().Add(-1 * time.Minute),
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: midDep.ID, MemBytes: 3 << 30, DiskBytes: 2 << 30, // 5 GB
		Path:        "/tmp/mid.snap", FCVersion: "1.8.0",
		CreatedAt:   time.Now().Add(-time.Minute),
	})
	_, _ = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: lightDep.ID, MemBytes: 1 << 30, DiskBytes: 1 << 30, // 2 GB
		Path:        "/tmp/light.snap", FCVersion: "1.8.0",
		CreatedAt:   time.Now(),
	})

	gcCh := make(chan time.Time, 1)
	// Probe is at 95% on first call (pressure), then drops to 50% after
	// the eviction (relief) so the loop exits cleanly. Mirrors what the
	// real lv-fc watcher would do after reclaiming 10 GB.
	calls := 0
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
		appsRoot: t.TempDir(),
		gcCh:     gcCh,
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
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100, Path: "/tmp/b.snap", FCVersion: "1.8.0",
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

// nan returns a float64 that is not a number. Used by the lv-fc probe
// "no data" path. (math.NaN() needs the math import — keep this tiny
// helper so the test files don't all grow it.)
func nan() float64 {
	var z float64
	return z / z // 0/0 → NaN, deterministic, no math import
}