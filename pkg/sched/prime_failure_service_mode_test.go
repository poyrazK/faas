package sched_test

// adr: 20261029120000 — a project service deployed as a worker that listens
// fails with the Compose fix, not the gregale.yaml one.

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMarkPrimeFailedNamesTheComposeFixForAListeningService(t *testing.T) {
	listening := api.NewProblem(422, api.CodeExecutionModeConflict, "execution mode conflicts with the app's behavior",
		api.ExecutionModeConflict{Mode: api.ExecutionModeWorker, Class: "http", Port: 3000, ExitCode: -1}.Detail())
	exited := api.NewProblem(422, api.CodeExecutionModeConflict, "execution mode conflicts with the app's behavior",
		api.ExecutionModeConflict{Mode: api.ExecutionModeWorker, Class: "job", ExitCode: 0}.Detail())
	for _, tc := range []struct {
		name        string
		project     bool
		cause       error
		wantCompose bool
	}{
		{"a listening project service", true, listening, true},
		{"a listening standalone app", false, listening, false},
		{"a project worker that exited", true, exited, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app := state.App{AccountID: acct.ID, Slug: "shop-api", RAMMB: 512, MaxConcurrency: 1, WorkloadClass: state.WorkloadClassWorker,
				Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}}
			if tc.project {
				project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
				if err != nil {
					t.Fatal(err)
				}
				app.ProjectID, app.WorkloadName = project.ID, "api"
			}
			app, err = store.CreateApp(ctx, app)
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:abc", Status: state.DeploySnapshotting})
			if err != nil {
				t.Fatal(err)
			}

			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			e.MarkPrimeFailed(ctx, dep.ID, tc.cause)

			got, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != state.DeployFailed || got.ErrorCode != api.CodeExecutionModeConflict {
				t.Fatalf("deployment status/code = %s/%s", got.Status, got.ErrorCode)
			}
			compose := strings.Contains(got.ErrorFix, `expose: ["3000"]`) && strings.Contains(got.Error, `api listens on port 3000`)
			if compose != tc.wantCompose {
				t.Fatalf("Compose fix = %t, want %t\nerror=%q\nfix=%q", compose, tc.wantCompose, got.Error, got.ErrorFix)
			}
			if !tc.wantCompose && !strings.Contains(got.ErrorFix, "execution_mode") {
				t.Fatalf("fix = %q, want the generic execution_mode guidance", got.ErrorFix)
			}
		})
	}
}
