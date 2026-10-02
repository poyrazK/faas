package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func workloadGraphFixture(t *testing.T, basic gitOpsTestStore) (state.EnvironmentGitOpsLease, environmentsync.Plan, state.QueueBindingConsumerResult) {
	t.Helper()
	store, source, desired, _, _, _ := workloadIntentFixture(t, basic, "enforce")
	worker, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-worker", WorkloadName: "worker", Type: state.AppTypeApp,
		Status: state.AppActive, WorkloadClass: state.WorkloadClassWorker, RAMMB: 512, MaxConcurrency: 1, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	servingWorker, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: worker.ID, Scope: "production", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, ImageDigest: "registry.example/worker@sha256:" + strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), servingWorker.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	queue, err := basic.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: source.AccountID, AppID: worker.ID,
		DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	desired.Definition.Workloads["worker"] = api.EnvironmentWorkload{App: worker.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/worker@sha256:" + strings.Repeat("e", 64)},
		Runtime: json.RawMessage(`{"execution_mode":"worker"}`), QueueBindings: map[string]api.EnvironmentQueueBinding{
			"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 1}}}
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40))); err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store, source)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "graph-preparer", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
		t.Fatal(err)
	}
	return lease, claimedIntentPlan(t, store, lease, desired), queue
}

func TestEnvironmentGitOpsWorkloadGraphPreparationRequiresWholeCohort(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, queue := workloadGraphFixture(t, basic)
		preparer, graphs := basic.(state.EnvironmentGitOpsPreparationStore), basic.(state.EnvironmentGitOpsGraphPreparationStore)
		if _, err := graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("missing graph: %v", err)
		}
		candidates, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || len(candidates) != 2 {
			t.Fatalf("prepare API and worker: %+v %v", candidates, err)
		}
		graph, err := graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
		if err != nil || graph.ID == "" || graph.Phase != "preparing" || len(graph.Members) != 2 || graph.EnvironmentID != lease.Source.EnvironmentID ||
			graph.RevisionID != lease.Revision.ID || graph.ResourceIDs["workload/worker/queue_bindings/orders"] != queue.Binding.ID || graph.ResourceIDs["workload/worker/queue_bindings/orders/consumer"] != queue.Changes[0].TriggerID {
			t.Fatalf("complete graph identity: %+v %v", graph, err)
		}
		original := graph
		graph.Members[0].AppID = "caller-mutated"
		graph.ResourceIDs["workload/worker/queue_bindings/orders/consumer"] = "caller-mutated"
		for i, candidate := range candidates {
			if err := basic.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "reviewed-"+candidate.Resource, 4096); err != nil {
				t.Fatal(err)
			}
			if err := basic.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			graph, err = graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
			wantPhase := "preparing"
			if i == len(candidates)-1 {
				wantPhase = "prepared"
			}
			if err != nil || graph.ID != original.ID || graph.Phase != wantPhase || graph.Members[0].AppID == "caller-mutated" || graph.ResourceIDs["workload/worker/queue_bindings/orders/consumer"] != queue.Changes[0].TriggerID {
				t.Fatalf("preparation cohort phase: %+v %v", graph, err)
			}
			if err := basic.MarkDeploymentLive(t.Context(), candidate.DeploymentID); err == nil {
				t.Fatal("prepared artifact became a serving deployment")
			}
		}
		if graph.PreparedAt == nil {
			t.Fatal("complete cohort has no durable preparation boundary")
		}
		retry, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || !reflect.DeepEqual(retry, candidatesWithArtifacts(candidates)) {
			t.Fatalf("retry changed candidate cohort: %+v %v", retry, err)
		}
		again, err := graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
		if err != nil || !reflect.DeepEqual(again, graph) {
			t.Fatalf("retry changed prepared graph: %+v %v", again, err)
		}
		planJSON, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "converged", planJSON, json.RawMessage(`[]`), "", time.Now(), time.Now().Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("prepared artifacts claimed serving convergence: %v", err)
		}
		if err := basic.UpdateDeploymentStatus(t.Context(), candidates[0].DeploymentID, state.DeployFailed, "image storage failure"); err != nil {
			t.Fatal(err)
		}
		failed, err := graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
		if err != nil || failed.Phase != "failed" || failed.ErrorCode != "environment_workload_artifact_failed" || failed.PreparedAt != nil {
			t.Fatalf("failed member did not stop the cohort: %+v %v", failed, err)
		}
	})
}

func candidatesWithArtifacts(candidates []state.EnvironmentWorkloadCandidate) []state.EnvironmentWorkloadCandidate {
	copy := append([]state.EnvironmentWorkloadCandidate(nil), candidates...)
	for i := range copy {
		copy[i].Status, copy[i].HasRootfs = state.DeploySnapshotting, true
	}
	return copy
}

func TestPgEnvironmentGitOpsWorkloadGraphAtomicPublicationAndMutationFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, _ := workloadGraphFixture(t, store)
	if _, err := pool.Exec(t.Context(), `create function reject_graph_publication() returns trigger language plpgsql as $$ begin raise exception 'injected graph publication failure'; end $$;
 create trigger reject_graph_publication before insert on environment_workload_graphs for each row execute function reject_graph_publication()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err == nil || !strings.Contains(err.Error(), "injected graph publication") {
		t.Fatalf("graph publication failure: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `select count(*) from deployments where environment_workload_runtime->>'source_id'=$1`, lease.Source.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("graph failure left candidate work: %d %v", count, err)
	}
	if err := pool.QueryRow(t.Context(), `select count(*) from notification_outbox where channel='environment_workload_image'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("graph failure left an imaging handoff: %d %v", count, err)
	}
	if _, err := pool.Exec(t.Context(), `drop trigger reject_graph_publication on environment_workload_graphs`); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("publication recovery: %+v %v", candidates, err)
	}
	graph, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workload/worker/queue_bindings/orders", "workload/worker/queue_bindings/orders/consumer"} {
		resources := make(map[string]string, len(graph.ResourceIDs))
		for name, id := range graph.ResourceIDs {
			resources[name] = id
		}
		resources[key] = graph.Members[0].AppID
		rawIDs, _ := json.Marshal(resources)
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `insert into environment_workload_graphs(source_id,environment_id,revision_id,generation,intent_version,plan_hash,definition_digest,members,resource_ids)
 select source_id,environment_id,revision_id,generation,intent_version,plan_hash,definition_digest,members,$2::jsonb from environment_workload_graphs where id=$1`, graph.ID, rawIDs)
		_ = tx.Rollback(t.Context())
		if err == nil || !strings.Contains(err.Error(), "queue and consumer identities") {
			t.Fatalf("current lease captured substituted queue identity %s: %v", key, err)
		}
	}
	for _, statement := range []string{
		`update environment_workload_graphs set members='[]'::jsonb where id=$1`,
		`update environment_workload_graphs set resource_ids='{}'::jsonb where id=$1`,
		`update environment_workload_graphs set generation=generation+1 where id=$1`,
		`update environment_workload_graphs set phase='prepared',prepared_at=now() where id=$1`,
		`update environment_workload_graphs set phase='active' where id=$1`,
		`delete from environment_workload_graphs where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), statement, graph.ID); err == nil {
			t.Fatalf("SQL bypassed graph boundary: %s", statement)
		}
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), `update environment_workload_graphs set phase='prepared',prepared_at=now() where id=$1`, graph.ID)
	_ = tx.Rollback(t.Context())
	if err == nil || !strings.Contains(err.Error(), "artifact phase") {
		t.Fatalf("current lease bypassed cohort artifact requirement: %v", err)
	}
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(lease.Source, desired, strings.Repeat("f", 40))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("superseded graph advanced: %v", err)
	}
}

func TestPgEnvironmentGitOpsWorkloadGraphPopulatedReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, _ := workloadGraphFixture(t, store)
	candidates, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if err := store.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "reviewed-"+candidate.Resource, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	original, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
	if err != nil || original.Phase != "prepared" {
		t.Fatalf("prepared journal: %+v %v", original, err)
	}
	if _, err := pool.Exec(t.Context(), `delete from goose_db_version where version_id=20261002090001000`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	again, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
	if err != nil || !reflect.DeepEqual(again, original) {
		t.Fatalf("replay changed prepared cohort: %+v %v", again, err)
	}
	for _, candidate := range candidates {
		if err := store.MarkDeploymentLive(t.Context(), candidate.DeploymentID); err == nil {
			t.Fatal("replayed artifact escaped the preparation hold")
		}
	}
}

func TestPgEnvironmentGitOpsWorkloadGraphParentPurges(t *testing.T) {
	for _, parent := range []string{"project", "account"} {
		t.Run(parent, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			lease, plan, _ := workloadGraphFixture(t, store)
			if _, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err != nil {
				t.Fatal(err)
			}
			graph, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
			if err != nil {
				t.Fatal(err)
			}
			if parent == "project" {
				if err := store.DeleteProject(t.Context(), lease.Source.ProjectID); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := store.DeleteAccount(t.Context(), lease.Source.AccountID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("active account purge: %v", err)
				}
				if current, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || current.ID != graph.ID {
					t.Fatalf("active account purge discarded journal: %+v %v", current, err)
				}
				if err := store.MarkAccountDeletionPending(t.Context(), lease.Source.AccountID); err != nil {
					t.Fatal(err)
				}
				if err := store.DeleteAccount(t.Context(), lease.Source.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := pool.QueryRow(t.Context(), `select count(*) from environment_workload_graphs where id=$1`, graph.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("parent purge retained graph: %d %v", count, err)
			}
		})
	}
}
