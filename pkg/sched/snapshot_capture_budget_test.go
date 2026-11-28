package sched_test

// adr: 005
// Production-us 2026-11-27: a Rails app with Thruster in front of Puma, a
// 770 MB Ruby image and the 1.2 GB Umami image passed their health checks
// within seconds, then every prime attempt ran out of the 45 s snapshot
// capture budget while vmmd uploaded the app layer's private clone, and the
// deployments failed as if the cold boot had timed out.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// The measured captures: a 555 MB layer took 44.7 s at 512 MB (24 s of it
// publishing the drive), and a 302 MB layer took up to 49.1 s at 768 MB.
func TestSnapshotCaptureBudgetCountsTheAppLayer(t *testing.T) {
	for _, tc := range []struct {
		ramMB    int
		layerMB  int64
		measured time.Duration
	}{
		{512, 555, 44729 * time.Millisecond},
		{768, 302, 49103 * time.Millisecond},
		{1024, 361, 45709 * time.Millisecond},
	} {
		got := sched.SnapshotCaptureBudget(tc.ramMB, tc.layerMB<<20)
		if got <= tc.measured+10*time.Second {
			t.Errorf("SnapshotCaptureBudget(%d MB, %d MB layer) = %s, want headroom over the measured %s", tc.ramMB, tc.layerMB, got, tc.measured)
		}
		if got <= sched.SnapshotBudgetFor(tc.ramMB) {
			t.Errorf("SnapshotCaptureBudget(%d MB, %d MB layer) = %s, want more than the memory-only %s", tc.ramMB, tc.layerMB, got, sched.SnapshotBudgetFor(tc.ramMB))
		}
	}
	for _, layer := range []int64{0, -1} {
		if got, want := sched.SnapshotCaptureBudget(512, layer), sched.SnapshotBudgetFor(512); got != want {
			t.Errorf("SnapshotCaptureBudget(512, %d) = %s, want the memory-only %s for an unknown layer", layer, got, want)
		}
	}
}

func TestPrimeCaptureDeadlineCoversTheAppLayer(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 2)
	const layer = int64(555) << 20
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "", "", layer); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	if err := engine.Prime(ctx, app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	deadline, ok := vmm.SnapDeadline()
	if !ok {
		t.Fatal("PauseAndSnapshot carried no deadline")
	}
	budget := time.Until(deadline)
	if want := sched.SnapshotCaptureBudget(512, layer); budget > want || budget <= sched.SnapshotBudgetFor(512)+time.Second {
		t.Fatalf("capture deadline = %s, want the layer-inclusive %s, not the memory-only %s", budget, want, sched.SnapshotBudgetFor(512))
	}
}

// Both prime attempts' captures time out: the app booted healthy and cold
// boots fine, so the deployment activates without a snapshot instead of
// failing (ADR-005, as a non-transient capture failure already does).
func TestFinalPrimeAttemptCaptureTimeoutActivatesWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanHobby, 512, 2)
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	timeout := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
	*vmm.SnapErrSequence() = []error{timeout, timeout}
	notifier := &sched.FakeNotifier{}
	engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
	loop := sched.NewLoop(nil, engine, sched.TestLog())
	loop.XHandleNotification(ctx, db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	loop.WaitPrimes()

	if *vmm.ColdBoots() != 2 {
		t.Fatalf("cold boots = %d, want 2 (the transient capture failure keeps its retry)", *vmm.ColdBoots())
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == state.DeployFailed {
		t.Fatalf("deployment failed (%s: %s) although its app booted healthy twice", got.ErrorCode, got.Error)
	}
	raw, ok := notifier.LastPayload(db.NotifyDeploymentReady)
	if !ok {
		t.Fatal("no deployment_ready handoff after the last capture timed out")
	}
	var ready struct {
		DeploymentID     string `json:"deployment_id"`
		NoSnapshotReason string `json:"no_snapshot_reason"`
	}
	if err := json.Unmarshal([]byte(raw), &ready); err != nil || ready.DeploymentID != dep.ID || ready.NoSnapshotReason != state.SnapshotPrimeCaptureFailedReason {
		t.Fatalf("deployment_ready = %s, %v", raw, err)
	}
}

// A deployment that still fails on a capture timeout (a managed cache's
// handoff) names the capture, not the cold boot that had succeeded.
func TestMarkPrimeFailedNamesTheSnapshotCaptureBudget(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, _, dep := sched.SeedApp(t, store, api.PlanPro, 512, 2)
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	cause := sched.NewSnapshotCaptureTimeoutError(77*time.Second, status.Error(codes.DeadlineExceeded, "context deadline exceeded"))
	e.MarkPrimeFailed(ctx, dep.ID, errors.Join(errors.New("sched: park: snapshot"), cause))

	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployFailed || got.ErrorCode != api.CodeStageSnapshotPrepareTimeout {
		t.Fatalf("deployment = %s/%s, want failed/%s", got.Status, got.ErrorCode, api.CodeStageSnapshotPrepareTimeout)
	}
	if !strings.Contains(got.Error, "startup_phase=snapshot_capture") || !strings.Contains(got.Error, "1m17s capture budget") || strings.Contains(got.Error, "cold-boot") {
		t.Fatalf("error = %q, want the snapshot capture phase and its 1m17s budget", got.Error)
	}
	if !strings.Contains(got.ErrorWhy, "passed its health check") || !strings.Contains(got.ErrorFix, "does not help") {
		t.Fatalf("why = %q, fix = %q; want capture guidance, not startup_deadline_s advice", got.ErrorWhy, got.ErrorFix)
	}
}

func TestCaptureBudgetExpired(t *testing.T) {
	live := context.Background()
	expired, cancel := context.WithTimeout(live, -time.Second)
	defer cancel()
	stopped, stop := context.WithCancel(live)
	stop()
	stoppedCapture, cancelStopped := context.WithTimeout(stopped, -time.Second)
	defer cancelStopped()
	failure := errors.New("rpc error: code = DeadlineExceeded")
	for _, tc := range []struct {
		name         string
		ctx, capture context.Context
		err          error
		want         bool
	}{
		{"capture budget ran out", live, expired, failure, true},
		{"capture succeeded", live, expired, nil, false},
		{"capture failed early", live, live, failure, false},
		{"daemon shutting down", stopped, stoppedCapture, failure, false},
	} {
		if got := sched.CaptureBudgetExpired(tc.ctx, tc.capture, tc.err); got != tc.want {
			t.Errorf("%s: captureBudgetExpired = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The watchdog spares a capture its layer-inclusive budget still allows,
// and stops it once that budget is gone.
func TestWatchdogSparesCaptureWithinItsLayerBudget(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 2)
	const layer = int64(555) << 20
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "", "", layer); err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	inBudget, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateSnapshotting), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	pastBudget, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateSnapshotting), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store.SetParkedAtForTest(inBudget.ID, now.Add(-(sched.SnapshotBudgetFor(512) + 5*time.Second)))
	store.SetParkedAtForTest(pastBudget.ID, now.Add(-(sched.SnapshotCaptureBudget(512, layer) + 5*time.Second)))

	sched.NewWatchdog(store, engine, slog.Default()).WithClock(func() time.Time { return now }).SweepRuns(ctx)

	for id, want := range map[string]state.State{inBudget.ID: state.StateSnapshotting, pastBudget.ID: state.StateStopped} {
		row, err := store.InstanceByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if row.State != string(want) {
			t.Errorf("instance parked %s ago = %s, want %s", now.Sub(row.ParkedAt).Round(time.Second), row.State, want)
		}
	}
}
