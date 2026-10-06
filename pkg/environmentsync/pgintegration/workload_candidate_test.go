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

func appliedImageCandidateFixture(t *testing.T, basic gitOpsTestStore) (intentTestStore, state.EnvironmentGitOpsLease, environmentsync.DesiredState, state.App, state.Deployment, environmentsync.Plan) {
	t.Helper()
	store, source, desired, app, previous, _ := workloadIntentFixture(t, basic, "enforce")
	adoptWorkloadIntent(t, store, source)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "image-preparer", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	return store, lease, desired, app, previous, plan
}

func TestEnvironmentGitOpsImageCandidatesFreezeInputsAndHoldExecution(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, desired, app, previous, plan := appliedImageCandidateFixture(t, basic)
		preparer := basic.(state.EnvironmentGitOpsPreparationStore)
		candidates, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("prepare: %+v %v", candidates, err)
		}
		candidate := candidates[0]
		dep, err := store.DeploymentByID(t.Context(), candidate.DeploymentID)
		if err != nil || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeployPending || dep.ImageDigest != desired.Definition.Workloads["api"].Source.Image || dep.TrafficPercent != 0 || candidate.HasRootfs {
			t.Fatalf("held candidate: %+v %v", dep, err)
		}
		frozen, err := dep.ScopedWorkloadRuntime()
		if err != nil || frozen.EnvironmentID != lease.Source.EnvironmentID || frozen.SourceID != lease.Source.ID || frozen.RevisionID != lease.Revision.ID || frozen.Generation != lease.Source.Generation || frozen.Resource != "workload/api" || frozen.PlanHash != plan.Hash || frozen.Baseline.Port != 8079 || string(frozen.Runtime["port"]) != "8080" {
			t.Fatalf("frozen authority: %+v %v", frozen, err)
		}
		// Persisted source and artifact identity must remain the same even if a
		// row is damaged outside the store APIs. Image candidates have no source
		// archive, so their digest and deployment kind are the provenance fence.
		otherImage := "registry.example/other@sha256:" + strings.Repeat("f", 64)
		changedSource := *frozen
		changedSource.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: otherImage}
		changedSourceJSON, _ := json.Marshal(changedSource)
		for name, mutate := range map[string]func(*state.Deployment){
			"frozen source digest":    func(candidate *state.Deployment) { candidate.EnvironmentWorkloadRuntime = string(changedSourceJSON) },
			"deployment image digest": func(candidate *state.Deployment) { candidate.ImageDigest = otherImage },
			"deployment kind":         func(candidate *state.Deployment) { candidate.Kind = state.DeploymentKindGitHub },
			"orphan source metadata":  func(candidate *state.Deployment) { candidate.SourcePath = "/tmp/unreviewed.tar.gz" },
		} {
			t.Run("source identity "+name, func(t *testing.T) {
				corrupted := dep
				mutate(&corrupted)
				if _, err := corrupted.ScopedWorkloadRuntime(); err == nil {
					t.Fatal("corrupted held candidate retained valid source authority")
				}
			})
		}
		for _, promote := range []func() error{
			func() error { return basic.(state.Store).MarkDeploymentLive(t.Context(), dep.ID) },
			func() error { return store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, "") },
		} {
			if err := promote(); err == nil {
				t.Fatal("unqualified candidate became live")
			}
		}
		if _, err := basic.(state.Store).CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 512, "", ""); err == nil {
			t.Fatal("unqualified candidate booted")
		}
		if _, err := store.CreateDeployment(t.Context(), dep); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("ordinary creation accepted frozen inputs: %v", err)
		}
		if _, err := basic.(state.Store).PrepareDeploymentRollback(t.Context(), app.ID, dep.ID); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("ordinary rollback accepted an unqualified candidate: %v", err)
		}
		if err := basic.(state.Store).SetDeploymentRootfs(t.Context(), dep.ID, "/tmp/candidate.ext4", "candidate.ext4", 4096); err != nil {
			t.Fatal(err)
		}
		retried, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || len(retried) != 1 || retried[0].DeploymentID != dep.ID || !retried[0].HasRootfs {
			t.Fatalf("retry lost durable candidate/artifact: %+v %v", retried, err)
		}
		if changed, err := store.CompareAndSetAppStatus(t.Context(), app.ID, state.AppActive, state.AppEvictedCold); err != nil || !changed {
			t.Fatalf("park app: %v %v", changed, err)
		}
		if parked, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err != nil || len(parked) != 1 || parked[0].DeploymentID != dep.ID {
			t.Fatalf("runtime eviction changed candidate identity: %+v %v", parked, err)
		}
		live, err := basic.(state.Store).LiveDeploymentForScope(t.Context(), app.ID, "production")
		if err != nil || live.ID != previous.ID {
			t.Fatalf("preparation replaced serving deployment: %+v %v", live, err)
		}
		current, _ := store.AppByID(t.Context(), app.ID)
		if !reflect.DeepEqual(current.Manifest, app.Manifest) {
			t.Fatal("preparation mutated shared app settings")
		}
		targets, err := basic.(state.EnvironmentGitOpsRuntimeStore).ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(targets) != 1 || targets[0].Ready() || targets[0].UnqualifiedWorkloads != 1 {
			t.Fatalf("candidate inputs were treated as serving proof: %+v %v", targets, err)
		}
		// An app change invalidates the reviewed baseline. Already-created
		// candidates continue using the old input rather than the new global row.
		command := "./changed-after-review"
		if _, err := basic.(state.Store).UpdateApp(t.Context(), app.ID, state.UpdateAppParams{StartCommand: &command}); err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale preparation review accepted: %v", err)
		}
		changed, _ := store.AppByID(t.Context(), app.ID)
		resolved, err := state.AppForDeploymentRuntime(changed, dep)
		if err != nil || resolved.StartCommand != "" || resolved.Manifest.Port != 8080 || resolved.Manifest.StopGracePeriodS != 20 {
			t.Fatalf("candidate inherited post-review settings: %+v %v", resolved, err)
		}
		// A revoked generation cannot prepare additional rows.
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(lease.Source, desired, strings.Repeat("b", 40))); err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded preparer wrote a candidate: %v", err)
		}
	})
}

func TestPgEnvironmentGitOpsImageCandidateDatabaseFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, lease, _, app, _, plan := appliedImageCandidateFixture(t, store)
	candidates, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare: %+v %v", candidates, err)
	}
	id := candidates[0].DeploymentID
	for _, query := range []string{
		`update deployments set environment_workload_runtime=null where id=$1`,
		`update deployments set environment_workload_runtime=jsonb_set(environment_workload_runtime,'{runtime,port}','9090') where id=$1`,
		`update deployments set override_port=9090 where id=$1`,
		`update deployments set scope='staging' where id=$1`,
		`update deployments set status='live' where id=$1`,
		`update deployments set image_digest='registry.example/escape' where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, id); err == nil {
			t.Fatalf("raw SQL bypassed candidate fence: %s", query)
		}
	}
	if _, err := pool.Exec(t.Context(), `insert into instances(app_id,deployment_id,state,ram_mb) values($1,$2,'cold_booting',512)`, app.ID, id); err == nil || !strings.Contains(err.Error(), "current qualification attempt") {
		t.Fatalf("raw instance insert escaped hold: %v", err)
	}
	dep, _ := store.DeploymentByID(t.Context(), id)
	frozen, _ := dep.ScopedWorkloadRuntime()
	frozen.EnvironmentID = strings.Repeat("0", 8) + "-0000-0000-0000-" + strings.Repeat("0", 12)
	raw, _ := json.Marshal(frozen)
	if _, err := pool.Exec(t.Context(), `insert into deployments(app_id,scope,kind,image_digest,status,environment_workload_runtime) values($1,'production','image',$2,'pending',$3)`, app.ID, dep.ImageDigest, raw); err == nil {
		t.Fatal("candidate inserted without current lease/original scope")
	}
	if err := store.UpdateDeploymentStatus(t.Context(), id, state.DeployFailed, "injected build failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryDeploymentFromStage(t.Context(), id, state.StageImageBuild); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("ordinary retry escaped graph authority: %v", err)
	}
}

func TestPgEnvironmentGitOpsImageCandidateHandoffRollsBackAndDeduplicates(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, lease, _, app, previous, plan := appliedImageCandidateFixture(t, store)
	if _, err := pool.Exec(t.Context(), `create function fail_candidate_handoff() returns trigger language plpgsql as $$ begin
 if NEW.channel='environment_workload_image' then raise exception 'injected candidate handoff failure'; end if; return NEW; end $$;
 create trigger fail_candidate_handoff before insert on notification_outbox for each row execute function fail_candidate_handoff()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err == nil || !strings.Contains(err.Error(), "injected candidate") {
		t.Fatalf("handoff failure was hidden: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `select count(*) from deployments where app_id=$1 and environment_workload_runtime is not null`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed handoff committed a candidate: %d %v", count, err)
	}
	if _, err := pool.Exec(t.Context(), `drop trigger fail_candidate_handoff on notification_outbox`); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil || len(prepared) != 1 {
		t.Fatalf("recovery: %+v %v", prepared, err)
	}
	retry, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil || len(retry) != 1 || retry[0].DeploymentID != prepared[0].DeploymentID {
		t.Fatalf("lost-reply recovery created a second candidate: %+v %v", retry, err)
	}
	if err := pool.QueryRow(t.Context(), `select count(*) from notification_outbox where channel='environment_workload_image' and payload::jsonb->>'to'=$1`, prepared[0].DeploymentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("candidate handoff was lost or duplicated: %d %v", count, err)
	}
	live, err := store.LiveDeploymentForScope(t.Context(), app.ID, "production")
	if err != nil || live.ID != previous.ID {
		t.Fatalf("handoff rollback changed serving deployment: %+v %v", live, err)
	}
}
