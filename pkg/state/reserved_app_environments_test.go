package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A PATCH with ?environment= reserves only that environment's workload
// settings, which the reaper applies to the environment's deployments, so an
// app reserved in any environment counts toward the reserved-tier cap
// (issue #475) once, like an app whose own row is reserved. A retired
// reservation or a deleted environment stops counting.
func TestMemReservedAppsCountEnvironmentReservations(t *testing.T) {
	reservedEnvironmentContract(t, state.NewMemStore())
}

func reservedEnvironmentContract(t *testing.T, store state.Store) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	specs, ok := store.(state.ProjectEnvironmentWorkloadSpecStore)
	if !ok {
		t.Fatal("store has no environment workload specs")
	}
	account, err := store.CreateAccount(ctx, "reserved-env-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop-" + suffix, ProductionBranch: "main",
		ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, projectID, workload string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: projectID, WorkloadName: workload,
			Slug: slug + "-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	staged, other, plain := newApp("api", project.ID, "api"), newApp("web", project.ID, "web"), newApp("plain", "", "")
	stage := func(revision int64, priority api.EvictionPriority) {
		t.Helper()
		settings, err := state.MaterializeEnvironmentWorkloadSettings(ctx, store, staged, "staging")
		if err != nil {
			t.Fatal(err)
		}
		settings.EvictionPriority = string(priority)
		if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", staged.ID, revision, settings); err != nil {
			t.Fatal(err)
		}
	}
	// expect gives, per app in order, how many other apps count as reserved.
	expect := func(step string, apps []state.App, want ...int) {
		t.Helper()
		for i, app := range apps {
			got, err := store.CountOtherBillingSubjectAppsWithEvictionPriority(ctx, app.ID, string(api.EvictionPriorityReserved))
			if err != nil || got != want[i] {
				t.Fatalf("%s: reserved apps other than %s = (%d, %v), want %d", step, app.Slug, got, err, want[i])
			}
		}
	}
	apps := []state.App{other, plain, staged}

	stage(0, api.EvictionPriorityReserved)
	expect("staging reserves the api", apps, 1, 1, 0)
	stage(1, api.EvictionPriorityBestEffort)
	expect("staging releases the api", apps, 0, 0, 0)
	stage(2, api.EvictionPriorityReserved)
	expect("staging reserves the api again", apps, 1, 1, 0)
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	expect("staging deleted", apps, 0, 0, 0)
}
