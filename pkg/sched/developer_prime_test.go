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

func seedDeveloperPrime(t *testing.T, previewOf string, maxConc int, reason string) (*state.MemStore, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "dev@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "dev-web-0123456789ab", PreviewOfSlug: previewOf,
		RAMMB: 1024, MaxConcurrency: maxConc, IdleTimeoutS: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeploySnapshotting, Reason: reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, app, dep
}

// adr: 20261122200000 — with dev_prime_serves_enabled, a developer
// environment's prime is promoted while RUNNING: no capture, no
// snapshot_written, and a deployment_ready proof naming the prime.
func TestDeveloperPrimeServesWithoutSnapshot(t *testing.T) {
	store, app, dep := seedDeveloperPrime(t, "web", 1, "")
	vmm := &sched.FakeVMM{}
	notifier := &sched.FakeNotifier{}
	engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
	engine.SetDevPrimeServes(true)

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if *vmm.ColdBoots() != 1 || *vmm.Snapshots() != 0 {
		t.Fatalf("prime calls: cold=%d snapshot=%d; want 1/0", *vmm.ColdBoots(), *vmm.Snapshots())
	}
	if notifier.Count(db.NotifySnapshotWritten) != 0 {
		t.Fatal("developer prime published a snapshot")
	}
	raw, ok := notifier.LastPayload(db.NotifyDeploymentReady)
	if !ok {
		t.Fatal("no deployment_ready for the developer prime")
	}
	var ready struct {
		DeploymentID     string `json:"deployment_id"`
		ExecutionMode    string `json:"execution_mode"`
		InstanceID       string `json:"instance_id"`
		NoSnapshotReason string `json:"no_snapshot_reason"`
	}
	if err := json.Unmarshal([]byte(raw), &ready); err != nil || ready.DeploymentID != dep.ID ||
		ready.ExecutionMode != api.ExecutionModeRequest || ready.NoSnapshotReason != state.DeveloperPrimeActivationReason {
		t.Fatalf("deployment_ready = %s, %v", raw, err)
	}
	ins, err := store.InstanceByID(context.Background(), ready.InstanceID)
	if err != nil || ins.State != string(state.StateRunning) || ins.DeploymentID != dep.ID {
		t.Fatalf("prime instance = %+v, %v; want the RUNNING prime", ins, err)
	}
}

// The ordinary snapshot prime stays in force when the setting is off, for
// apps that are not developer environments, and for environment-branch
// deployments.
func TestDeveloperPrimeKeepsSnapshotPrime(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enabled   bool
		previewOf string
		reason    string
	}{
		{name: "setting off", enabled: false, previewOf: "web"},
		{name: "production app", enabled: true},
		{name: "environment branch seed", enabled: true, previewOf: "web", reason: state.EnvironmentBranchSeedReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, app, dep := seedDeveloperPrime(t, tc.previewOf, 1, tc.reason)
			vmm := &sched.FakeVMM{}
			notifier := &sched.FakeNotifier{}
			engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
			engine.SetDevPrimeServes(tc.enabled)
			if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			if *vmm.Snapshots() != 1 || notifier.Count(db.NotifySnapshotWritten) != 1 {
				t.Fatalf("snapshot=%d written=%d; want the ordinary capture", *vmm.Snapshots(), notifier.Count(db.NotifySnapshotWritten))
			}
			if raw, ok := notifier.LastPayload(db.NotifyDeploymentReady); ok {
				t.Fatalf("unexpected deployment_ready %s", raw)
			}
		})
	}
}

// Invariant 1: a prime serves only when the app's serving gate has a slot
// for its deployment, the ADR-199 rollout grant included. A prime next to
// one old instance at max_concurrency 1 serves through the grant; with the
// grant also taken, it captures and parks as usual.
func TestDeveloperPrimeParksWithoutServingRoom(t *testing.T) {
	for _, tc := range []struct {
		name      string
		oldWakes  int
		wantServe bool
	}{
		{name: "grant free", oldWakes: 1, wantServe: true},
		{name: "grant taken", oldWakes: 2, wantServe: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, app, dep := seedDeveloperPrime(t, "web", 1, "")
			vmm := &sched.FakeVMM{}
			notifier := &sched.FakeNotifier{}
			engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
			engine.SetDevPrimeServes(true)
			for i := 0; i < tc.oldWakes; i++ {
				old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:old", Status: state.DeployLive})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := engine.Wake(ctx, app.ID, old.ID, "", sched.TriggerGateway); err != nil {
					t.Fatalf("wake old deployment %d: %v", i, err)
				}
			}
			if err := engine.Prime(ctx, app.ID, dep.ID); err != nil {
				t.Fatalf("Prime: %v", err)
			}
			_, served := notifier.LastPayload(db.NotifyDeploymentReady)
			written := notifier.Count(db.NotifySnapshotWritten)
			if served != tc.wantServe || (written == 1) == tc.wantServe {
				t.Fatalf("served=%v snapshot_written=%d; want served=%v", served, written, tc.wantServe)
			}
		})
	}
}
