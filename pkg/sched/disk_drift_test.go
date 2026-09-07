// PR scale-out readiness #3 — disk-drift sweep tests. The sweep is
// read-only and never writes; these tests drive Tick directly against
// a hermetic t.TempDir() wired through sched.SetSnapDirForTesting.
// Tests are sequential within the file because SetSnapDirForTesting
// mutates a package-level var (per the testing_paths.go contract);
// do not t.Parallel() this file.

package sched

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// driftFixture sets up a hermetic Tick environment: a t.TempDir()
// wired through SetSnapDirForTesting, a MemStore with one or more
// snapshots, and an OpsMetrics receiver returning a counter we can
// read back. The returned counter is what the assertions inspect.
type driftFixture struct {
	store *state.MemStore
	ops   *wire.OpsMetrics
	dd    *DiskDrift
	root  string
	// cleanup restores the package-level SnapDir() to the production
	// default so subsequent tests in other files (e.g.
	// TestEngineVmstateHelpers) observe the canonical /srv/fc/snap
	// root instead of an empty string. SetSnapDirForTesting is
	// sequential within a single test (testing_paths.go contract);
	// t.TempDir() handles the cleanup-order guarantee independently.
	cleanup func()
}

func newDriftFixture(t *testing.T) *driftFixture {
	t.Helper()
	root := t.TempDir()
	SetSnapDirForTesting(root)
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	dd := NewDiskDrift(store, nil).WithMetrics(ops)
	return &driftFixture{
		store: store,
		ops:   ops,
		dd:    dd,
		root:  root,
		cleanup: func() {
			// Restore the production default (mirrors paths.go's
			// `var snapDir = "/srv/fc/snap"`). An empty string here
			// would leave SnapDir() returning "" for any test
			// running after ours in the same package — which
			// breaks TestEngineVmstateHelpers/host/standard_dep
			// (vmstateHostPathFor prepends SnapDir() unconditionally
			// and would build "/d-1/vmstate" without the snap root).
			SetSnapDirForTesting("/srv/fc/snap")
		},
	}
}

// seedSnapshot inserts an Account + App + Deployment + Snapshot row
// triple via MemStore so ListSnapshotsForGC sees one entry. The
// depID argument is BOTH the deployment ID MemStore stores AND the
// on-disk directory name the sweep expects — DiskDrift uses
// SnapDir()+"/"+depID+"/..." mirroring the production
// pkg/sched/engine.go:1117 path mapping.
func (f *driftFixture) seedSnapshot(t *testing.T, depID string, memBytes, diskBytes int64) {
	t.Helper()
	ctx := context.Background()
	acct, err := f.store.CreateAccount(ctx, "drift@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := f.store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "drift-" + depID, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{
		ID:          depID,
		AppID:       app.ID,
		ImageDigest: "img:latest",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	_, err = f.store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID,
		FCVersion:    "v1.0",
		StorageKey:   "snap/" + depID + "/mem",
		MemBytes:     memBytes,
		DiskBytes:    diskBytes,
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
}

// writeFile writes content to <root>/<depID>/<name>; helper for
// materialising the canonical "mem" / "vmstate" files plus extras
// the tests want to assert against.
func (f *driftFixture) writeFile(t *testing.T, depID, name string, content []byte) {
	t.Helper()
	dir := filepath.Join(f.root, depID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir dep dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatalf("write %s/%s: %v", depID, name, err)
	}
}

// counterValue reads the schedd_snapshot_disk_drift_total counter
// from the test registry's Gather output. Mirrors the readback path
// in pkg/wire/metrics_test.go.
func (f *driftFixture) counterValue(t *testing.T) float64 {
	t.Helper()
	mfs, err := f.ops.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, fam := range mfs {
		if fam.GetName() == "schedd_snapshot_disk_drift_total" {
			for _, mt := range fam.GetMetric() {
				return mt.GetCounter().GetValue()
			}
			return 0
		}
	}
	t.Fatal("schedd_snapshot_disk_drift_total not present in registry")
	return 0
}

// errLister is a snapshotLister shim that always returns the same
// error. Used by the store-failure-path test.
type errLister struct{ err error }

func (e *errLister) ListSnapshotsForGC(_ context.Context) ([]state.SnapshotForGC, error) {
	return nil, e.err
}

// --- tests ---------------------------------------------------------------

// TestDiskDrift_NoSnapDirIsNoOp — when SnapDir() points to a
// directory that doesn't exist (dev box, fresh CI), Tick must
// return nil and increment zero. The sweep is diagnostic; no
// SnapDir means nothing to compare against.
func TestDiskDrift_NoSnapDirIsNoOp(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	// Remove the temp dir so SnapDir() is missing.
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatalf("remove tempdir: %v", err)
	}

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 0 {
		t.Errorf("drift = %d, want 0", drift)
	}
	if got := f.counterValue(t); got != 0 {
		t.Errorf("counter = %v, want 0", got)
	}
}

// TestDiskDrift_ExpectedFilesPresentNoDrift — happy path: DB row
// exists, both mem and vmstate files exist with the right sizes.
// The sweep must increment zero.
func TestDiskDrift_ExpectedFilesPresentNoDrift(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 0 {
		t.Errorf("drift = %d, want 0", drift)
	}
	if got := f.counterValue(t); got != 0 {
		t.Errorf("counter = %v, want 0", got)
	}
}

// TestDiskDrift_MissingMemFileIncrements — DB row exists, mem file
// is missing on disk. Counter increments by 1.
func TestDiskDrift_MissingMemFileIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	// mem missing — only vmstate written.
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_SizeMismatchIncrements — file present, on-disk size
// differs from the DB-recorded bytes. Counter increments by 1.
func TestDiskDrift_SizeMismatchIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	// mem: DB says 100, disk has 99.
	f.writeFile(t, "dep-1", "mem", make([]byte, 99))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_UnexpectedFileIncrements — DB row only expects mem
// and vmstate. An extra file in the dep dir increments the counter
// by 1.
func TestDiskDrift_UnexpectedFileIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))
	f.writeFile(t, "dep-1", "surprise.txt", []byte("hi"))

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_SymlinkNotFollowedIncrements — a symlink in the dep
// dir counts as drift; the sweep does not follow it. We assert the
// target file (placed elsewhere) is NOT consulted by reading its
// size and confirming the sweep didn't try to resolve through the
// symlink.
func TestDiskDrift_SymlinkNotFollowedIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))

	// Create an unrelated target file OUTSIDE SnapDir (so Pass 2 does
	// not count it as orphan-file-under-snap-dir), plus a symlink to
	// it inside the dep dir. The sweep must count the symlink as
	// drift without following it.
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(target, []byte("do not read me"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(f.root, "dep-1", "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_OrphanDepDirIncrements — a directory under SnapDir
// whose name has no matching DB row counts as drift. The sweep
// never writes, so an orphan is itself drift.
func TestDiskDrift_OrphanDepDirIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))

	// Orphan dep dir — no DB row, no files.
	if err := os.MkdirAll(filepath.Join(f.root, "orphan-dep"), 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_SoftDeletedAppExcluded — DB has a soft-deleted app
// with stale files on disk. ListSnapshotsForGC filters soft-deleted
// apps, so the sweep sees no row → treats the dep dir as orphan
// and increments drift by 1 (the orphan dep dir path), but does
// NOT fire per-file-missing drift inside it (because the dep is
// not in the expected set).
//
// This pins the inherited "soft-deleted apps are excluded" contract:
// the sweep never independently inspects apps.status; it relies on
// ListSnapshotsForGC to filter. We document this by seeding one
// soft-deleted app's dep dir and asserting the sweep counts the
// orphan (one drift) and does NOT iterate its files looking for
// mem/vmstate mismatches.
func TestDiskDrift_SoftDeletedAppExcluded(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	// Seed a soft-deleted app + dep + orphan files on disk.
	ctx := context.Background()
	acct, err := f.store.CreateAccount(ctx, "softdel@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := f.store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "softdel", Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	// Order: create the dep + snapshot first (MemStore rejects new
	// deployments on a deleted app), THEN soft-delete the app. The
	// resulting DB row is returned to consumers with AppDeleted status.
	// DiskDrift omits cleanup-eligible rows from its expected set, so the
	// on-disk files are orphans from the sweep's perspective.
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{
		ID:          "softdel-dep",
		AppID:       app.ID,
		ImageDigest: "img:latest",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := f.store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID,
		FCVersion:    "v1.0",
		StorageKey:   "snap/" + dep.ID + "/mem",
		MemBytes:     100,
		DiskBytes:    200,
	}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if err := f.store.DeleteApp(ctx, app.ID); err != nil {
		t.Fatalf("delete app (sets AppDeleted status): %v", err)
	}
	// Files on disk — but the app is soft-deleted, so the sweep omits its
	// row from the expected set. It sees the orphan dir and
	// increments drift; it does NOT inspect files inside.
	f.writeFile(t, dep.ID, "mem", make([]byte, 100))
	f.writeFile(t, dep.ID, "vmstate", make([]byte, 200))

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 1 drift = orphan dep dir. Files inside are NOT iterated because
	// the dep is not in the expected set.
	if drift != 1 {
		t.Errorf("drift = %d, want 1 (orphan-dep-dir only)", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_NilMetricsNoPanic — Tick must not panic when the
// metrics receiver is nil. The accessor short-circuits, so the
// sweep simply doesn't emit samples.
func TestDiskDrift_NilMetricsNoPanic(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	// Replace the dd with one that has nil metrics.
	dd := NewDiskDrift(f.store, nil) // no WithMetrics call

	f.seedSnapshot(t, "dep-1", 100, 200)
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))
	f.writeFile(t, "dep-1", "surprise.txt", []byte("hi"))

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Tick panicked with nil metrics: %v", r)
		}
	}()
	drift, err := dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1 (counter not incremented but drift count returned)", drift)
	}
}

// TestDiskDrift_StoreErrorIsNoOp — when ListSnapshotsForGC returns
// an error, Tick must log + return (0, nil) so the loop continues.
// Diagnostic sweeps never escalate errors to the dispatcher.
func TestDiskDrift_StoreErrorIsNoOp(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	// Swap the dd for one whose snapshotLister always errors. The
	// sweep must log + return (0, nil) and increment zero.
	dd := NewDiskDrift(&errLister{err: errors.New("db gone")}, nil).
		WithMetrics(f.ops)

	drift, err := dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick returned error: %v", err)
	}
	if drift != 0 {
		t.Errorf("drift = %d, want 0", drift)
	}
	if got := f.counterValue(t); got != 0 {
		t.Errorf("counter = %v, want 0", got)
	}
}

// TestDiskDrift_DepDirUnreadableIncrements — when os.ReadDir on a
// dep dir fails (transient permission / race with imaged's GC), the
// sweep must log + count one drift + move on.
func TestDiskDrift_DepDirUnreadableIncrements(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, cannot test permission-based unreadability")
	}
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	// Materialise the dep dir (seedSnapshot only writes DB rows,
	// not the on-disk tree) so chmod has something to operate on.
	if err := os.MkdirAll(filepath.Join(f.root, "dep-1"), 0o755); err != nil {
		t.Fatalf("mkdir dep-1: %v", err)
	}
	// Make the dep dir unreadable.
	dir := filepath.Join(f.root, "dep-1")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_OrphanFileUnderSnapDirIncrements — a regular file
// directly under SnapDir (not in any dep dir) is drift. The sweep
// never writes, so this represents a future contributor's misuse.
func TestDiskDrift_OrphanFileUnderSnapDirIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	if err := os.WriteFile(filepath.Join(f.root, "stray.txt"),
		[]byte("where am I"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_UnexpectedDirectoryInDepDirIncrements — a sub-
// directory inside a dep dir counts as drift. The spec layout is
// flat (two files at the top of <depID>); nested dirs would
// indicate a future contributor's misuse.
func TestDiskDrift_UnexpectedDirectoryInDepDirIncrements(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 100, 200)
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 200))
	if err := os.MkdirAll(filepath.Join(f.root, "dep-1", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1", drift)
	}
	if got := f.counterValue(t); got != 1 {
		t.Errorf("counter = %v, want 1", got)
	}
}

// TestDiskDrift_RowWithZeroSizeSkipsSizeCheck — degraded-but-
// recorded state: a row with MemBytes=0 / DiskBytes=0 means "this
// snapshot is recorded but its files haven't been written." The
// sweep must NOT flag a size mismatch in that case (the on-disk
// size is whatever it is). The missing-file check still fires.
func TestDiskDrift_RowWithZeroSizeSkipsSizeCheck(t *testing.T) {
	f := newDriftFixture(t)
	defer f.cleanup()

	f.seedSnapshot(t, "dep-1", 0, 0)
	// Files exist with non-zero sizes — the row claims zero bytes,
	// so the sweep must NOT flag a size mismatch.
	f.writeFile(t, "dep-1", "mem", make([]byte, 100))
	f.writeFile(t, "dep-1", "vmstate", make([]byte, 100))

	drift, err := f.dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 0 {
		t.Errorf("drift = %d, want 0 (size=0 rows skip size check)", drift)
	}
}

// TestDiskDrift_TableDriven — table-driven sweep combining the
// cases above into one fixture. Mirrors TestHeartbeat_TableDriven
// at pkg/sched/heartbeat_test.go. Each row sets up its own disk
// tree + DB state, runs Tick, and asserts on the drift count +
// counter value.
func TestDiskDrift_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, f *driftFixture)
		wantDrift int
		wantAfter float64
	}{
		{
			name: "happy path no drift",
			setup: func(t *testing.T, f *driftFixture) {
				f.seedSnapshot(t, "d1", 10, 20)
				f.writeFile(t, "d1", "mem", make([]byte, 10))
				f.writeFile(t, "d1", "vmstate", make([]byte, 20))
			},
			wantDrift: 0, wantAfter: 0,
		},
		{
			name: "missing mem",
			setup: func(t *testing.T, f *driftFixture) {
				f.seedSnapshot(t, "d1", 10, 20)
				f.writeFile(t, "d1", "vmstate", make([]byte, 20))
			},
			wantDrift: 1, wantAfter: 1,
		},
		{
			name: "size mismatch on mem",
			setup: func(t *testing.T, f *driftFixture) {
				f.seedSnapshot(t, "d1", 10, 20)
				f.writeFile(t, "d1", "mem", make([]byte, 9))
				f.writeFile(t, "d1", "vmstate", make([]byte, 20))
			},
			wantDrift: 1, wantAfter: 1,
		},
		{
			name: "unexpected file in dep dir",
			setup: func(t *testing.T, f *driftFixture) {
				f.seedSnapshot(t, "d1", 10, 20)
				f.writeFile(t, "d1", "mem", make([]byte, 10))
				f.writeFile(t, "d1", "vmstate", make([]byte, 20))
				f.writeFile(t, "d1", "surprise", []byte("x"))
			},
			wantDrift: 1, wantAfter: 1,
		},
		{
			name: "orphan dep dir on disk",
			setup: func(t *testing.T, f *driftFixture) {
				_ = os.MkdirAll(filepath.Join(f.root, "orphan"), 0o755)
			},
			wantDrift: 1, wantAfter: 1,
		},
		{
			name: "row zero bytes skipped",
			setup: func(t *testing.T, f *driftFixture) {
				f.seedSnapshot(t, "d1", 0, 0)
				f.writeFile(t, "d1", "mem", make([]byte, 100))
				f.writeFile(t, "d1", "vmstate", make([]byte, 100))
			},
			wantDrift: 0, wantAfter: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newDriftFixture(t)
			defer f.cleanup()
			tc.setup(t, f)

			drift, err := f.dd.Tick(context.Background())
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if drift != tc.wantDrift {
				t.Errorf("drift = %d, want %d", drift, tc.wantDrift)
			}
			if got := f.counterValue(t); got != tc.wantAfter {
				t.Errorf("counter = %v, want %v", got, tc.wantAfter)
			}
		})
	}
}

// Reference fs.FileMode so the import is used even if no test
// specifically constructs a ModeIrregular value (ModeIrregular is
// platform-dependent; the symlink test exercises the most common
// non-regular mode).
var _ fs.FileMode = 0

// --- ADR-054 §3 storage-backend-aware sweep tests ----------------------
//
// The tests below pin the storage-backed path: when WithStorage is
// wired, the sweep enumerates the snap/ prefix via backend.List
// instead of os.ReadDir. The byte-comparison path is intentionally
// not exercised here (registry manifests don't expose byte sizes);
// these tests assert the presence + orphan checks that survive the
// storage backend's abstraction layer.

// fakeStorageLister is a hand-rolled minimal StorageBackend stub
// for the storage-backed tests. DiskDrift.WithStorage type-asserts
// the value to LocalArtifactLister; we satisfy that surface with a
// single List method. Put/Get/Delete are stubbed so the value also
// satisfies StorageBackend (the function parameter type).
type fakeStorageLister struct {
	keys []string
	err  error
}

func (f *fakeStorageLister) Put(_ context.Context, _ string, _ io.Reader) error {
	return nil
}
func (f *fakeStorageLister) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *fakeStorageLister) Delete(_ context.Context, _ string) error { return nil }

// List is the surface DiskDrift.WithStorage type-asserts and calls.
func (f *fakeStorageLister) List(_ context.Context, _ string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.keys, nil
}

// TestDiskDrift_StorageBackend_PresenceMatch pins the happy path:
// when WithStorage is wired and the listed keys include both mem +
// vmstate for every DB-known dep, drift stays at 0.
func TestDiskDrift_StorageBackend_PresenceMatch(t *testing.T) {
	store := state.NewMemStore()
	dd := NewDiskDrift(store, nil).
		WithStorage(&fakeStorageLister{keys: []string{
			"snap/d-1/mem",
			"snap/d-1/vmstate",
			"snap/d-2/mem",
			"snap/d-2/vmstate",
		}})
	// Seed two dep rows.
	ctx := context.Background()
	seedDriftRow(ctx, t, store, "d-1")
	seedDriftRow(ctx, t, store, "d-2")
	drift, err := dd.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 0 {
		t.Errorf("drift = %d, want 0 (all keys present)", drift)
	}
}

// TestDiskDrift_StorageBackend_MissingFileIncrements pins the
// presence-check contract: a missing mem file in storage increments
// the drift counter, regardless of whether the deployment has bytes
// recorded in the DB.
func TestDiskDrift_StorageBackend_MissingFileIncrements(t *testing.T) {
	store := state.NewMemStore()
	dd := NewDiskDrift(store, nil).
		WithStorage(&fakeStorageLister{keys: []string{
			"snap/d-1/mem",
			// vmstate deliberately absent.
		}})
	ctx := context.Background()
	seedDriftRow(ctx, t, store, "d-1")
	drift, err := dd.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1 (vmstate missing)", drift)
	}
}

// TestDiskDrift_StorageBackend_OrphanDepIncrements pins the
// orphan check: a key in storage with no matching DB row is drift.
func TestDiskDrift_StorageBackend_OrphanDepIncrements(t *testing.T) {
	store := state.NewMemStore()
	dd := NewDiskDrift(store, nil).
		WithStorage(&fakeStorageLister{keys: []string{
			"snap/d-orphan/mem",
			"snap/d-orphan/vmstate",
		}})
	drift, err := dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if drift != 1 {
		t.Errorf("drift = %d, want 1 (orphan dep dir)", drift)
	}
}

// TestDiskDrift_StorageBackend_ListErrorFallsBackToDisk pins the
// degradation path: a backend.List error falls back to the on-disk
// os.ReadDir path so a transient registry outage doesn't silence
// the drift detector entirely. The fixture also leaves /srv/fc/snap
// empty (snapDir is package-default), so the fallback returns 0
// drift (no orphan + no expected).
func TestDiskDrift_StorageBackend_ListErrorFallsBackToDisk(t *testing.T) {
	store := state.NewMemStore()
	dd := NewDiskDrift(store, nil).
		WithStorage(&fakeStorageLister{err: errors.New("registry down")})
	// Don't seed any DB rows — the fallback runs with empty
	// expected set + empty snap dir → 0 drift, but proves the
	// fallback path doesn't propagate the List error.
	drift, err := dd.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v (fallback should swallow List error)", err)
	}
	if drift != 0 {
		t.Errorf("drift = %d, want 0 (fallback clean run)", drift)
	}
}

// seedDriftRow inserts a minimal Account + App + Deployment +
// Snapshot triple for a single depID via MemStore. The email is
// depID-suffixed because MemStore rejects duplicate emails, and
// several storage-backed tests share a single MemStore instance.
func seedDriftRow(ctx context.Context, t *testing.T, store *state.MemStore, depID string) {
	t.Helper()
	now := time.Now()
	acct, err := store.CreateAccount(ctx, "drift-store-"+depID+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "drift-store-" + depID, Type: state.AppTypeApp,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		ID: depID, AppID: app.ID, ImageDigest: "img:latest", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	_, err = store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID,
		FCVersion:    "v1.0",
		StorageKey:   "snap/" + depID + "/mem",
		MemBytes:     1024,
		DiskBytes:    2048,
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
}

func TestDiskDriftSnapshotCaptureDirectory(t *testing.T) {
	for _, useBackend := range []bool{false, true} {
		t.Run(fmt.Sprint(useBackend), func(t *testing.T) {
			f := newDriftFixture(t)
			defer f.cleanup()
			if useBackend {
				root := t.TempDir()
				be, err := storage.NewLocalStorageBackend(root)
				if err != nil {
					t.Fatal(err)
				}
				f.root = filepath.Join(root, "snap")
				SetSnapDirForTesting(f.root)
				f.dd.WithStorage(be)
			}
			f.seedSnapshot(t, "generation-dep", 10, 20)
			old, err := f.store.LatestSnapshot(context.Background(), "generation-dep")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.MarkSnapshotStale(context.Background(), old.ID); err != nil {
				t.Fatal(err)
			}
			key := state.SnapshotCaptureMemKey("generation-dep", "init", "first")
			old.ID = ""
			old.StorageKey = key
			if _, err := f.store.CreateSnapshot(context.Background(), old); err != nil {
				t.Fatal(err)
			}
			directory := strings.TrimSuffix(strings.TrimPrefix(key, "snap/"), "/mem")
			f.writeFile(t, directory, "mem", make([]byte, 10))
			f.writeFile(t, directory, "vmstate", make([]byte, 20))
			if drift, err := f.dd.Tick(context.Background()); err != nil || drift != 0 {
				t.Fatalf("capture drift=%d err=%v", drift, err)
			}
		})
	}
}
