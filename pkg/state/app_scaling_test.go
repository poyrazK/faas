// adr: 20261104104521
package state_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRecoverableAppScaling(t *testing.T) { testRecoverableAppScaling(t, state.NewMemStore()) }

func testRecoverableAppScaling(t *testing.T, store state.Store) {
	t.Helper()
	for _, variant := range []string{"default", "project-default", "staging"} {
		t.Run(variant, func(t *testing.T) {
			scope := variant
			if variant == "project-default" {
				scope = "default"
			}
			ctx := t.Context()
			scaling := store.(state.AppScalingStore)
			account, err := store.CreateAccount(ctx, "scaling-"+uuid.NewString()+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app := state.App{AccountID: account.ID, Slug: "scale-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, CPUMillicores: 250, MaxConcurrency: 4, WorkloadClass: state.WorkloadClassHTTP}
			if variant != "default" {
				project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "scale-" + uuid.NewString()[:8]})
				if err != nil {
					t.Fatal(err)
				}
				app.ProjectID = project.ID
			}
			app, err = store.CreateApp(ctx, app)
			if err != nil {
				t.Fatal(err)
			}
			if scope != "default" {
				if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: app.ProjectID, Slug: scope}); err != nil {
					t.Fatal(err)
				}
				settings, err := state.WorkloadSettingsFromApp(app)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, app.ProjectID, scope, app.ID, 0, settings); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "project-default" {
				// A legacy floor writer can leave the production desired spec older
				// than the app row. Scaling must preserve every reviewed value.
				workloads := store.(state.ProjectEnvironmentWorkloadSpecStore)
				current, err := workloads.ProjectEnvironmentWorkloadSpec(ctx, account.ID, app.ProjectID, "production", app.ID)
				if err != nil && !errors.Is(err, state.ErrNotFound) {
					t.Fatal(err)
				}
				settings, err := state.WorkloadSettingsFromApp(app)
				if err != nil {
					t.Fatal(err)
				}
				settings.MinInstances = 2
				if _, err := workloads.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, app.ProjectID, "production", app.ID, current.Revision, settings); err != nil {
					t.Fatal(err)
				}
			}
			before, err := scaling.AppScaling(ctx, account.ID, app.ID, scope)
			if err != nil || before.Revision < 1 {
				t.Fatalf("snapshot: %+v %v", before, err)
			}
			ram := 512
			change := state.AppScalingChange{AccountID: account.ID, AppID: app.ID, Scope: scope, Request: api.ApplyAppScalingRequest{ExpectedAppID: app.ID, RequestID: uuid.NewString(), ExpectedEnvironmentID: before.EnvironmentID, ExpectedRevision: &before.Revision, RAMMB: &ram}}
			refusal := errors.New("admission refused")
			change.Validate = func(state.App) error { return refusal }
			if _, err := scaling.ApplyAppScaling(ctx, change); !errors.Is(err, refusal) {
				t.Fatalf("admission: %v", err)
			}
			unchanged, err := scaling.AppScaling(ctx, account.ID, app.ID, scope)
			if err != nil || unchanged != before {
				t.Fatalf("refusal changed settings: %+v %v", unchanged, err)
			}
			if _, err := scaling.AppScalingReceipt(ctx, account.ID, app.ID, scope, change.Request.RequestID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("refused request has receipt: %v", err)
			}
			wrongApp := change
			wrongApp.Request.ExpectedAppID = uuid.NewString()
			if _, err := scaling.ApplyAppScaling(ctx, wrongApp); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("app identity not pinned: %v", err)
			}
			change.Validate = nil
			first, err := scaling.ApplyAppScaling(ctx, change)
			if err != nil || first.Revision <= before.Revision || first.Settings.RAMMB != ram {
				t.Fatalf("commit: %+v %v", first, err)
			}
			if scope == "default" {
				noop := change
				noop.Request.RequestID = uuid.NewString()
				noop.Request.ExpectedRevision = &first.Revision
				accepted, err := scaling.ApplyAppScaling(ctx, noop)
				if err != nil || accepted.Revision <= first.Revision {
					t.Fatalf("no-op did not consume revision: %+v %v", accepted, err)
				}
			} else {
				wrongEnvironment := change
				wrongEnvironment.Request.ExpectedEnvironmentID = uuid.NewString()
				if _, err := scaling.ApplyAppScaling(ctx, wrongEnvironment); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("environment identity not pinned: %v", err)
				}
			}
			if first.Settings.MinInstances != before.Settings.MinInstances || first.Settings.CPUMillicores != before.Settings.CPUMillicores || first.Settings.MaxConcurrency != before.Settings.MaxConcurrency {
				t.Fatalf("omitted scaling settings changed: before %+v receipt %+v", before, first)
			}
			// A legacy writer advances the same guard, including an A -> B -> A edit.
			if scope == "default" {
				for _, memory := range []int{768, 512} {
					if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &memory}); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				secondRAM := 768
				env, err := store.ProjectEnvironmentBySlug(ctx, account.ID, app.ProjectID, scope)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := state.UpdateEnvironmentWorkloadSettings(ctx, store.(state.ProjectEnvironmentWorkloadSpecStore), app, env, &first.Revision, state.UpdateAppParams{RAMMB: &secondRAM}); err != nil {
					t.Fatal(err)
				}
			}
			current, err := scaling.AppScaling(ctx, account.ID, app.ID, scope)
			if err != nil || current.Revision <= first.Revision {
				t.Fatalf("legacy revision: %+v %v", current, err)
			}
			replayed, err := scaling.ApplyAppScaling(ctx, change)
			if err != nil || !reflect.DeepEqual(replayed, first) {
				t.Fatalf("replay: %+v %v", replayed, err)
			}
			recovered, err := scaling.AppScalingReceipt(ctx, account.ID, app.ID, scope, change.Request.RequestID)
			if err != nil || !reflect.DeepEqual(recovered, first) {
				t.Fatalf("recover: %+v %v", recovered, err)
			}
			afterReplay, err := scaling.AppScaling(ctx, account.ID, app.ID, scope)
			if err != nil || afterReplay != current {
				t.Fatalf("replay overwrote later writer: %+v %v", afterReplay, err)
			}
			changed := change
			otherRAM := 1024
			changed.Request.RAMMB = &otherRAM
			if _, err := scaling.ApplyAppScaling(ctx, changed); !errors.Is(err, state.ErrScalingRequestConflict) {
				t.Fatalf("changed payload: %v", err)
			}
			changed.Request.RequestID = uuid.NewString()
			if _, err := scaling.ApplyAppScaling(ctx, changed); !errors.Is(err, state.ErrScalingRevisionConflict) {
				t.Fatalf("stale review: %v", err)
			}
			changed.AccountID = uuid.NewString()
			if _, err := scaling.ApplyAppScaling(ctx, changed); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("cross-account write: %v", err)
			}
			if _, err := scaling.AppScalingReceipt(ctx, changed.AccountID, app.ID, scope, change.Request.RequestID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("cross-account receipt: %v", err)
			}
			// Different requests reviewed at the same revision cannot both commit.
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for range 2 {
				wg.Go(func() {
					req := change
					req.Request.RequestID = uuid.NewString()
					req.Request.ExpectedRevision = &current.Revision
					req.Request.RAMMB = &otherRAM
					_, err := scaling.ApplyAppScaling(ctx, req)
					results <- err
				})
			}
			wg.Wait()
			close(results)
			committed, conflicted := 0, 0
			for err := range results {
				if err == nil {
					committed++
				} else if errors.Is(err, state.ErrScalingRevisionConflict) {
					conflicted++
				} else {
					t.Fatal(err)
				}
			}
			if committed != 1 || conflicted != 1 {
				t.Fatalf("concurrent outcomes: %d commits %d conflicts", committed, conflicted)
			}
			if scope == "staging" {
				production, err := store.AppByID(ctx, app.ID)
				if err != nil || production.RAMMB != 256 {
					t.Fatalf("named scaling changed production: %+v %v", production, err)
				}
			}
		})
	}
}
