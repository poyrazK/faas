package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type intentTestStore interface {
	gitOpsTestStore
	state.EnvironmentGitOpsIntentStore
	state.EnvironmentGitOpsControlStore
	CompareAndSetAppStatus(context.Context, string, state.AppStatus, state.AppStatus) (bool, error)
}

func intentFixture(t *testing.T, store intentTestStore, mode string) (state.EnvironmentGitSource, environmentsync.DesiredState, state.App) {
	t.Helper()
	source, base := seedMode(t, store, mode)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-api", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MANUAL", "keep"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "staging", "MODE", "staging"); err != nil {
		t.Fatal(err)
	}
	d := base.Definition
	d.Configuration = map[string]json.RawMessage{"LOG_LEVEL": json.RawMessage(`"info"`)}
	w := d.Workloads["api"]
	w.Routes = &api.EnvironmentRouteContract{OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []api.DeclaredRoute{{Path: "/health", Methods: []string{"GET"}}}}
	w.Policies = &[]api.EnvironmentPolicy{{Name: "security", Kind: "headers", Action: json.RawMessage(`{"response_headers":[{"name":"X-Content-Type-Options","action":"set","value":"nosniff"}]}`)}}
	d.Workloads["api"] = w
	desired, err := environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	return source, desired, app
}

func assertVariable(t *testing.T, store state.Store, source state.EnvironmentGitSource, app state.App, scope, key, expected string) {
	t.Helper()
	variables, err := store.ListAppEnvInScope(t.Context(), source.AccountID, app.ID, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range variables {
		if variable.Key == key && variable.Value == expected {
			return
		}
	}
	t.Fatalf("missing expected %s/%s", scope, key)
}

func TestRealEnvironmentGitOpsAdoptionAndEnforcement(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, _, app := intentFixture(t, store, "enforce")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("preview: %+v %v", preview, err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "console")
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "forbidden"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("unreviewed owned mutation: %v", err)
		}
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployLive, Scope: "production"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LatestSnapshot(t.Context(), deployment.ID); err != nil {
			t.Fatalf("seeded snapshot is not restorable: %v", err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
			t.Fatalf("worker: %v %v", worked, err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "production")
		assertVariable(t, store, source, app, "production", "MANUAL", "keep")
		assertVariable(t, store, source, app, "staging", "MODE", "staging")
		if _, err := store.LatestSnapshot(t.Context(), deployment.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old runtime cache remained restorable: %v", err)
		}
		if stamp, exists, err := state.RuntimeConfigChangedAtForScope(t.Context(), store, app.ID, "production"); err != nil || !exists || stamp.IsZero() {
			t.Fatalf("runtime freshness stamp: %v %v %v", stamp, exists, err)
		}
		routes, err := store.GetProjectEnvironmentRoutePolicy(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || !routes.OnlyAllowDeclaredRoutes || len(routes.DeclaredRoutes) != 1 {
			t.Fatalf("routes: %+v %v", routes, err)
		}
		policies, err := store.GetProjectEnvironmentEdgePolicy(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || len(policies.Rules) != 1 || policies.Rules[0].Name != "security" {
			t.Fatalf("policies: %+v %v", policies, err)
		}
		config, err := store.ProjectEnvironmentConfigLatest(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil || string(config.Values) != `{"LOG_LEVEL":"info"}` {
			t.Fatalf("config: %+v %v", config, err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil || current.AppliedRevisionID != current.ApprovedRevisionID {
			t.Fatalf("no verified convergence: %+v %v", current, err)
		}
		for _, transition := range [][2]state.AppStatus{{state.AppActive, state.AppEvictedCold}, {state.AppEvictedCold, state.AppActive}} {
			if changed, err := store.CompareAndSetAppStatus(t.Context(), app.ID, transition[0], transition[1]); err != nil || !changed {
				t.Fatalf("runtime eviction/activation treated as intent drift: %v %v", changed, err)
			}
		}
		if _, err := store.SoftDeleteAppCascade(t.Context(), app.ID); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("owned membership deletion: %v", err)
		}
		afterLifecycle, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil || afterLifecycle.IntentVersion != current.IntentVersion {
			t.Fatalf("runtime transitions or rejected deletion changed intent: %+v %v", afterLifecycle, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 1 || runs[0].Status != "converged" {
			t.Fatalf("runs: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsEmptyMembershipCannotBypassWorkloadPruning(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, prior, app := intentFixture(t, store, "enforce")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("adoption: %+v %v", preview, err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		emptyDefinition := prior.Definition
		emptyDefinition.Workloads = map[string]api.EnvironmentWorkload{}
		empty, err := environmentsync.Compile(emptyDefinition)
		if err != nil {
			t.Fatal(err)
		}
		prune := true
		source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, empty, strings.Repeat("b", 40))); err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "empty-environment-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, empty)
		if plan.CanApply() || !strings.Contains(strings.Join(plan.BlockingReasons, " "), "workload pruning adapter") {
			t.Fatalf("empty definition bypassed graph pruning gate: %+v", plan)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unsupported prune mutated intent: %v", err)
		}
		current, err := store.AppByID(t.Context(), app.ID)
		if err != nil || current.Status != app.Status {
			t.Fatalf("empty definition deleted the adopted application: %+v %v", current, err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "console")
		assertVariable(t, store, source, app, "production", "MANUAL", "keep")
	})
}

func TestEnvironmentGitOpsQuotaAcrossScopesBlocksWholePlan(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, desired, app := intentFixture(t, store, "enforce")
		limits := api.MustLimitsFor(api.PlanPro)
		// The fixture has three variables across production and staging.
		for i := 3; i < limits.EnvVarsMax; i++ {
			if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "staging", fmt.Sprintf("EXTRA_%d", i), "unmanaged"); err != nil {
				t.Fatal(err)
			}
		}
		definition := desired.Definition
		workload := definition.Workloads["api"]
		workload.Variables["NEW"] = "blocked"
		definition.Workloads["api"] = workload
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || preview.CanApply() || !strings.Contains(strings.Join(preview.BlockingReasons, " "), "quota across environments") {
			t.Fatalf("quota preflight: %+v %v", preview, err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if _, err := worker.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "console")
		if config, err := store.ProjectEnvironmentConfigLatest(t.Context(), source.AccountID, source.ProjectID, "production"); (err != nil && !errors.Is(err, state.ErrNotFound)) || config.Version != 0 {
			t.Fatalf("quota-blocked run changed configuration: %+v %v", config, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 1 || runs[0].Status != "blocked" {
			t.Fatalf("quota status: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsWorkloadPruningBlocksBeforeWrites(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, desired, app := intentFixture(t, store, "enforce")
		workerApp, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID,
			Slug: "shop-worker", WorkloadName: "worker", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		definition := desired.Definition
		definition.Workloads["worker"] = api.EnvironmentWorkload{App: workerApp.Slug}
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		prune := true
		source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		definition = desired.Definition
		delete(definition.Workloads, "worker")
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40)))
		if err != nil {
			t.Fatal(err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if _, err := worker.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "console")
		if current, err := store.AppBySlug(t.Context(), workerApp.Slug); err != nil || current.Status != state.AppActive {
			t.Fatalf("pruning removed existing workload: %+v %v", current, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 1 || runs[0].Status != "blocked" || !strings.Contains(string(runs[0].Plan), "workload pruning adapter") {
			t.Fatalf("workload prune preflight: %+v %v", runs, err)
		}
	})
}

func TestRealEnvironmentGitOpsStaleAdoptionAndReport(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, _, app := intentFixture(t, store, "report")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "new-console"); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale adoption accepted: %v", err)
		}
		preview, err = store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "report-console"); err != nil {
			t.Fatal(err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if _, err := worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "report-console")
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 1 || runs[0].Status != "drifted" {
			t.Fatalf("report: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsOverrideExpiryAndPruning(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, desired, app := intentFixture(t, store, "enforce")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if _, err := worker.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		expiry := time.Now().Add(400 * time.Millisecond)
		request := state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "variables/MODE", Reason: "incident mitigation", ExpiresAt: expiry}
		if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, request); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "temporary"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || runs[0].Status != "overridden" {
			t.Fatalf("override hidden: %+v %v", runs, err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "temporary")
		time.Sleep(max(time.Until(expiry)+time.Millisecond, 0))
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "late"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("expired override authorized edit: %v", err)
		}
		// Periodic work also restores expired overrides, without a fresh Git push.
		worker.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
		if _, err := worker.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "production")
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		prune := true
		current, err = store.UpdateEnvironmentGitSource(t.Context(), current.AccountID, current.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		w := desired.Definition.Workloads["api"]
		delete(w.Variables, "MODE")
		desired.Definition.Workloads["api"] = w
		desired, err = environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(current, desired, strings.Repeat("b", 40))); err != nil {
			t.Fatal(err)
		}
		worker.Now = nil
		if _, err := worker.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		variables, err := store.ListAppEnvInScope(t.Context(), source.AccountID, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		for _, variable := range variables {
			if variable.Key == "MODE" {
				t.Fatal("previously owned variable was not pruned")
			}
		}
		assertVariable(t, store, source, app, "production", "MANUAL", "keep")
		assertVariable(t, store, source, app, "staging", "MODE", "staging")
	})
}

func TestEnvironmentGitOpsRejectsStaleApplyAndSuccess(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, desired, app := intentFixture(t, store, "enforce")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "stale-apply", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		options := environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation, Now: time.Now()}
		plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, options)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MANUAL", "changed"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale plan applied: %v", err)
		}
		observed, err = store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = environmentsync.BuildPlan(desired, observed.State, observed.Owners, options)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
			t.Fatal(err)
		}
		observed, err = store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = environmentsync.BuildPlan(desired, observed.State, observed.Owners, options)
		if err != nil || plan.HasDrift() {
			t.Fatalf("verification: %+v %v", plan, err)
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MANUAL", "newer"); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(plan)
		if err := store.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", time.Now(), time.Now().Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed-after-verification published success: %v", err)
		}
	})
}
