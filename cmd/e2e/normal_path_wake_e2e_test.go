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
	"net/http"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// createNormalPathParkedDeployment publishes a live deployment with a real,
// signed rootfs layer and no running instance — what a parked app looks like:
// routable and cold-bootable, but the next request has to wake it.
//
// The layer is what makes this a *parked* app rather than a broken one.
// Invariant §6.2-3 is that an app always has a live snapshot OR a cold-bootable
// rootfs; a deployment row with neither is a state production never reaches,
// and a wake against it fails before it ever chooses a boot edge.
func createNormalPathParkedDeployment(t *testing.T, f *normalPathFixture) state.Deployment {
	t.Helper()
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       f.app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + repeatChar("3", 64),
	})
	if err != nil {
		t.Fatalf("create parked deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark parked deployment live: %v", err)
	}

	publishNormalPathLayer(t, f, dep.ID)
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

func seedNormalPathSnapshot(t *testing.T, f *normalPathFixture, deploymentID string, opts normalPathSnapshotOpts) state.Snapshot {
	t.Helper()
	ctx, store := f.ctx, f.store
	fcVersion := opts.fcVersion
	if fcVersion == "" {
		fcVersion = e2etest.FakeFCVersion
	}
	memBytes := opts.memBytes
	if memBytes == 0 {
		memBytes = int64(e2etest.FakeSnapshotRAMMB) << 20
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
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})

	wakeNormalPathApp(t, f)

	restores := f.vmmd.RestoreCalls()
	if len(restores) == 0 {
		t.Fatalf("wake used no restore; cold boots=%d (a usable snapshot must restore)", len(f.vmmd.ColdBootCalls()))
	}
	if got := restores[0].GetSnapshot().GetDeploymentId(); got != dep.ID {
		t.Errorf("restore targeted deployment %q, want %q", got, dep.ID)
	}
	if got := restores[0].GetSnapshot().GetFcVersion(); got != e2etest.FakeFCVersion {
		t.Errorf("restore carried fc_version %q, want %q", got, e2etest.FakeFCVersion)
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
			opts: normalPathSnapshotOpts{memBytes: int64(e2etest.FakeSnapshotRAMMB+128) << 20},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newNormalPathFixture(t, tc.slug)
			if f == nil {
				return
			}
			dep := createNormalPathParkedDeployment(t, f)
			seedNormalPathSnapshot(t, f, dep.ID, tc.opts)

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
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
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
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
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

// TestE2E_NormalPath_ParkReleasesInstanceByTheRightPath covers the park edge,
// which was unreachable until now: PauseAndSnapshot was Unimplemented, so no
// CI test had ever observed a park reach the VM boundary at all.
//
// schedd has TWO correct parks and picks between them on the instance's state
// (Engine.Park), which nothing in the spec prose distinguishes at the call
// site:
//
//   - RUNNING -> capture, then release. The resident guest holds the only copy
//     of its current memory, so it must be written down before the VM dies.
//   - WARM    -> release, no capture. A warm instance is a paused restore of a
//     snapshot that is still on disk; capturing it again would write a second
//     copy of something already durable.
//
// The test asserts whichever contract applies to the state the instance is
// actually in rather than forcing one. Pinning only the RUNNING path made this
// flaky and, worse, would have read as "park is broken" when schedd was doing
// exactly the right thing — the first version of this test failed that way.
//
// The invariant underneath both is the one that matters: after a park the
// instance is no longer resident (§6.2-4) and the app is still wakeable
// (§6.2-3).
//
// It drives schedd's ParkInstance RPC — the entry point meterd's quota path
// and the operator tooling use — not POST /v1/apps/{slug}/park, which marks
// the app evicted_cold. That endpoint is a COLD eviction: it tears the VM down
// deliberately without capturing, so asserting a snapshot against it would be
// asserting the opposite of what it promises.
func TestE2E_NormalPath_ParkReleasesInstanceByTheRightPath(t *testing.T) {
	f := newNormalPathFixture(t, "park-releases-instance")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	instance, err := f.store.RunningInstanceForApp(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("no running instance after wake: %v", err)
	}

	// Read the state schedd will branch on, as late as possible before the
	// call. A change between this read and the RPC only costs a clear failure
	// message, not a wrong assertion.
	before, err := f.store.InstanceByID(f.ctx, instance.ID)
	if err != nil {
		t.Fatalf("read instance before park: %v", err)
	}

	conn, err := grpc.NewClient("unix://"+f.h.ScheddSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial schedd: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	if _, err := scheddpb.NewScheddClient(conn).ParkInstance(ctx,
		&scheddpb.ParkInstanceRequest{InstanceId: instance.ID, Reason: "e2e_park"}); err != nil {
		t.Fatalf("park instance: %v", err)
	}

	captured := func() bool {
		for _, call := range f.vmmd.SnapshotCalls() {
			if call.GetInstance() == instance.ID {
				return true
			}
		}
		return false
	}
	released := func() bool {
		for _, id := range f.vmmd.DestroyCalls() {
			if id == instance.ID {
				return true
			}
		}
		return false
	}

	switch before.State {
	case string(state.StateRunning):
		waitForWake(t, 20*time.Second, captured,
			"a RUNNING instance was parked without capturing its memory first")
	case string(state.StateWarm):
		waitForWake(t, 20*time.Second, released,
			"a WARM instance was parked without releasing its VM")
		if captured() {
			t.Error("a WARM instance was captured on park; its snapshot is already on disk")
		}
	default:
		t.Fatalf("instance was %q before park, which Engine.Park ignores entirely (it acts only on running or warm)", before.State)
	}

	// §6.2-4: a parked app holds zero resident RAM, whichever path ran.
	waitForWake(t, 10*time.Second, func() bool {
		ins, err := f.store.InstanceByID(f.ctx, instance.ID)
		if err != nil {
			return false
		}
		return ins.State != string(state.StateRunning) && ins.State != string(state.StateWarm)
	}, "instance stayed resident after a successful park")

	// §6.2-3: the app must still be wakeable. The seeded snapshot is the
	// cheapest proof that a park never leaves an app stranded.
	if _, err := f.store.LatestSnapshotForTier(f.ctx, dep.ID, state.SnapshotTierInit); err != nil {
		t.Errorf("app has no usable snapshot after park: %v", err)
	}
}
