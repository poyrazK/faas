package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func newWorkloadFixture(t *testing.T, basic gitOpsTestStore, mode string, plan api.Plan) (state.EnvironmentGitSource, environmentsync.DesiredState, state.EnvironmentGitOpsLease) {
	t.Helper()
	account, err := basic.CreateAccount(t.Context(), "new-workload@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	project, err := basic.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "new-workload", RepoFullName: "example/new", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := basic.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/new", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: mode, ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
		"api":      {ServiceBindings: map[string]api.EnvironmentServiceBinding{"function": {Workload: "function", EnvKey: "FUNCTION_URL"}}, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "example/api@sha256:" + strings.Repeat("a", 64)}, Runtime: json.RawMessage(`{"port":8081}`)},
		"function": {Source: &api.EnvironmentWorkloadSource{Kind: "function", Runtime: "node22"}, Runtime: json.RawMessage(`{"port":8080}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := basic.ClaimEnvironmentGitOps(t.Context(), "new-workload", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return source, desired, lease
}

func TestEnvironmentGitOpsNewWorkloadReservation(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		source, desired, lease := newWorkloadFixture(t, basic, "enforce", api.PlanPro)
		store := basic.(state.EnvironmentGitOpsWorkloadCreationStore)
		intent := basic.(state.EnvironmentGitOpsIntentStore)
		plan := planForStore(t, intent, lease, desired)
		if !plan.CanApply() {
			t.Fatalf("new workload plan blocked: %+v", plan)
		}
		if _, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, environmentsync.Plan{Hash: strings.Repeat("0", 64)}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unreviewed reservation: %v", err)
		}
		steps, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan)
		if err != nil || len(steps) == 0 {
			t.Fatalf("reserve: %+v %v", steps, err)
		}
		if _, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old plan reused after identity creation: %v", err)
		}
		fresh := planForStore(t, intent, lease, desired)
		if !fresh.CanApply() || fresh.HasDrift() {
			t.Fatalf("scoped intent not atomically reserved: %+v", fresh)
		}
		if steps, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, fresh); err != nil || len(steps) != 0 {
			t.Fatalf("retry created more resources: %+v %v", steps, err)
		}
		apps, err := basic.ListApps(t.Context(), source.AccountID)
		if err != nil || len(apps) != 2 {
			t.Fatalf("apps: %+v %v", apps, err)
		}
		ids := map[string]string{}
		for _, app := range apps {
			if app.Visibility != api.AppVisibilityInternal {
				t.Fatalf("reservation became public: %+v", app)
			}
			if deps, err := basic.ListDeploymentsForApp(t.Context(), app.ID, 10, 0); err != nil || len(deps) != 0 {
				t.Fatalf("reservation launched deployments: %+v %v", deps, err)
			}
			row, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), source.AccountID, app.ID, source.EnvironmentID)
			if err != nil || row.Source == nil {
				t.Fatalf("source missing: %+v %v", row, err)
			}
			ids[row.Source.Kind] = app.ID
			if row.Source.Kind == "function" && (app.Type != state.AppTypeFunction || app.Runtime != "node22" || row.SourceRevision != lease.Revision.CommitSHA) {
				t.Fatalf("function runner or revision lost: %+v %+v", app, row)
			}
		}
		before, err := intent.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil || before.State.ResourceIDs["workload/function"] != ids["function"] {
			t.Fatalf("logical mapping lost: %+v %v", before, err)
		}
		if _, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, fresh); err != nil {
			t.Fatal(err)
		}
		after, err := intent.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil || !reflect.DeepEqual(before.State.ResourceIDs, after.State.ResourceIDs) {
			t.Fatalf("retry rebound identity: %+v %v", after, err)
		}
	})
}

func TestEnvironmentGitOpsNewWorkloadReservationIsAtomicAndEnforceOnly(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		plan       api.Plan
	}{{"quota", "enforce", api.PlanFree}, {"report", "report", api.PlanPro}} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				source, desired, lease := newWorkloadFixture(t, basic, tc.mode, tc.plan)
				plan := planForStore(t, basic.(state.EnvironmentGitOpsIntentStore), lease, desired)
				_, err := basic.(state.EnvironmentGitOpsWorkloadCreationStore).PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan)
				if err == nil {
					t.Fatal("forbidden reservation succeeded")
				}
				apps, err := basic.ListApps(t.Context(), source.AccountID)
				if err != nil || len(apps) != 0 {
					t.Fatalf("failed cohort leaked apps: %+v %v", apps, err)
				}
				after := planForStore(t, basic.(state.EnvironmentGitOpsIntentStore), lease, desired)
				if after.Hash != plan.Hash {
					t.Fatalf("failed cohort changed observation: before %s after %s", plan.Hash, after.Hash)
				}
			})
		})
	}
}

func planForStore(t *testing.T, store state.EnvironmentGitOpsIntentStore, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) environmentsync.Plan {
	t.Helper()
	observation, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observation.State, observation.Owners, environmentsync.PlanOptions{Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation, Now: time.Now(), Overrides: observation.Overrides})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
