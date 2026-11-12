package reconcile_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemEnvironmentDeletionKeepsUnfinishedDeploymentSettings(t *testing.T) {
	testEnvironmentDeletionKeepsUnfinishedDeploymentSettings(t, state.NewMemStore())
}

func TestMemEnvironmentDeletionRacesDeploymentCreation(t *testing.T) {
	testEnvironmentDeletionRacesDeploymentCreation(t, state.NewMemStore())
}

func testEnvironmentDeletionRacesDeploymentCreation(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "delete-create-race@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "delete-create-race"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "delete-create-race-web", WorkloadName: "web", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		scope := fmt.Sprintf("race-%d", i)
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		created, deleted := make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			_, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball})
			created <- err
		}()
		go func() {
			<-start
			deleted <- store.DeleteProjectEnvironment(ctx, account.ID, project.ID, scope)
		}()
		close(start)
		createErr, deleteErr := <-created, <-deleted
		if createErr == nil && deleteErr == nil {
			t.Fatal("created a deployment and deleted its environment in the same race")
		}
		if createErr != nil && deleteErr != nil {
			t.Fatalf("neither competing operation completed: create=%v delete=%v", createErr, deleteErr)
		}
		_, lookupErr := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, scope)
		if createErr == nil && lookupErr != nil || deleteErr == nil && !errors.Is(lookupErr, state.ErrNotFound) {
			t.Fatalf("race lost environment ownership: create=%v delete=%v lookup=%v", createErr, deleteErr, lookupErr)
		}
	}
}

func testEnvironmentDeletionKeepsUnfinishedDeploymentSettings(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "delete-stage@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "delete-stage"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "delete-stage-web", WorkloadName: "web", Type: state.AppTypeApp, RAMMB: 256, StartCommand: "serve-production"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployPending, state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting, state.DeployLive, state.DeployFailed, state.DeployCancelled, state.DeploySuperseded} {
		t.Run(string(status), func(t *testing.T) {
			if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
				t.Fatal(err)
			}
			settings, err := state.WorkloadSettingsFromApp(app)
			if err != nil {
				t.Fatal(err)
			}
			settings.RAMMB, settings.StartCommand = 512, "serve-staging"
			if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, status, ""); err != nil {
				t.Fatal(err)
			}
			err = store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging")
			if !status.IsTerminal() {
				if !errors.Is(err, state.ErrConflict) {
					t.Fatalf("delete during %s: %v", status, err)
				}
				effective, err := state.AppForDeployment(ctx, store, dep)
				if err != nil || effective.RAMMB != 512 || effective.StartCommand != "serve-staging" {
					t.Fatalf("lost queued deployment settings: %+v %v", effective, err)
				}
				if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployFailed, "test cleanup"); err != nil {
					t.Fatal(err)
				}
				if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatalf("terminal history blocked deletion: %v", err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale preflight created deployment after deletion: %v", err)
			}
			if err := store.MarkDeploymentLive(ctx, dep.ID); err == nil {
				t.Fatal("activated a deployment after its environment was deleted")
			}
		})
	}
}
