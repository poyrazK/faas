// park_wake_cycle_e2e_test.go — the loop the product is built on, closed.
//
// Scale-to-zero is one cycle: serve, park to a snapshot, restore from THAT
// snapshot on the next request. Every piece of it now has a test, and nothing
// asserted that the pieces connect — that the artifact written on park is the
// artifact read on wake. A platform can pass a capture test and a restore test
// and still restore something else, or nothing.
//
// Closing it means crossing the handoff that no CI test has ever exercised:
//
//	schedd captures (PauseAndSnapshot)
//	  -> pg_notify "snapshot_written"
//	    -> imaged writes the snapshots row (the SOLE row writer, spec §6.0)
//	      -> the next wake selects that row and restores from it
//
// Three daemons and a notification, and until imaged could boot in CI (#3189)
// none of it was reachable: a park could be observed capturing, and the row it
// produced could not. The wake path would then find no snapshot and cold-boot,
// which looks like success from the customer's side and silently costs the
// entire latency benefit the architecture exists for.
//
// This also exercises pg_notify delivery end to end, which nothing else does.

package e2e_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
)

// newParkWakeFixture is the normal-path fixture with imaged in the loop.
//
// imaged needs its builder base pre-provisioned on a CI runner — the base is
// not stubbable (it must contain railpack, buildctl, runc and guest-init), and
// a runner has none. Pointing FAAS_BUILDER_BASE_REF at an unservable digest
// makes imaged take its normal registry-outage fallback onto what is staged.
func newParkWakeFixture(t *testing.T, slug string) *normalPathFixture {
	t.Helper()
	storageRoot := t.TempDir()
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)

	guestInitBody := []byte("#!/bin/sh\n")
	guestInit := filepath.Join(t.TempDir(), "faas-guest-init")
	if err := os.WriteFile(guestInit, guestInitBody, 0o755); err != nil {
		t.Fatalf("write guest-init: %v", err)
	}
	t.Setenv("FAAS_GUEST_INIT", guestInit)
	seedBootContractBuilderBase(t, storageRoot, guestInitBody)

	// imaged validates an existing base read-only through debugfs. The cycle
	// is what is under test, not ext4 mechanics.
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "debugfs"), []byte("#!/bin/sh\necho 'Inode: 1'\n"), 0o755); err != nil {
		t.Fatalf("write debugfs shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := newNormalPathFixtureWith(t, slug, api.PlanHobby, e2etest.Imaged,
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT="+storageRoot,
		"FAAS_BUILDER_BASE_REF=127.0.0.1:1/onebox-faas/builder-base@sha256:"+repeatChar("0", 64),
	)
	if f == nil {
		return nil
	}
	// Re-point the fixture's artifact store at the root the daemons were told
	// to use.
	//
	// The normal-path fixture makes its own directory and passes it as
	// FAAS_STORAGE_ROOT; the extraEnv above is appended after that, so the
	// daemons follow ours while f.artifacts still pointed at theirs. The test
	// then published its layer somewhere nothing reads, and the first wake
	// failed with "live deployment artifact is unavailable" — the layer was
	// written, just not where schedd verifies it.
	artifacts, err := storage.NewLocalStorageBackend(storageRoot)
	if err != nil {
		t.Fatalf("open artifact store at the daemons' storage root: %v", err)
	}
	f.artifacts = artifacts
	return f
}

// TestE2E_ParkWakeCycle_RestoresTheSnapshotItJustWrote is the whole loop in
// one pass: cold boot, park, and a second wake that must restore the artifact
// the park produced.
//
// Each assertion below fails on a different real defect:
//
//   - no capture on park: the guest's memory is gone and the next wake pays a
//     cold boot forever;
//   - capture but no row: the schedd -> notify -> imaged handoff is broken, and
//     every wake cold-boots while the disk fills with snapshots nothing reads;
//   - row but no restore: wake selection ignores a perfectly good snapshot,
//     which is the p95 budget quietly evaporating;
//   - restore of a DIFFERENT key: the cycle does not close, and an app can be
//     resumed from another deployment's memory.
func TestE2E_ParkWakeCycle_RestoresTheSnapshotItJustWrote(t *testing.T) {
	f := newParkWakeFixture(t, "park-wake-cycle")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f)

	// First wake has nothing to restore from, so it must cold boot. Asserting
	// this pins the starting state: a later restore cannot be explained by a
	// snapshot that was already lying around.
	wakeNormalPathApp(t, f)
	if n := len(f.vmmd.RestoreCalls()); n != 0 {
		t.Fatalf("first wake restored %d times with no snapshot on record", n)
	}
	if n := len(f.vmmd.ColdBootCalls()); n == 0 {
		t.Fatal("first wake neither restored nor cold booted")
	}
	instance, err := f.store.RunningInstanceForApp(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("no running instance after first wake: %v", err)
	}

	// Park. schedd captures and then announces the capture; imaged is the only
	// component that may turn that announcement into a row.
	conn, err := grpc.NewClient("unix://"+f.h.ScheddSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial schedd: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := scheddpb.NewScheddClient(conn).ParkInstance(f.ctx,
		&scheddpb.ParkInstanceRequest{InstanceId: instance.ID, Reason: "e2e_cycle"}); err != nil {
		t.Fatalf("park instance: %v", err)
	}

	waitForWake(t, 30*time.Second, func() bool {
		for _, c := range f.vmmd.SnapshotCalls() {
			if c.GetInstance() == instance.ID {
				return true
			}
		}
		return false
	}, "park never captured; there was no snapshot to record, so the cycle cannot close")

	// The handoff. A capture that never becomes a row is worse than no capture
	// at all: the bytes are on disk, the wake path cannot see them, and the
	// only symptom is latency.
	var snap state.Snapshot
	waitForWake(t, 60*time.Second, func() bool {
		s, err := f.store.LatestSnapshotForTier(f.ctx, dep.ID, state.SnapshotTierInit)
		if err != nil {
			return false
		}
		snap = s
		return true
	}, "schedd captured a snapshot and no row ever appeared — the "+
		"schedd -> pg_notify(snapshot_written) -> imaged handoff is broken, and imaged is "+
		"the only component allowed to write that row (spec §6.0)")

	if snap.StorageKey == "" {
		t.Fatal("recorded snapshot has no storage key; wake selection rejects a keyless row")
	}

	// Second wake must restore, and restore THIS artifact.
	restoresBefore := len(f.vmmd.RestoreCalls())
	wakeNormalPathApp(t, f)
	restores := f.vmmd.RestoreCalls()
	if len(restores) == restoresBefore {
		t.Fatalf("second wake did not restore despite a usable snapshot (cold boots=%d); "+
			"the platform is paying a cold boot on every wake and the snapshot is dead weight",
			len(f.vmmd.ColdBootCalls()))
	}
	last := restores[len(restores)-1]
	if got := last.GetSnapshot().GetStorageKey(); got != snap.StorageKey {
		t.Errorf("restored storage key %q, want %q — the wake restored something other than "+
			"the snapshot this deployment just wrote", got, snap.StorageKey)
	}
	if got := last.GetSnapshot().GetDeploymentId(); got != dep.ID {
		t.Errorf("restored deployment %q, want %q — an app must never resume from another "+
			"deployment's memory", got, dep.ID)
	}
}
