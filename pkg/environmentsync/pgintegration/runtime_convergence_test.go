package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func assertWorkloadRuntimeConvergence(t *testing.T, basic gitOpsTestStore, lease state.EnvironmentGitOpsLease,
	desired environmentsync.DesiredState, ready bool) {
	t.Helper()
	targets, err := basic.(state.EnvironmentGitOpsRuntimeStore).ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
	if err != nil || len(targets) != len(desired.Definition.Workloads) {
		t.Fatalf("runtime targets: %d %v", len(targets), err)
	}
	for _, target := range targets {
		if target.Ready() != ready || (target.UnqualifiedWorkloads == 0) != ready {
			t.Fatalf("runtime target readiness=%t: %+v", ready, target)
		}
	}
	if !ready {
		plan := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
		raw, _ := json.Marshal(plan)
		now := time.Now().UTC()
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", now, now.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unfinished graph reported convergence: %v", err)
		}
	}
}

func serveRuntimeConvergenceGraph(t *testing.T, basic gitOpsTestStore) (state.EnvironmentGitOpsLease, environmentsync.DesiredState) {
	t.Helper()
	lease, requests := preparedRequestModeQualificationFixture(t, basic)
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
	assertWorkloadRuntimeConvergence(t, basic, lease, desired, false)
	for _, request := range requests {
		completeRequestQualificationReceipts(t, basic, request)
	}
	assertWorkloadRuntimeConvergence(t, basic, lease, desired, false)
	if _, active, err := basic.(state.EnvironmentGitOpsGraphActivationStore).ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !active {
		t.Fatalf("activate: %t %v", active, err)
	}
	assertWorkloadRuntimeConvergence(t, basic, lease, desired, false)
	gateways := []string{"runtime-edge-a", "runtime-edge-b"}
	for _, gateway := range gateways {
		registerServingGateway(t, basic, gateway)
	}
	serving := basic.(state.EnvironmentGitOpsWorkloadServingStore)
	receipt, _, err := serving.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, gateways)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range receipt.Routes {
		for _, gateway := range gateways {
			_, complete, err := serving.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID, route.Generation, gateway)
			if err != nil {
				t.Fatal(err)
			}
			assertWorkloadRuntimeConvergence(t, basic, lease, desired, complete)
		}
	}
	evidence, err := basic.(state.EnvironmentGitOpsActivationEvidenceStore).EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
	if err != nil || !evidence.Qualified || !evidence.Activated || !evidence.Serving {
		t.Fatalf("graph is not fully serving: %+v %v", evidence, err)
	}
	return lease, desired
}

func TestEnvironmentGitOpsRuntimeConvergesAfterWholeGraphServing(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, desired := serveRuntimeConvergenceGraph(t, basic)
		plan := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
		if !plan.CanApply() || plan.HasDrift() {
			t.Fatal("serving graph still has intent drift")
		}
		raw, _ := json.Marshal(plan)
		now := time.Now().UTC()
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", now, now.Add(time.Minute)); err != nil {
			t.Fatalf("serving graph cannot finish converged: %v", err)
		}
		source, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil || source.AppliedRevisionID != lease.Revision.ID {
			t.Fatalf("serving revision was not recorded as applied: %v", err)
		}
	})
}

func TestEnvironmentGitOpsRuntimeRejectsStaleServingEvidence(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, desired := serveRuntimeConvergenceGraph(t, basic)
		apps, err := basic.ListApps(t.Context(), lease.Source.AccountID)
		if err != nil || len(apps) == 0 {
			t.Fatal("mapped apps unavailable", err)
		}
		if err := basic.UpsertAppEnvInScope(t.Context(), lease.Source.AccountID, apps[0].ID, lease.Source.EnvironmentSlug, "MANUAL", "changed"); err != nil {
			t.Fatal(err)
		}
		assertWorkloadRuntimeConvergence(t, basic, lease, desired, false)
	})
}

func TestEnvironmentGitOpsServingGraphRequiresFreshResidents(t *testing.T) {
	for _, residentState := range []state.State{state.StateWaking, state.StateRunning} {
		t.Run(string(residentState), func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, desired := serveRuntimeConvergenceGraph(t, basic)
				release, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
				if err != nil || len(release.Members) == 0 {
					t.Fatal("active release unavailable", err)
				}
				deployment, err := basic.DeploymentByID(t.Context(), release.Members[0].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				app, err := basic.AppByID(t.Context(), deployment.AppID)
				if err != nil {
					t.Fatal(err)
				}
				instance := runtimeInstance(t, basic, app, deployment, state.StateWaking)
				if residentState != state.StateWaking {
					// Readiness alone does not acknowledge the inputs sent to the guest.
					if err := basic.UpdateInstanceState(t.Context(), instance.ID, string(residentState)); err != nil {
						t.Fatal(err)
					}
				}
				runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
				targets, err := runtime.ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, target := range targets {
					if target.AppID == app.ID {
						found = true
						if target.StaleResidents != 1 || target.Ready() || residentState == state.StateWaking && target.StartingResidents != 1 {
							t.Fatalf("serving proof excused an unacknowledged resident: %+v", target)
						}
					}
				}
				if !found {
					t.Fatal("source/runtime-only workload missing from fleet observation")
				}
				plan := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
				raw, _ := json.Marshal(plan)
				now := time.Now().UTC()
				if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", now, now.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("unacknowledged resident did not block convergence: %v", err)
				}
			})
		})
	}
}

func TestPgEnvironmentGitOpsRuntimeFreshnessMigrationPreservesPopulatedScope(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, _ := serveRuntimeConvergenceGraph(t, store)
	release, err := store.ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil || len(release.Members) == 0 {
		t.Fatal("active release unavailable", err)
	}
	deployment, err := store.DeploymentByID(t.Context(), release.Members[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.AppByID(t.Context(), deployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeInstance(t, store, app, deployment, state.StateWaking)
	raw, err := migrations.FS.ReadFile("20261103201000001_gitops_workload_runtime_freshness.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("migration is missing rollback")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, step := range []struct {
		sql  string
		want int
	}{{down, 0}, {up, 1}} {
		if _, err := tx.Exec(t.Context(), step.sql); err != nil {
			t.Fatal("view migration replay", err)
		}
		var observed int
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM environment_gitops_runtime_targets
			WHERE source_id=$1 AND app_id=$2 AND stale_residents=1 AND starting_residents=1`, lease.Source.ID, app.ID).Scan(&observed); err != nil || observed != step.want {
			t.Fatalf("populated scope observation=%d want=%d: %v", observed, step.want, err)
		}
	}
}

func TestEnvironmentGitOpsRemovedWorkloadRuntimeEffectRecovery(t *testing.T) {
	for _, scenario := range []string{"cold", "resident", "report"} {
		t.Run(scenario, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				ctx := t.Context()
				_, old, desired, app, _, instance := runtimeFixture(t, basic, scenario != "cold")
				runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
				pending, err := runtime.PendingEnvironmentGitOpsRuntime(ctx, old)
				if err != nil || len(pending) != 1 {
					t.Fatalf("pending fixture: %d %v", len(pending), err)
				}
				current, err := basic.EnvironmentGitSource(ctx, old.Source.AccountID, old.Source.ProjectID, old.Source.EnvironmentSlug)
				if err != nil {
					t.Fatal(err)
				}
				definition := desired.Definition
				definition.Workloads = map[string]api.EnvironmentWorkload{}
				definition.WorkloadPruningPolicy = "retain"
				next, err := environmentsync.Compile(definition)
				if err != nil {
					t.Fatal(err)
				}
				current, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(current, next, strings.Repeat("f", 40)))
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "report" {
					current, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(ctx, current.AccountID, current.ID,
						state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Mode: "report"})
					if err != nil {
						t.Fatal(err)
					}
				}
				fresh, err := basic.ClaimEnvironmentGitOps(ctx, uuid.NewString(), time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.ReconcileEnvironmentGitOpsRuntime(ctx, old, pending[0].ID); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("old lease retained recovery authority: %v", err)
				}
				if targets, err := runtime.ObserveEnvironmentGitOpsRuntime(ctx, fresh); err != nil || len(targets) != 0 {
					t.Fatalf("removed workload entered the desired runtime: %d %v", len(targets), err)
				}
				progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(ctx, fresh, pending[0].ID)
				if err != nil || progress.Ready != (scenario == "cold") || scenario == "report" && progress.Request != nil {
					t.Fatalf("original scope recovery: %+v %v", progress, err)
				}
				if progress.Request != nil && (progress.Request.AppID != app.ID || progress.Request.Scope != old.Source.EnvironmentSlug) {
					t.Fatal("recovery changed the effect's original scope")
				}
				if scenario != "cold" {
					remaining, err := runtime.PendingEnvironmentGitOpsRuntime(ctx, fresh)
					if err != nil || len(remaining) != 1 || remaining[0].ID != pending[0].ID || remaining[0].Generation != old.Source.Generation {
						t.Fatalf("stale original residents lost their durable effect: %+v %v", remaining, err)
					}
					if err := basic.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
						t.Fatal(err)
					}
					if progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(ctx, fresh, pending[0].ID); err != nil || !progress.Ready {
						t.Fatalf("drained original scope did not finish: %+v %v", progress, err)
					}
				}
				if remaining, err := runtime.PendingEnvironmentGitOpsRuntime(ctx, fresh); err != nil || len(remaining) != 0 {
					t.Fatalf("runtime work remains stranded: %d %v", len(remaining), err)
				}
				if current.Spec.Mode != "report" {
					current, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(ctx, current.AccountID, current.ID,
						state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Mode: "report"})
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := basic.(state.EnvironmentGitOpsLifecycleStore).DetachEnvironmentGitSource(ctx, current.AccountID, current.ID, current.Generation); err != nil {
					t.Fatalf("completed recovery still blocks source detach: %v", err)
				}
			})
		})
	}
}
