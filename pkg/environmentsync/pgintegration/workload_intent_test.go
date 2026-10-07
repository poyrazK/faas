package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func workloadIntentFixture(t *testing.T, basic gitOpsTestStore, mode string) (intentTestStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App, state.Deployment, state.ProjectEnvironment) {
	return workloadIntentFixtureType(t, basic, mode, state.AppTypeApp)
}

func workloadIntentFixtureType(t *testing.T, basic gitOpsTestStore, mode string, appType state.AppType) (intentTestStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App, state.Deployment, state.ProjectEnvironment) {
	return workloadIntentFixtureWithProtocolTransport(t, basic, mode, appType, "", "")
}

func workloadIntentFixtureWithProtocolTransport(t *testing.T, basic gitOpsTestStore, mode string, appType state.AppType, appProtocol string, transport api.ServiceBindingTransport) (intentTestStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App, state.Deployment, state.ProjectEnvironment) {
	t.Helper()
	store := basic.(intentTestStore)
	source, desired := seedMode(t, store, mode)
	runtime := ""
	if appType == state.AppTypeFunction {
		runtime = "node22"
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, AppProtocol: appProtocol,
		Slug: "shop-api", Type: appType, Runtime: runtime, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive,
		Manifest: state.AppManifest{Entrypoint: []string{"./api"}, Port: 8079, StopGracePeriodS: 10, ServiceBindingTransport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production",
		Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "registry.example/shop@sha256:" + strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	dep.Status = state.DeployLive
	staging, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := basic.(state.EnvironmentWorkloadIntentStore).PutEnvironmentWorkloadIntent(t.Context(), state.EnvironmentWorkloadIntent{
		AccountID: source.AccountID, AppID: app.ID, EnvironmentID: staging.ID, Runtime: map[string]json.RawMessage{"port": json.RawMessage(`9080`)}}); err != nil {
		t.Fatal(err)
	}
	w := desired.Definition.Workloads["api"]
	w.Variables = nil
	w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/shop@sha256:" + strings.Repeat("d", 64)}
	w.Runtime = json.RawMessage(`{"port":8080,"ports":[],"startup_deadline_s":25,"stop_grace_period":20000000000}`)
	desired.Definition.Workloads["api"] = w
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	return store, source, desired, app, dep, staging
}

func scopedWorkloadIntent(t *testing.T, basic gitOpsTestStore, source state.EnvironmentGitSource, appID, environmentID string) state.EnvironmentWorkloadIntent {
	t.Helper()
	row, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), source.AccountID, appID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func adoptWorkloadIntent(t *testing.T, store intentTestStore, source state.EnvironmentGitSource) {
	t.Helper()
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentGitOpsScopedWorkloadIntentAdoptionAndQualification(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app, dep, staging := workloadIntentFixture(t, basic, "enforce")
		adoptWorkloadIntent(t, store, source)
		row := scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
		if row.Source == nil || row.Source.Image != dep.ImageDigest || string(row.Runtime["port"]) != "8079" || string(row.Runtime["ports"]) != "null" || string(row.Runtime["stop_grace_period"]) != "10000000000" {
			t.Fatalf("adoption replaced the reviewed baseline: %+v", row)
		}
		workloads := basic.(state.EnvironmentWorkloadIntentStore)
		row.Runtime["port"] = json.RawMessage(`9090`)
		if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("unreviewed runtime write: %v", err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "scoped-workload-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if !plan.CanApply() || !plan.HasDrift() {
			t.Fatalf("scoped intent was not executable: %+v", plan)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
			t.Fatal(err)
		}
		row = scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
		if row.Source == nil || *row.Source != *desired.Definition.Workloads["api"].Source || string(row.Runtime["port"]) != "8080" {
			t.Fatalf("reviewed intent missing: %+v", row)
		}
		currentApp, err := store.AppByID(t.Context(), app.ID)
		if err != nil || !reflect.DeepEqual(currentApp.Manifest, app.Manifest) {
			t.Fatalf("scoped reconciliation changed shared manifest: %+v %v", currentApp.Manifest, err)
		}
		currentDep, err := store.DeploymentByID(t.Context(), dep.ID)
		if err != nil || currentDep.ImageDigest != dep.ImageDigest || currentDep.Status != dep.Status {
			t.Fatalf("intent staging activated a deployment: %+v %v", currentDep, err)
		}
		if neighbor := scopedWorkloadIntent(t, basic, source, app.ID, staging.ID); string(neighbor.Runtime["port"]) != "9080" {
			t.Fatalf("neighbor environment changed: %+v", neighbor)
		}
		verified := claimedIntentPlan(t, store, lease, desired)
		if !verified.CanApply() || verified.HasDrift() {
			t.Fatalf("intent did not converge: %+v", verified)
		}
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		targets, err := runtime.ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(targets) != 1 || targets[0].UnqualifiedWorkloads != 1 || !targets[0].Fresh() || targets[0].Ready() {
			t.Fatalf("intent equality hid unqualified serving graph: %+v %v", targets, err)
		}
		if err := runtime.EnsureEnvironmentGitOpsRuntime(t.Context(), lease, verified); err != nil {
			t.Fatal(err)
		}
		if pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease); err != nil || len(pending) != 0 {
			t.Fatalf("unqualified graph requested an unrelated variable refresh: %+v %v", pending, err)
		}
		if err := runtimeFinish(t, store, lease, desired); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unqualified graph published applied revision: %v", err)
		}
		raw, _ := json.Marshal(verified)
		if err := store.FinishEnvironmentGitOps(t.Context(), lease, "partial", raw, json.RawMessage(`[]`), "environment_runtime_unacknowledged", time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
			t.Fatalf("recovery worker: %v %v", worked, err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil || current.AppliedRevisionID != "" {
			t.Fatalf("intent-only backend claimed serving convergence: %+v %v", current, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "partial" {
			t.Fatalf("missing qualification status: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsScopedWorkloadIntentReportAndOverride(t *testing.T) {
	for _, mode := range []string{"report", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, app, _, _ := workloadIntentFixture(t, basic, mode)
				adoptWorkloadIntent(t, store, source)
				workloads := basic.(state.EnvironmentWorkloadIntentStore)
				if mode == "enforce" {
					if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, state.EnvironmentGitOpsOverrideRequest{
						Resource: "workload/api", Path: "runtime/port", Reason: "incident mitigation", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				row := scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
				row.Runtime["port"] = json.RawMessage(`9090`)
				if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); err != nil {
					t.Fatal(err)
				}
				if mode == "enforce" {
					row.Source = desired.Definition.Workloads["api"].Source
					if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); !errors.Is(err, state.ErrEnvironmentGitManaged) {
						t.Fatalf("port override authorized a source change: %v", err)
					}
				}
				worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
				if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
					t.Fatalf("worker: %v %v", worked, err)
				}
				if row := scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID); string(row.Runtime["port"]) != "9090" {
					t.Fatalf("permitted console edit was restored: %+v", row)
				}
				runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
				want := "drifted"
				if mode == "enforce" {
					want = "overridden"
				}
				if err != nil || len(runs) != 1 || runs[0].Status != want {
					t.Fatalf("console difference hidden: %+v %v", runs, err)
				}
				if mode == "enforce" {
					if err := store.RemoveEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, "workload/api", "runtime/port"); err != nil {
						t.Fatal(err)
					}
					if _, err := worker.RunOnce(t.Context()); err != nil {
						t.Fatal(err)
					}
					if row := scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID); string(row.Runtime["port"]) != "8080" {
						t.Fatalf("override removal did not restore Git intent: %+v", row)
					}
				}
			})
		})
	}
}

func TestEnvironmentGitOpsScopedWorkloadIntentScopeAndValidation(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, source, _, app, _, staging := workloadIntentFixture(t, basic, "enforce")
		workloads := basic.(state.EnvironmentWorkloadIntentStore)
		row := scopedWorkloadIntent(t, basic, source, app.ID, staging.ID)
		for _, field := range []string{"account", "app", "environment"} {
			invalid := row
			switch field {
			case "account":
				invalid.AccountID = uuid.NewString()
			case "app":
				invalid.AppID = uuid.NewString()
			case "environment":
				invalid.EnvironmentID = uuid.NewString()
			}
			if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), invalid); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("%s identity write escaped its scope: %v", field, err)
			}
			if _, err := workloads.EnvironmentWorkloadIntent(t.Context(), invalid.AccountID, invalid.AppID, invalid.EnvironmentID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("%s identity read escaped its scope: %v", field, err)
			}
		}
		for _, raw := range []string{`{"port":null}`, `{"unknown":true}`, `{"startup_deadline_s":100}`, `{"execution_mode":"worker"}`} {
			invalid := row
			invalid.Runtime = nil
			_ = json.Unmarshal([]byte(raw), &invalid.Runtime)
			if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), invalid); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid runtime %s accepted: %v", raw, err)
			}
		}
		if after := scopedWorkloadIntent(t, basic, source, app.ID, staging.ID); string(after.Runtime["port"]) != "9080" {
			t.Fatalf("rejected writes mutated prior intent: %+v", after)
		}
		// Nil denotes an empty scoped override, never a JSON null contract.
		row.Runtime = nil
		if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); err != nil {
			t.Fatal(err)
		}
		if err := basic.DeleteProjectEnvironment(t.Context(), source.AccountID, source.ProjectID, staging.Slug); err != nil {
			t.Fatal(err)
		}
		replacement, err := basic.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: staging.Slug})
		if err != nil {
			t.Fatal(err)
		}
		for _, environmentID := range []string{staging.ID, replacement.ID} {
			if _, err := workloads.EnvironmentWorkloadIntent(t.Context(), source.AccountID, app.ID, environmentID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replacement catalog identity inherited retired intent: %v", err)
			}
		}
	})
}

func TestEnvironmentGitOpsScopedWorkloadIntentBindsInheritedBaseline(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, _, _ := workloadIntentFixture(t, basic, "enforce")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("preview: %+v %v", preview, err)
		}
		manifest := app.Manifest
		manifest.Entrypoint = []string{"./changed-unmanaged-command"}
		if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale inherited baseline was adopted: %v", err)
		}
		if _, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), source.AccountID, app.ID, source.EnvironmentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stale adoption persisted scoped intent: %v", err)
		}
	})
}

func TestEnvironmentGitOpsScopedWorkloadIntentBlocksWholePlan(t *testing.T) {
	for _, raw := range []string{`{"startup_deadline_s":100}`, `{"execution_mode":"worker"}`} {
		t.Run(raw, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, app, _, _ := workloadIntentFixture(t, basic, "enforce")
				w := desired.Definition.Workloads["api"]
				w.Runtime, w.Variables = json.RawMessage(raw), map[string]string{"MODE": "production"}
				desired.Definition.Workloads["api"] = w
				var err error
				desired, err = environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
				if err != nil {
					t.Fatal(err)
				}
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "blocked-runtime-worker", time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				if plan.CanApply() || !strings.Contains(strings.Join(plan.BlockingReasons, " "), "workload or plan contract") {
					t.Fatalf("runtime preflight failed open: %+v", plan)
				}
				if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("invalid whole plan executed: %v", err)
				}
				if values, err := store.ListAppEnvInScope(t.Context(), source.AccountID, app.ID, "production"); err != nil || len(values) != 0 {
					t.Fatalf("blocked workload changed variables: %+v %v", values, err)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsScopedWorkloadIntentPruningAndSupersession(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app, _, _ := workloadIntentFixture(t, basic, "enforce")
		adoptWorkloadIntent(t, store, source)
		row := scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
		row.Runtime["healthz"] = json.RawMessage(`"/manual"`)
		if _, err := basic.(state.EnvironmentWorkloadIntentStore).PutEnvironmentWorkloadIntent(t.Context(), row); err != nil {
			t.Fatal(err)
		}
		old, err := store.ClaimEnvironmentGitOps(t.Context(), "old-runtime-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		oldPlan := claimedIntentPlan(t, store, old, desired)
		prune := true
		source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		w := desired.Definition.Workloads["api"]
		w.Runtime = json.RawMessage(`{"startup_deadline_s":25}`)
		desired.Definition.Workloads["api"] = w
		desired, err = environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), old, oldPlan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded lease changed scoped workload: %v", err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "new-runtime-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if !plan.CanApply() {
			t.Fatalf("pruning: %+v", plan)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
			t.Fatal(err)
		}
		row = scopedWorkloadIntent(t, basic, source, app.ID, source.EnvironmentID)
		if _, present := row.Runtime["port"]; present || string(row.Runtime["healthz"]) != `"/manual"` {
			t.Fatalf("pruning retained owned override or removed unmanaged key: %+v", row)
		}
		row.Runtime["port"] = json.RawMessage(`9091`)
		if _, err := basic.(state.EnvironmentWorkloadIntentStore).PutEnvironmentWorkloadIntent(t.Context(), row); err != nil {
			t.Fatalf("pruning did not release field ownership: %v", err)
		}
	})
}

func TestEnvironmentGitOpsScopedWorkloadIntentPostgresGuardsAndRollback(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, source, desired, app, dep, staging := workloadIntentFixture(t, store, "enforce")
	if err := store.UpsertAppEnvInScope(ctx, source.AccountID, app.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	w := desired.Definition.Workloads["api"]
	w.Variables = map[string]string{"MODE": "production"}
	desired.Definition.Workloads["api"] = w
	desired, err := environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("b", 40)))
	if err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store, source)
	for _, query := range []string{
		`update app_environment_workload_intents set runtime=jsonb_set(runtime,'{port}','9090') where app_id=$1 and environment_id=$2`,
		`update app_environment_workload_intents set source=NULL where app_id=$1 and environment_id=$2`,
		`delete from app_environment_workload_intents where app_id=$1 and environment_id=$2`,
	} {
		_, err := pool.Exec(ctx, query, app.ID, source.EnvironmentID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "environment_gitops_field_owned" {
			t.Fatalf("raw SQL ownership bypass: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `update app_environment_workload_intents set environment_id=$3 where app_id=$1 and environment_id=$2`, app.ID, source.EnvironmentID, staging.ID); err == nil {
		t.Fatal("raw SQL transferred original environment identity")
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "atomic-workload-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	// Fail after scoped source/runtime writes, proving the complete intent
	// transaction rolls back when a later adapter cannot commit.
	if _, err := pool.Exec(ctx, `create function reject_gitops_variable() returns trigger language plpgsql as $$begin raise exception 'injected variable failure'; end$$;
	 create trigger reject_gitops_variable before update on app_envs for each row execute function reject_gitops_variable()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(ctx, lease, plan); err == nil {
		t.Fatal("injected later write failure was ignored")
	}
	row := scopedWorkloadIntent(t, store, source, app.ID, source.EnvironmentID)
	if row.Source == nil || row.Source.Image != dep.ImageDigest || string(row.Runtime["port"]) != "8079" {
		t.Fatalf("rollback left partial scoped intent: %+v", row)
	}
	assertVariable(t, store, source, app, "production", "MODE", "console")
	if _, err := pool.Exec(ctx, `drop trigger reject_gitops_variable on app_envs; drop function reject_gitops_variable()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(ctx, lease, plan); err != nil {
		t.Fatalf("rollback did not preserve retryable lease and observation: %v", err)
	}
	// A token from an actual, previously valid lease loses authority after a
	// source generation change, including direct SQL writes.
	prune := true
	if _, err := store.UpdateEnvironmentGitSource(ctx, source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `update app_environment_workload_intents set runtime=jsonb_set(runtime,'{port}','9090') where app_id=$1 and environment_id=$2`, app.ID, source.EnvironmentID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "environment_gitops_field_owned" {
		t.Fatalf("superseded raw SQL lease retained authority: %v", err)
	}
}

func TestEnvironmentGitOpsScopedWorkloadIntentAmbiguousSource(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, _, _ := workloadIntentFixture(t, basic, "enforce")
		canary, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production",
			Kind: state.DeploymentKindImage, ImageDigest: "registry.example/shop@sha256:" + strings.Repeat("e", 64),
			CanaryTotalSteps: 2, RolloutState: "rolling_out"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), canary.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || preview.CanApply() || !strings.Contains(strings.Join(preview.BlockingReasons, " "), "disagree on source") {
			t.Fatalf("ambiguous serving sources silently adopted: %+v %v", preview, err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ambiguous baseline applied: %v", err)
		}
	})
}

func TestEnvironmentGitOpsScopedWorkloadIntentParentPurges(t *testing.T) {
	for _, parent := range []string{"project", "account"} {
		t.Run(parent, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, _, app, _, staging := workloadIntentFixture(t, basic, "enforce")
				adoptWorkloadIntent(t, store, source)
				if parent == "project" {
					if err := store.DeleteProject(t.Context(), source.ProjectID); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := store.DeleteAccount(t.Context(), source.AccountID); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("active account purge: %v", err)
					}
					if current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production"); err != nil || current.ID != source.ID {
						t.Fatalf("active account lost ownership source: %+v %v", current, err)
					}
					if err := store.MarkAccountDeletionPending(t.Context(), source.AccountID); err != nil {
						t.Fatal(err)
					}
					if err := store.DeleteAccount(t.Context(), source.AccountID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production"); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("purge retained approved definitions and ownership: %v", err)
				}
				for _, environmentID := range []string{source.EnvironmentID, staging.ID} {
					if _, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), source.AccountID, app.ID, environmentID); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("purged original identity still exposes scoped intent: %v", err)
					}
				}
			})
		})
	}
}
