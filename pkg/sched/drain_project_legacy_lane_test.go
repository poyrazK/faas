package sched_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// `gregale deploy --project` without a named environment leaves a project
// app's deployments in the legacy default scope, which the gateway serves as
// production. Invoke, cron and delayed-task work captured for production must
// wake that same deployment instead of failing with "no live deployment".
func TestDrain_ProjectInvocationWakesLegacyProductionLane(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "legacy-lane@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "legacy-lane"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "legacy-lane-api", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	// apid's invoke handler prepares ordinary work through the ingress resolver.
	prepared, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, state.Invocation{AppID: app.ID, AccountID: account.ID,
		Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/work", Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(-time.Second)}, "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	vmm, notifier, synth := &sched.FakeVMM{}, &sched.FakeNotifier{}, &sched.DrainSynth{}
	engine := sched.XNewEngine(t, store, vmm, notifier, "1.10.0")
	drain := sched.NewDrain(store, engine, sched.WithDrainGatewaySynth(synth))
	drain.Tick(ctx)
	completed, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || completed.State != state.InvocationCompleted || completed.InstanceID == "" || (*synth.Calls()).Load() != 1 {
		t.Fatalf("legacy-lane dispatch = %+v, calls=%d, %v", completed, (*synth.Calls()).Load(), err)
	}
	instance, err := store.InstanceByID(ctx, completed.InstanceID)
	if err != nil || instance.DeploymentID != dep.ID {
		t.Fatalf("legacy-lane invocation woke %+v, want deployment %s: %v", instance, dep.ID, err)
	}
}
