package pgintegration_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// These fixtures exercise owner/state handoffs, not native VM qualification.
func bindingRetirementFixture(t *testing.T, basic gitOpsTestStore, kind string) (state.EnvironmentGitOpsLease, environmentsync.DesiredState, environmentsync.Plan, []state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	lease, plan, requests, placement := candidateBindingFixture(t, basic, kind, "")
	store := basic.(candidateBindingTestStore)
	completeCandidateBindingCohort(t, store, requests, placement, true)
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !active {
		t.Fatalf("initial checked activation: %t %v", active, err)
	}
	role, target := "compute-only", "tcp://127.0.0.1:8080"
	for _, edge := range []string{"retirement-edge-a", "retirement-edge-b"} {
		if _, err := basic.CreateComputeNode(t.Context(), state.ComputeNode{Name: edge, Active: true, Role: &role, GatewayTargetURL: &target,
			TargetURL: "tcp://127.0.0.1:50051", MemMB: 8192, AdmissionCeilingMB: 4096, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5}); err != nil {
			t.Fatal(err)
		}
	}
	serving := basic.(state.EnvironmentGitOpsWorkloadServingStore)
	receipt, _, err := serving.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"retirement-edge-a", "retirement-edge-b"})
	if err != nil {
		t.Fatal("prepare serving", err)
	}
	for _, route := range receipt.Routes {
		for _, edge := range []string{"retirement-edge-a", "retirement-edge-b"} {
			if _, _, err := serving.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID, route.Generation, edge); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The inherited fixture baselines have no executions. Finish their prior
	// scheduler drain so only the managed cohort is live in this environment.
	for _, request := range requests {
		deps, err := basic.ListDeploymentsForApp(t.Context(), request.AppID, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range deps {
			if d.Scope == "production" && d.EnvironmentWorkloadRuntime == "" && d.Status == state.DeployLive {
				if err := basic.UpdateDeploymentStatus(t.Context(), d.ID, state.DeploySuperseded, ""); err != nil {
					t.Fatal("baseline drain", err)
				}
			}
		}
	}
	prune := true
	source, err := basic.(intentTestStore).UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Prune: &prune})
	if err != nil {
		t.Fatal("enable pruning", err)
	}
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	definition.Workloads, definition.WorkloadPruningPolicy = map[string]api.EnvironmentWorkload{}, "retain"
	definition.QueuePruningPolicy = "retain"
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("f", 40))); err != nil {
		t.Fatal("approve removal", err)
	}
	lease, err = basic.ClaimEnvironmentGitOps(t.Context(), "binding-retirement", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal("claim removal", err)
	}
	retirement := basic.(state.EnvironmentGitOpsWorkloadRetirementStore)
	reviewed := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
	if _, err := retirement.BeginEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, reviewed); err != nil {
		t.Fatalf("reserve removal: %v blockers=%v", err, reviewed.BlockingReasons)
	}
	return lease, desired, claimedIntentPlan(t, basic.(intentTestStore), lease, desired), requests
}

func TestBindingRetirementCompleteGraph(t *testing.T) {
	for _, kind := range []string{"", "dockerfile"} {
		t.Run("source="+kind, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, desired, plan, requests := bindingRetirementFixture(t, basic, kind)
				retirement := basic.(state.EnvironmentGitOpsWorkloadRetirementStore)
				var sentinel state.Invocation
				var queue state.QueueBinding
				for _, r := range requests {
					if r.Resource != "workload/worker" {
						continue
					}
					queues, err := basic.ListQueueBindingsForApp(t.Context(), lease.Source.AccountID, r.AppID)
					if err != nil || len(queues) != 1 {
						t.Fatal("worker queue", err)
					}
					queue = queues[0]
					sentinel, err = basic.EnqueueInvocation(t.Context(), state.Invocation{AccountID: lease.Source.AccountID, AppID: r.AppID, Source: state.InvocationQueue,
						QueueName: queue.QueueName, QueueBindingID: queue.ID, DeploymentScope: "production", Payload: json.RawMessage(`{"keep":"backlog"}`), DueAt: time.Now().Add(time.Hour)})
					if err != nil {
						t.Fatal(err)
					}
				}
				if ready, err := retirement.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, plan); err != nil || !ready {
					t.Fatalf("checked retirement: %t %v", ready, err)
				}
				active, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
				if err != nil || !active.Active || len(active.Members) != 0 {
					t.Fatal("removal did not publish an empty graph", err)
				}
				for _, r := range requests {
					policy, err := basic.(state.BindingReleasePolicyStore).GetBindingReleasePolicy(t.Context(), lease.Source.AccountID, r.AppID, "production")
					if err != nil || policy.Mode != "enforce" || policy.Revision != 1 {
						t.Fatalf("policy changed during retirement: %+v %v", policy, err)
					}
					d, err := basic.DeploymentByID(t.Context(), r.DeploymentID)
					if err != nil || d.TrafficPercent != 0 || d.Status != state.DeployLive {
						t.Fatal("cutover skipped withdrawal/drain boundary", err)
					}
				}
				if ready, err := retirement.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || ready {
					t.Fatal("retired before gateway withdrawal", err)
				}
				pending, err := retirement.PendingEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, []string{"retirement-edge-a", "retirement-edge-b"})
				if err != nil || len(pending) != 2 {
					t.Fatal("withdrawal identities", err)
				}
				for _, r := range pending {
					if err := retirement.AcknowledgeEnvironmentGitOpsWorkloadRetirement(t.Context(), lease, r.ID, r.RouteGeneration, "retirement-edge-a"); err != nil {
						t.Fatal(err)
					}
				}
				if ready, err := retirement.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || ready {
					t.Fatal("partial gateway roster certified retirement", err)
				}
				for _, r := range pending {
					if err := retirement.AcknowledgeEnvironmentGitOpsWorkloadRetirement(t.Context(), lease, r.ID, r.RouteGeneration, "retirement-edge-b"); err != nil {
						t.Fatal(err)
					}
				}
				if ready, err := retirement.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || !ready {
					t.Fatal("drained retirement", err)
				}
				unchanged, err := basic.InvocationByID(t.Context(), sentinel.ID)
				if err != nil || unchanged.State != sentinel.State || unchanged.Attempts != 0 || string(unchanged.Payload) != string(sentinel.Payload) {
					t.Fatal("retirement consumed or deleted backlog", err)
				}
				if retained, err := basic.(state.QueueBindingHistoryStore).QueueBindingHistoryByID(t.Context(), lease.Source.AccountID, queue.AppID, queue.ID); err != nil || retained.RetiredAt == nil || retained.Enabled {
					t.Fatal("retirement deleted retained queue", err)
				}
				consumers, err := basic.ListTriggersForApp(t.Context(), queue.AppID)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range consumers {
					if c.Enabled {
						t.Fatal("retired worker consumer is still enabled")
					}
				}
				replay := claimedIntentPlan(t, basic.(intentTestStore), lease, desired)
				if ready, err := retirement.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, replay); err != nil || !ready {
					t.Fatal("retirement replay", err)
				}
			})
		})
	}
}

func TestBindingRetirementRejectsChangedAuthority(t *testing.T) {
	for _, change := range []string{"review", "policy", "inventory", "survivor", "lease", "source"} {
		t.Run(change, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, desired, plan, requests := bindingRetirementFixture(t, basic, "")
				previous, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "review":
					plan.Hash = strings.Repeat("0", 64)
				case "policy":
					store := basic.(state.BindingReleasePolicyStore)
					p, err := store.GetBindingReleasePolicy(t.Context(), lease.Source.AccountID, requests[0].AppID, "production")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.SetBindingReleasePolicy(t.Context(), lease.Source.AccountID, p.AppID, p.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &p.Revision, MaxVerificationAge: "2m", QueueRequirements: p.QueueRequirements}); err != nil {
						t.Fatal(err)
					}
				case "survivor":
					if _, err := basic.CreateApp(t.Context(), state.App{AccountID: lease.Source.AccountID, ProjectID: lease.Source.ProjectID, Slug: "surviving-caller", WorkloadName: "survivor", Type: state.AppTypeApp, Status: state.AppActive,
						RAMMB: 512, MaxConcurrency: 1, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}}); err != nil {
						t.Fatal(err)
					}
				case "inventory":
					for _, r := range requests {
						if r.Resource == "workload/worker" {
							if _, err := basic.CreateQueueBinding(t.Context(), state.QueueBinding{AccountID: lease.Source.AccountID, AppID: r.AppID, DeploymentScope: "production", Name: "unreviewed", QueueName: "unreviewed", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
								t.Fatal("change binding inventory", err)
							}
						}
					}
				case "lease":
					if err := basic.RenewEnvironmentGitOps(t.Context(), lease, time.Now(), time.Millisecond); err != nil {
						t.Fatal(err)
					}
					<-time.After(5 * time.Millisecond)
				case "source":
					if _, _, err := basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(lease.Source, desired, strings.Repeat("a", 40))); err != nil {
						t.Fatal(err)
					}
				}
				retirement := basic.(state.EnvironmentGitOpsWorkloadRetirementStore)
				if ready, err := retirement.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, plan); err == nil || ready {
					t.Fatal("changed authority withdrew execution")
				}
				current, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
				if err != nil || current.ID != previous.ID {
					t.Fatal("failed retirement replaced the active graph", err)
				}
				for _, r := range requests {
					blocked, err := basic.(state.EnvironmentWorkloadRetirementAdmissionStore).EnvironmentWorkloadAdmissionRetired(t.Context(), r.AppID, "production")
					if err != nil || blocked {
						t.Fatal("failed retirement left admission closed", err)
					}
					d, err := basic.DeploymentByID(t.Context(), r.DeploymentID)
					if err != nil || d.Status != state.DeployLive || r.Resource == "workload/api" && d.TrafficPercent != 100 {
						t.Fatal("failed retirement changed serving deployment", err)
					}
				}
			})
		})
	}
}

func TestPgBindingRetirementAuditIsImmutable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, _, plan, _ := bindingRetirementFixture(t, store, "")
	if ready, err := store.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, plan); err != nil || !ready {
		t.Fatal("retirement", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM environment_binding_retirement_receipts WHERE source_id=$1 AND revision_id=$2 AND plan_hash=$3`, lease.Source.ID, lease.Revision.ID, plan.Hash).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing exact retirement audit", err)
	}
	for _, query := range []string{
		`UPDATE environment_binding_retirement_receipts SET plan_hash=repeat('0',64) WHERE source_id=$1`,
		`DELETE FROM environment_binding_retirement_receipts WHERE source_id=$1`,
		`UPDATE environment_workload_retirements SET binding_policy_context='{}' WHERE source_id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, lease.Source.ID); err == nil {
			t.Fatal("retirement authority/history was mutable")
		}
	}
	source, err := os.ReadFile("../../../migrations/20261017210000001_checked_binding_retirement.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded checked retirement history")
	}
	active, err := store.ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"policy", "membership"} {
		t.Run("deferred_"+change, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(t.Context(), `SELECT set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
				t.Fatal(err)
			}
			next := uuid.NewString()
			if _, err := tx.Exec(t.Context(), `SELECT authorize_environment_binding_retirement($1,$2,$3,$4)`, lease.Source.ID, next, active.ID, plan.Hash); err != nil {
				t.Fatal("prepare exact empty grant", err)
			}
			if _, err := tx.Exec(t.Context(), `UPDATE project_release_sets SET active=false,expires_at=clock_timestamp()+ttl_seconds*interval '1 second' WHERE id=$1`, active.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO project_release_sets(id,account_id,project_id,environment_slug,active,ttl_seconds) VALUES($1,$2,$3,'production',true,3600)`, next, lease.Source.AccountID, lease.Source.ProjectID); err != nil {
				t.Fatal(err)
			}
			if change == "policy" {
				if _, err := tx.Exec(t.Context(), `UPDATE app_binding_release_policies SET revision=revision+1,max_age_seconds=max_age_seconds+1 WHERE app_id=(SELECT app_id FROM environment_workload_retirements WHERE source_id=$1 ORDER BY app_id LIMIT 1) AND scope='production'`, lease.Source.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := tx.Exec(t.Context(), `INSERT INTO project_release_members(release_id,app_id,deployment_id) SELECT $1,app_id,unnest(deployment_ids) FROM environment_workload_retirements WHERE source_id=$2 LIMIT 1`, next, lease.Source.ID); err != nil {
					t.Fatal("stage invalid membership for deferred validation", err)
				}
			}
			if err := tx.Commit(t.Context()); err == nil {
				t.Fatal("changed authority committed using an empty graph grant")
			}
		})
	}
}
