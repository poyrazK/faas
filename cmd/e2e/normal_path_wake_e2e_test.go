// normal_path_wake_e2e_test.go — KVM-free coverage of the wake edge selection.
//
// ADR-005's invariant is that a snapshot may only ever *accelerate* a wake:
// "a deploy must never depend on one existing or being restorable". Until this
// file existed, no test that runs in PR CI could observe which edge schedd
// actually took. Two things hid it, and both had to be fixed to get here:
//
//   - the fake vmmd left CreateFromSnapshot Unimplemented, and
//   - CI has no firecracker binary, so schedd's detected version was "" and
//     Engine.snapshotCompatible rejected every snapshot row on
//     `snap.FCVersion != e.fcVer`.
//
// The result was a suite that exercised only the cold-boot edge while
// appearing to cover wake. These tests pin the choice itself:
//
//   - the restore edge when the snapshot is usable;
//   - the cold-boot edge for each way a snapshot stops being usable — stale,
//     Firecracker-version skew (the fleet-wide event after any FC upgrade),
//     and RAM-shape skew;
//   - the two failure shapes the spec prose blurs together. vmmd degrading a
//     restore into a cold boot SERVES the request and retires the snapshot —
//     that is where ADR-005's fallback actually lives. A gRPC error out of
//     CreateFromSnapshot does NOT cold boot; schedd fails the wake.
//
// What this does NOT cover, by design: whether Firecracker can actually load
// the snapshot. That is the metal gate's job. This file covers the selection
// logic and the state transitions around it, which is where the control-plane
// bugs live.

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// createNormalPathParkedDeployment publishes a live deployment with no running
// instance, which is what a parked app looks like: routable, but the next
// request has to wake it.
func createNormalPathParkedDeployment(t *testing.T, ctx context.Context, store *state.PgStore, appID string) state.Deployment {
	t.Helper()
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + repeatChar("3", 64),
	})
	if err != nil {
		t.Fatalf("create parked deployment: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("mark parked deployment live: %v", err)
	}
	return dep
}

// normalPathSnapshotOpts are the fields a wake's compatibility check reads.
// Each zero value is the compatible one, so a test only names the axis it is
// deliberately breaking.
type normalPathSnapshotOpts struct {
	fcVersion string // "" -> the pinned, compatible version
	memBytes  int64  // 0  -> the compatible size for a seeded instance
	stale     bool
}

func seedNormalPathSnapshot(t *testing.T, ctx context.Context, store *state.PgStore, deploymentID string, opts normalPathSnapshotOpts) state.Snapshot {
	t.Helper()
	fcVersion := opts.fcVersion
	if fcVersion == "" {
		fcVersion = normalPathFCVersion
	}
	memBytes := opts.memBytes
	if memBytes == 0 {
		memBytes = int64(normalPathSnapshotRAMMB) << 20
	}
	snap, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID,
		FCVersion:    fcVersion,
		MemBytes:     memBytes,
		Tier:         state.SnapshotTierInit,
		// A canonical v2 capture key is required: SnapshotDriveKey returns ""
		// for any other shape, and an empty drive key is itself a
		// compatibility failure. Seeding a legacy key here would make every
		// test below cold-boot for a reason it was not trying to test.
		StorageKey: state.SnapshotCaptureMemKey(deploymentID, state.SnapshotTierInit, "capture-e2e"),
	})
	if err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	if opts.stale {
		if err := store.MarkSnapshotStale(ctx, snap.ID); err != nil {
			t.Fatalf("mark snapshot stale: %v", err)
		}
	}
	return snap
}

// waitForWake polls until cond holds. The state writes these tests assert on
// happen after the customer response is already on the wire (schedd commits
// them on the post-vmmd path), so a bare read races the wake.
func waitForWake(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error(msg)
}

func repeatChar(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}

// wakeNormalPathApp drives one customer request through the public route and
// returns once it succeeds, which is the point at which the wake has completed
// and the instance is serving.
func wakeNormalPathApp(t *testing.T, f *normalPathFixture) {
	t.Helper()
	f.vmmd.SetDefaultVersion("v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 30*time.Second)
}

// TestE2E_NormalPath_ParkedAppWakesByRestore pins the happy path that the
// product's entire latency story rests on: a parked app with a usable snapshot
// wakes through CreateFromSnapshot, not through a cold boot.
//
// Before this test the suite could not tell the two apart, so a regression
// that disabled restore entirely — turning every customer wake into a cold
// boot and blowing the p95 budget — would have shipped completely green.
func TestE2E_NormalPath_ParkedAppWakesByRestore(t *testing.T) {
	f := newNormalPathFixture(t, "wake-restore")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f.ctx, f.store, f.app.ID)
	seedNormalPathSnapshot(t, f.ctx, f.store, dep.ID, normalPathSnapshotOpts{})

	wakeNormalPathApp(t, f)

	restores := f.vmmd.RestoreCalls()
	if len(restores) == 0 {
		t.Fatalf("wake used no restore; cold boots=%d (a usable snapshot must restore)", len(f.vmmd.ColdBootCalls()))
	}
	if got := restores[0].GetSnapshot().GetDeploymentId(); got != dep.ID {
		t.Errorf("restore targeted deployment %q, want %q", got, dep.ID)
	}
	if got := restores[0].GetSnapshot().GetFcVersion(); got != normalPathFCVersion {
		t.Errorf("restore carried fc_version %q, want %q", got, normalPathFCVersion)
	}
	if n := len(f.vmmd.ColdBootCalls()); n != 0 {
		t.Errorf("cold boots=%d, want 0 when the snapshot is usable", n)
	}
}

// TestE2E_NormalPath_UnusableSnapshotColdBoots is ADR-005's invariant stated as
// a table: every way a snapshot stops being usable must still serve the
// request, via cold boot, without ever attempting a restore.
//
// The fc-version case is the one that matters operationally — a Firecracker
// upgrade invalidates the entire snapshot fleet at once, and if that path is
// broken every parked app on the platform fails its next wake simultaneously.
func TestE2E_NormalPath_UnusableSnapshotColdBoots(t *testing.T) {
	tests := []struct {
		name string
		slug string
		opts normalPathSnapshotOpts
	}{
		{
			name: "stale snapshot",
			slug: "wake-stale",
			opts: normalPathSnapshotOpts{stale: true},
		},
		{
			// The post-Firecracker-upgrade shape. imaged's FC sweep marks rows
			// stale lazily, so a wake can always race ahead of it and must
			// reject the row on version alone.
			name: "firecracker version skew",
			slug: "wake-fcskew",
			opts: normalPathSnapshotOpts{fcVersion: "0.0.1-other"},
		},
		{
			// A snapshot captured at a different plan RAM cannot be restored
			// into the admitted memory size.
			name: "ram shape skew",
			slug: "wake-ramskew",
			opts: normalPathSnapshotOpts{memBytes: int64(normalPathSnapshotRAMMB+128) << 20},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newNormalPathFixture(t, tc.slug)
			if f == nil {
				return
			}
			dep := createNormalPathParkedDeployment(t, f.ctx, f.store, f.app.ID)
			seedNormalPathSnapshot(t, f.ctx, f.store, dep.ID, tc.opts)

			wakeNormalPathApp(t, f)

			if n := len(f.vmmd.ColdBootCalls()); n == 0 {
				t.Fatalf("no cold boot; restores=%d (an unusable snapshot must cold boot)", len(f.vmmd.RestoreCalls()))
			}
			if n := len(f.vmmd.RestoreCalls()); n != 0 {
				t.Errorf("restores=%d, want 0 — an unusable snapshot must not be attempted", n)
			}
		})
	}
}

// TestE2E_NormalPath_DegradedRestoreServesAndRetiresSnapshot covers the case
// the compatibility check cannot predict: the row looks usable and the
// snapshot still will not load (corrupt blob, missing object, a memory shape
// vmmd rejects).
//
// This is where ADR-005's fallback actually lives — inside vmmd, which boots
// the rootfs and reports Method=WAKE_COLD_BOOT against a requested restore.
// Two things must hold. The customer's request still succeeds, because a
// snapshot that will not load is a latency event and not an outage. And the
// snapshot is retired, so the next wake does not pay for the same doomed
// attempt again.
func TestE2E_NormalPath_DegradedRestoreServesAndRetiresSnapshot(t *testing.T) {
	f := newNormalPathFixture(t, "wake-restore-degraded")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f.ctx, f.store, f.app.ID)
	seedNormalPathSnapshot(t, f.ctx, f.store, dep.ID, normalPathSnapshotOpts{})
	f.vmmd.DegradeRestoreToColdBoot()

	// Fails the test if the request never succeeds, which is the first half of
	// the contract: a snapshot that will not load must not cost the customer
	// their request.
	wakeNormalPathApp(t, f)

	if n := len(f.vmmd.RestoreCalls()); n == 0 {
		t.Fatal("restore was never attempted; the degraded path proves nothing")
	}

	// Assert the consequence rather than the column: LatestSnapshotForTier is
	// the reader the next wake uses, and it filters stale rows. If the bad
	// snapshot is still selectable, every subsequent wake repeats the failed
	// load before degrading again.
	waitForWake(t, 10*time.Second, func() bool {
		_, err := f.store.LatestSnapshotForTier(f.ctx, dep.ID, state.SnapshotTierInit)
		return errors.Is(err, state.ErrNotFound)
	}, "a snapshot that failed to load is still selectable for the next wake")
}

// TestE2E_NormalPath_RestoreRPCErrorFailsTheWake pins the boundary between the
// two restore failure shapes. A gRPC error from CreateFromSnapshot is NOT a
// cold-boot trigger: schedd releases the capacity reservation and marks the
// instance failed. Pinning it keeps someone from "fixing" the degraded path
// above by making every restore error silently cold boot, which would hide a
// broken vmmd behind a slow-but-green platform.
func TestE2E_NormalPath_RestoreRPCErrorFailsTheWake(t *testing.T) {
	f := newNormalPathFixture(t, "wake-restore-rpc-error")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f.ctx, f.store, f.app.ID)
	seedNormalPathSnapshot(t, f.ctx, f.store, dep.ID, normalPathSnapshotOpts{})
	f.vmmd.FailRestore(status.Error(codes.Internal, "fake vmmd: snapshot blob is corrupt"))
	f.vmmd.SetDefaultVersion("v1")

	// The request must not succeed: there is no healthy instance to serve it.
	_, _, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
	if statusCode == http.StatusOK {
		t.Fatal("request succeeded after a failed restore; schedd must not silently cold boot on an RPC error")
	}

	if n := len(f.vmmd.RestoreCalls()); n == 0 {
		t.Fatal("restore was never attempted")
	}
	if n := len(f.vmmd.ColdBootCalls()); n != 0 {
		t.Errorf("cold boots=%d, want 0 — an errored restore is terminal, not a fallback", n)
	}
}

// TestE2E_NormalPath_IdleParkWritesSnapshotAndReleasesInstance covers the park
// edge, which was equally unreachable: PauseAndSnapshot was Unimplemented, so
// no CI test had ever observed a park.
func TestE2E_NormalPath_IdleParkWritesSnapshotAndReleasesInstance(t *testing.T) {
	f := newNormalPathFixture(t, "park-writes-snapshot")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f.ctx, f.store, f.app.ID)
	seedNormalPathSnapshot(t, f.ctx, f.store, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	instance, err := f.store.RunningInstanceForApp(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("no running instance after wake: %v", err)
	}

	if err := parkNormalPathInstance(t, f, instance.ID); err != nil {
		t.Fatalf("park: %v", err)
	}

	if n := len(f.vmmd.SnapshotCalls()); n == 0 {
		t.Fatal("park did not reach PauseAndSnapshot")
	}
	if got := f.vmmd.SnapshotCalls()[0].GetInstance(); got != instance.ID {
		t.Errorf("snapshot captured instance %q, want %q", got, instance.ID)
	}
}

// parkNormalPathInstance asks schedd to park through the same durable path the
// idle reaper uses. Kept as a seam so the assertion above does not depend on
// which trigger fired the park.
func parkNormalPathInstance(t *testing.T, f *normalPathFixture, instanceID string) error {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+f.app.Slug+"/park", nil)
	if statusCode != http.StatusAccepted && statusCode != http.StatusOK && statusCode != http.StatusNoContent {
		return fmt.Errorf("park request status=%d body=%s", statusCode, body)
	}
	for time.Now().Before(deadline) {
		ins, err := f.store.InstanceByID(f.ctx, instanceID)
		if err == nil && ins.State != string(state.StateRunning) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("instance %s never left running", instanceID)
}
