package sched_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowScheduleStopsAdmissionWhenKillSwitchIsOff(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "schedule-kill-switch@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "schedule-kill-switch", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","path":"/report"}]}]`)
	if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: definition}); err != nil {
		t.Fatal(err)
	}
	engine, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	key, ok := platformflags.CapabilityFlag("workflows")
	if !ok {
		t.Fatal("workflow capability flag missing")
	}
	flags := platformflags.NewSet(map[string]platformflags.Definition{key: {Enabled: false}})
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithCapabilities(decider)
	for range 3 {
		now = now.Add(time.Minute)
		if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	runs, _, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("disabled workflows admitted %d runs: %v", len(runs), err)
	}
	engine.WithCapabilities(nil)
	if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _, err = store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("re-enabled workflows admitted %d runs, want one current occurrence: %v", len(runs), err)
	}
}

func TestTenantScheduleDeniedPageDoesNotStarveAnotherAccount(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	var apps []state.App
	for i, name := range []string{"denied-tenant-page", "allowed-tenant-page"} {
		account, err := store.CreateAccount(ctx, name+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), AccountID: account.ID, Slug: name, Type: state.AppTypeApp, RAMMB: 256, PlatformTenantRequired: true})
		if err != nil {
			t.Fatal(err)
		}
		definition := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","path":"/report"}]}]`)
		if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: definition}); err != nil {
			t.Fatal(err)
		}
		count := 1
		if i == 0 {
			count = api.WorkflowScheduleBatch
		}
		for n := range count {
			ref := fmt.Sprintf("customer-%d", n)
			tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, ref, ref, api.WorkflowScheduleBatch+1)
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, ref, ref)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
				t.Fatal(err)
			}
		}
		apps = append(apps, app)
	}
	key, ok := platformflags.CapabilityFlag("workflows")
	if !ok {
		t.Fatal("workflow capability flag missing")
	}
	denied := false
	flags := platformflags.NewSet(map[string]platformflags.Definition{key: {
		Enabled: true, Default: true, Rules: []platformflags.Rule{{ID: "deny", Accounts: []string{apps[0].AccountID}, Value: &denied}},
	}})
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	engine, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	engine.WithCapabilities(decider)
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	for range 2 {
		if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Minute)
	for range 2 {
		if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i, app := range apps {
		runs, _, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != i {
			t.Fatalf("app %s: got %d runs, want %d: %v", app.Slug, len(runs), i, err)
		}
	}
}

func TestAppWorkflowScheduleSkipsDeniedAccountWithoutBlockingOthers(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	var apps []state.App
	for _, name := range []string{"denied-schedule", "allowed-schedule"} {
		account, err := store.CreateAccount(ctx, name+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: name, Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		definition := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","path":"/report"}]}]`)
		if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: definition}); err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
	}
	engine, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	key, ok := platformflags.CapabilityFlag("workflows")
	if !ok {
		t.Fatal("workflow capability flag missing")
	}
	denied := false
	flags := platformflags.NewSet(map[string]platformflags.Definition{key: {
		Enabled: true, Default: true, Rules: []platformflags.Rule{{ID: "deny", Accounts: []string{apps[0].AccountID}, Value: &denied}},
	}})
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithCapabilities(decider)
	now = now.Add(time.Minute)
	if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	for i, app := range apps {
		runs, _, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != i {
			t.Fatalf("app %s: got %d runs, want %d: %v", app.Slug, len(runs), i, err)
		}
	}
	engine.WithCapabilities(nil)
	now = now.Add(time.Minute)
	if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _, err := store.ListWorkflowRuns(ctx, apps[0].ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("re-enabled account admitted %d runs, want one current occurrence: %v", len(runs), err)
	}
}
