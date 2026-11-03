package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// requestParentCapture seeds a branch whose parent has a managed Postgres
// secret and queues the parent's credential-free capture.
func requestParentCapture(t *testing.T, store *state.MemStore) (state.App, string) {
	t.Helper()
	ctx := context.Background()
	app, dep := seedEnvironmentBranch(t, store, api.ExecutionModeRequest, true)
	parent, err := store.EnvironmentBranchLineageParent(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestEnvironmentBranchCapture(ctx, parent); err != nil {
		t.Fatal(err)
	}
	return app, parent
}

// ADR-20261031193635 slice 5a: the worker boots the parent quarantined with
// placeholder managed secrets, captures it outside the snapshots table and
// retires the VM.
func TestEnvironmentBranchCaptureWorkerPublishesQuarantinedCapture(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app, parent := requestParentCapture(t, store)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

	sched.NewEnvironmentBranchCaptureWorker(store, engine, 0, nil).Tick(ctx)

	capture, err := store.EnvironmentBranchCapture(ctx, parent)
	if err != nil || capture.State != state.EnvironmentBranchCaptureReady {
		t.Fatalf("capture = %+v, %v; want ready", capture, err)
	}
	if !strings.HasPrefix(capture.StorageKey, "snap/"+parent+"/") || !state.IsSnapshotCaptureKey(capture.StorageKey) || capture.FCVersion != "1.10.0" {
		t.Fatalf("capture artifact = %+v", capture.EnvironmentBranchCaptureArtifact)
	}
	if *vmm.ColdBoots() != 1 || *vmm.WarmSnapshots() != 1 || *vmm.Snapshots() != 0 || *vmm.Destroys() != 1 {
		t.Fatalf("vm work: cold=%d warm=%d snapshot=%d destroy=%d", *vmm.ColdBoots(), *vmm.WarmSnapshots(), *vmm.Snapshots(), *vmm.Destroys())
	}
	spec := *vmm.LastColdBootSpec()
	if !spec.Quarantine {
		t.Fatal("the capture boot was not quarantined")
	}
	for _, entry := range spec.SealedEnv {
		if entry.Key == "DATABASE_URL" {
			t.Fatal("the capture boot received the sealed managed Postgres secret")
		}
	}
	env := map[string]string{}
	for _, entry := range spec.APIEnv {
		env[entry.Key] = entry.Value
	}
	if !strings.Contains(env["DATABASE_URL"], state.CredentialFreeHost) || env["GREGALE_ENVIRONMENT_CAPTURE"] != state.EnvironmentBranchCaptureEnvironment {
		t.Fatalf("capture env = %v", env)
	}
	if _, err := store.LatestSnapshot(ctx, parent); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the capture reached the snapshots table: %v", err)
	}
	instances, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances = %d, %v", len(instances), err)
	}
	if ins := instances[0]; !state.IsFork(ins.Mode) || ins.State != string(state.StateStopped) || ins.ID != capture.InstanceID {
		t.Fatalf("capture instance = %+v; want a stopped fork-mode row", ins)
	}
}

// A failed attempt is retried on a later tick; after the attempt cap the
// capture stays failed and its branches keep cold-booting.
func TestEnvironmentBranchCaptureWorkerFailsAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, parent := requestParentCapture(t, store)
	vmm := &sched.FakeVMM{}
	*vmm.WakeErr() = errors.New("boot failed")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	worker := sched.NewEnvironmentBranchCaptureWorker(store, engine, 0, nil)

	for attempt := 1; attempt <= api.EnvironmentBranchCaptureMaxAttempts; attempt++ {
		worker.Tick(ctx)
		capture, err := store.EnvironmentBranchCapture(ctx, parent)
		if err != nil || capture.Attempts != attempt {
			t.Fatalf("attempt %d: capture = %+v, %v", attempt, capture, err)
		}
		wantState := state.EnvironmentBranchCaptureRequested
		if attempt == api.EnvironmentBranchCaptureMaxAttempts {
			wantState = state.EnvironmentBranchCaptureFailed
		}
		if capture.State != wantState || !strings.Contains(capture.Failure, "boot failed") {
			t.Fatalf("attempt %d: state %q failure %q; want %q", attempt, capture.State, capture.Failure, wantState)
		}
	}
	if *vmm.WarmSnapshots() != 0 {
		t.Fatal("a failed boot was captured")
	}
}

// A capture VM a stopped schedd left behind is retired by the next attempt.
func TestEnvironmentBranchCaptureWorkerRetiresPreviousAttempt(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app, parent := requestParentCapture(t, store)
	lease := uuid.NewString()
	capture, err := store.ClaimEnvironmentBranchCapture(ctx, lease, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	leaked, err := store.CreateInstanceWithMode(ctx, app.ID, parent, string(state.StateRunning), app.RAMMB, "", uuid.NewString(), string(state.InstanceModeFork))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordEnvironmentBranchCaptureInstance(ctx, capture.ID, lease, leaked.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.FailEnvironmentBranchCapture(ctx, capture.ID, lease, "schedd stopped", false); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

	sched.NewEnvironmentBranchCaptureWorker(store, engine, 0, nil).Tick(ctx)

	if ins, err := store.InstanceByID(ctx, leaked.ID); err != nil || ins.State != string(state.StateStopped) {
		t.Fatalf("leaked capture VM = %+v, %v; want stopped", ins, err)
	}
	if *vmm.Destroys() != 2 {
		t.Fatalf("destroys = %d; want the leaked VM and this attempt's", *vmm.Destroys())
	}
	if capture, err := store.EnvironmentBranchCapture(ctx, parent); err != nil || capture.State != state.EnvironmentBranchCaptureReady {
		t.Fatalf("capture = %+v, %v", capture, err)
	}
}
