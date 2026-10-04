// adr: 531
package state_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type productionWorkloadCollectionsStore interface {
	state.Store
	state.ProjectEnvironmentWorkloadSpecStore
	state.DeploymentWorkloadSpecReader
}

func TestMemProductionAppEditPreservesWorkloadCollections(t *testing.T) {
	testProductionAppEditPreservesWorkloadCollections(t, state.NewMemStore())
}

func testProductionAppEditPreservesWorkloadCollections(t *testing.T, store productionWorkloadCollectionsStore) {
	t.Helper()
	for _, collection := range []string{"legacy", "empty", "configured"} {
		t.Run(collection, func(t *testing.T) {
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@collections.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "collections-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID,
				Slug: "worker-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256,
				MaxConcurrency: 4, IdleTimeoutS: 30, Status: state.AppActive, WorkloadClass: state.WorkloadClassWorker})
			if err != nil {
				t.Fatal(err)
			}
			settings, err := state.WorkloadSettingsFromApp(app)
			if err != nil {
				t.Fatal(err)
			}
			if collection != "legacy" {
				settings.WorkPolicies = &state.ProjectEnvironmentWorkPolicySettings{Revision: 3, Policies: []state.ProjectEnvironmentCloneWorkPolicy{}}
				settings.QueueBindings = &state.ProjectEnvironmentQueueSettings{Revision: 2, Bindings: []state.ProjectEnvironmentQueueDefinition{}}
			}
			if collection == "configured" {
				settings.WorkPolicies.Policies = []state.ProjectEnvironmentCloneWorkPolicy{{Name: "serial", Revision: 3, MaxRunningPerKey: 1, PendingUpdates: "all"}}
				settings.QueueBindings.Bindings = []state.ProjectEnvironmentQueueDefinition{{Name: "jobs", QueueName: "jobs", Mode: "pull",
					WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2, RetryPolicyJSON: json.RawMessage(`{}`)}}
			}
			before, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID, 0, settings)
			if err != nil {
				t.Fatal(err)
			}
			oldDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			idle := 75
			for _, edit := range []struct {
				name   string
				params state.UpdateAppParams
			}{
				{"noop", state.UpdateAppParams{}},
				{"idle_timeout", state.UpdateAppParams{IdleTimeoutS: &idle, SetIdleTimeout: true}},
			} {
				if _, err := store.UpdateApp(ctx, app.ID, edit.params); err != nil {
					t.Fatal(err)
				}
				after, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after.Settings.WorkPolicies, before.Settings.WorkPolicies) ||
					!reflect.DeepEqual(after.Settings.QueueBindings, before.Settings.QueueBindings) {
					t.Fatalf("%s edit changed policy or queue collections: before=%+v after=%+v", edit.name, before.Settings, after.Settings)
				}
				if edit.name == "noop" && (after.ID != before.ID || after.Hash != before.Hash || after.Revision != before.Revision) {
					t.Fatal("no-op edit advanced the workload revision")
				}
				if edit.name == "idle_timeout" && (after.Settings.IdleTimeoutS != idle || after.Revision != before.Revision+1) {
					t.Fatalf("idle timeout edit did not publish the next desired revision: idle=%d want=%d revision=%d prior=%d", after.Settings.IdleTimeoutS, idle, after.Revision, before.Revision)
				}
			}
			newDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			for _, deployment := range []state.Deployment{oldDeployment, newDeployment} {
				pinned, err := store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, account.ID, project.ID, deployment.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(pinned.Settings.WorkPolicies, before.Settings.WorkPolicies) ||
					!reflect.DeepEqual(pinned.Settings.QueueBindings, before.Settings.QueueBindings) {
					t.Fatal("deployment lost its policy or queue configuration")
				}
				if deployment.ID == oldDeployment.ID && pinned.Settings.IdleTimeoutS != before.Settings.IdleTimeoutS ||
					deployment.ID == newDeployment.ID && pinned.Settings.IdleTimeoutS != idle {
					t.Fatal("deployment adopted the wrong settings revision")
				}
			}
		})
	}
}
