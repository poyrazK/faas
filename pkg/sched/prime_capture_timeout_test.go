package sched_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 005
// Prod hunt r71: uc1127-umami booted healthy on both prime attempts, but each
// init snapshot capture hit its 45 s budget, and the release failed. A
// transient capture failure on the last attempt activates the deployment
// without a snapshot, as any other platform capture failure does (P13).
func TestPrimeCaptureTimeoutOnLastAttemptActivatesWithoutSnapshot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanHobby, 256, 2)
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	*vmm.SnapErr() = context.DeadlineExceeded
	notifier := &sched.FakeNotifier{}
	engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
	loop := sched.NewLoop(nil, engine, sched.TestLog())
	loop.XHandleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	loop.WaitPrimes()

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == state.DeployFailed {
		t.Fatalf("deployment failed (%s: %s) although it booted healthy", got.ErrorCode, got.Error)
	}
	raw, ok := notifier.LastPayload(db.NotifyDeploymentReady)
	if !ok {
		t.Fatal("no deployment_ready handoff after the last capture timed out")
	}
	var ready struct {
		NoSnapshotReason string `json:"no_snapshot_reason"`
	}
	if err := json.Unmarshal([]byte(raw), &ready); err != nil || ready.NoSnapshotReason != state.SnapshotPrimeCaptureFailedReason {
		t.Fatalf("deployment_ready = %s, %v", raw, err)
	}
}
