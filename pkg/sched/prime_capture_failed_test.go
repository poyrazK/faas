package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 005
// TestPrimeCaptureFailureActivatesWithoutSnapshot pins deploy-path review
// P13: a deployment whose prime booted healthy but whose snapshot capture
// failed on the platform side is handed to imaged as ready without a
// snapshot, instead of failing a release that cold boots fine.
func TestPrimeCaptureFailureActivatesWithoutSnapshot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanHobby, 256, 2)
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	*vmm.SnapErr() = errors.New("snapshot write: no space left on device")
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
		t.Fatalf("deployment failed (%s) although it booted healthy", got.ErrorCode)
	}
	raw, ok := notifier.LastPayload(db.NotifyDeploymentReady)
	if !ok {
		t.Fatal("no deployment_ready handoff for the failed capture")
	}
	var ready struct {
		DeploymentID     string `json:"deployment_id"`
		InstanceID       string `json:"instance_id"`
		NoSnapshotReason string `json:"no_snapshot_reason"`
	}
	if err := json.Unmarshal([]byte(raw), &ready); err != nil || ready.DeploymentID != dep.ID || ready.NoSnapshotReason != state.SnapshotPrimeCaptureFailedReason {
		t.Fatalf("deployment_ready = %s, %v", raw, err)
	}
	ins, err := store.InstanceByID(context.Background(), ready.InstanceID)
	if err != nil || ins.State != string(state.StateStopped) {
		t.Fatalf("instance = %+v, %v; want STOPPED so imaged's proof holds", ins, err)
	}
}

// adr: 20261116192126
// TestManagedCacheHandoffPrimeCaptureFailureDoesNotActivate pins the
// exception to P13: a managed cache's handoff deployment holds the copied
// data only in its prime VM, and a cold boot of it re-runs the copy from the
// cache's own address and never serves. So a failed prime capture must not
// hand it to imaged as ready; the rollout aborts and the running cache keeps
// serving (native soak redissoak-20261121T120457).
func TestManagedCacheHandoffPrimeCaptureFailureDoesNotActivate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanHobby, 256, 2)
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		ID: "dep-handoff", AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeploySnapshotting,
		ImageDigest:        api.ManagedValkeyImage,
		OverrideEntrypoint: api.ManagedValkeyHandoffCommand("10.200.0.7", 128, "allkeys-lru", 1<<40),
		OverridePort:       api.ManagedValkeyPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	*vmm.SnapErr() = errors.New("prepare local snapshot path: stat /srv/fc/snap: no such file or directory")
	notifier := &sched.FakeNotifier{}
	engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
	loop := sched.NewLoop(nil, engine, sched.TestLog())
	loop.XHandleNotification(ctx, db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	loop.WaitPrimes()

	if raw, ok := notifier.LastPayload(db.NotifyDeploymentReady); ok {
		t.Fatalf("handoff went live without its snapshot: deployment_ready %s", raw)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == state.DeployLive {
		t.Fatalf("handoff deployment is live without a snapshot: %+v", got)
	}
}

func TestIsManagedValkeyHandoffCommand(t *testing.T) {
	if !api.IsManagedValkeyHandoffCommand(api.ManagedValkeyHandoffCommand("10.200.0.7", 128, "allkeys-lru", 1)) {
		t.Fatal("the handoff command is not recognised")
	}
	if api.IsManagedValkeyHandoffCommand([]string{"valkey-server", "--port", "6379"}) || api.IsManagedValkeyHandoffCommand(nil) {
		t.Fatal("a plain cache command was taken for a handoff")
	}
}
