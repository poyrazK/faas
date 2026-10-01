// adr: 375
package state_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type warmPoolCandidateStore interface {
	state.Store
	state.ProjectEnvironmentWorkloadSpecStore
	state.WarmPoolReconciliationStore
}

func TestMemWarmPoolReconciliationCandidates(t *testing.T) {
	testWarmPoolReconciliationCandidates(t, state.NewMemStore(), state.DefaultLocalNodeName)
}

func testWarmPoolReconciliationCandidates(t *testing.T, store warmPoolCandidateStore, nodeID string) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "pool-candidates-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "pool-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	createApp := func(slug string, pool int) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, WorkloadName: slug, RAMMB: 256, MaxConcurrency: 4, WarmPoolSize: pool, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	pin := func(app state.App, scope string, pool int) state.Deployment {
		settings, err := state.WorkloadSettingsFromApp(app)
		if err != nil {
			t.Fatal(err)
		}
		settings.WarmPoolSize = pool
		if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, scope, app.ID, 0, settings); err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	raw := createApp("raw-pool", 1)
	pinned := createApp("pinned-pool", 0)
	pinnedDep := pin(pinned, "production", 1)
	// The desired head is zero, but the running release still pins one.
	settings, err := state.WorkloadSettingsFromApp(pinned)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", pinned.ID, 1, settings); err != nil {
		t.Fatal(err)
	}
	stageOnly := createApp("stage-pool", 0)
	stageDep := pin(stageOnly, "stage", 2)
	stageHead, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", stageOnly.ID)
	if err != nil {
		t.Fatal(err)
	}
	stageSettings := stageHead.Settings
	stageSettings.WarmPoolSize = 0
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", stageOnly.ID, stageHead.Revision, stageSettings); err != nil {
		t.Fatal(err)
	}
	cold := createApp("cold-pool", 0)
	deleted := createApp("deleted-pool", 1)
	if err := store.DeleteApp(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	assertCandidates := func(node string, present, absent []state.App) {
		t.Helper()
		ids, err := store.WarmPoolReconciliationAppIDs(ctx, node)
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range present {
			if !slices.Contains(ids, app.ID) {
				t.Fatalf("missing pool candidate %s from %v", app.Slug, ids)
			}
		}
		for _, app := range absent {
			if slices.Contains(ids, app.ID) {
				t.Fatalf("unexpected pool candidate %s in %v", app.Slug, ids)
			}
		}
	}
	assertCandidates("", []state.App{raw, pinned, stageOnly}, []state.App{cold, deleted})
	assertCandidates(uuid.NewString(), nil, []state.App{raw, pinned, stageOnly, cold, deleted})
	if err := store.MarkDeploymentSuperseded(ctx, stageDep.ID); err != nil {
		t.Fatal(err)
	}
	assertCandidates("", []state.App{raw, pinned}, []state.App{stageOnly, cold, deleted})
	warm, err := store.CreateInstanceWithMode(ctx, stageOnly.ID, stageDep.ID, string(state.StateWarm), 256, nodeID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	assertCandidates("", []state.App{raw, pinned, stageOnly}, []state.App{cold, deleted})
	for _, app := range []state.App{raw, pinned} {
		if err := store.SetAppNodeID(ctx, app.ID, warm.NodeID); err != nil {
			t.Fatal(err)
		}
	}
	assertCandidates(warm.NodeID, []state.App{raw, pinned}, []state.App{stageOnly, cold, deleted})
	if err := store.MarkDeploymentSuperseded(ctx, pinnedDep.ID); err != nil {
		t.Fatal(err)
	}
	assertCandidates("", []state.App{raw, stageOnly}, []state.App{pinned, cold, deleted})
}
