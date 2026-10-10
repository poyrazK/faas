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
		corruptRuntime := func(key string, value json.RawMessage) func(*state.Deployment) {
			corrupted := *frozen
			corrupted.Runtime = map[string]json.RawMessage{}
			for name, raw := range frozen.Runtime {
				corrupted.Runtime[name] = append(json.RawMessage(nil), raw...)
			}
			corrupted.Runtime[key] = value
			raw, _ := json.Marshal(corrupted)
			return func(candidate *state.Deployment) { candidate.EnvironmentWorkloadRuntime = string(raw) }
		}
		corruptBaseline := *frozen
		corruptBaseline.Baseline.ExecutionMode = api.ExecutionModeWorker
		corruptBaselineJSON, _ := json.Marshal(corruptBaseline)
		for name, mutate := range map[string]func(*state.Deployment){
			"frozen source digest":      func(candidate *state.Deployment) { candidate.EnvironmentWorkloadRuntime = string(changedSourceJSON) },
			"deployment image digest":   func(candidate *state.Deployment) { candidate.ImageDigest = otherImage },
			"deployment kind":           func(candidate *state.Deployment) { candidate.Kind = state.DeploymentKindGitHub },
			"orphan source metadata":    func(candidate *state.Deployment) { candidate.SourcePath = "/tmp/unreviewed.tar.gz" },
			"runtime type":              corruptRuntime("port", json.RawMessage(`"not-a-number"`)),
			"runtime constraint":        corruptRuntime("port", json.RawMessage(`65536`)),
			"runtime unsupported probe": corruptRuntime("healthcheck", json.RawMessage(`{"grpc":{"port":8080,"service":"internal"}}`)),
			"baseline class mismatch":   func(candidate *state.Deployment) { candidate.EnvironmentWorkloadRuntime = string(corruptBaselineJSON) },
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

func TestEnvironmentGitOpsImageCandidateFreezesReviewedScopedSecrets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unreviewed bool
	}{
		{name: "reviewed references only"},
		{name: "unreviewed reference blocks candidate", unreviewed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, app, _, _ := workloadIntentFixture(t, basic, "enforce")
				secretStore := basic.(secretRefTestStore)
				for _, name := range []string{"API_TOKEN", "EXTRA_TOKEN"} {
					if err := secretStore.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", name, []byte("sealed-"+name)); err != nil {
						t.Fatal(err)
					}
				}
				refs := map[string]string{"API_TOKEN": "secret:API_TOKEN"}
				if tc.unreviewed {
					refs["EXTRA_TOKEN"] = "secret:EXTRA_TOKEN"
				}
				for alias, ref := range refs {
					if err := secretStore.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", alias, ref); err != nil {
						t.Fatal(err)
					}
				}
				workload := desired.Definition.Workloads["api"]
				workload.SecretRefs = map[string]string{"API_TOKEN": "secret:API_TOKEN"}
				desired.Definition.Workloads["api"] = workload
				desired, err := environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
				if err != nil {
					t.Fatal(err)
				}
				adoptWorkloadIntent(t, store, source)
				adoptSecretRefs(t, secretStore, source)
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "secret-candidate-preparer", time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				if !plan.CanApply() {
					t.Fatalf("reviewed secret-reference plan: %+v", plan)
				}
				if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
					t.Fatal(err)
				}
				plan = claimedIntentPlan(t, store, lease, desired)
				candidates, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
				if tc.unreviewed {
					if !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) || len(candidates) != 0 {
						t.Fatalf("candidate included an unreviewed scoped secret: %+v %v", candidates, err)
					}
					return
				}
				if err != nil || len(candidates) != 1 {
					t.Fatalf("prepare reviewed secret candidate: %+v %v", candidates, err)
				}
				deployment, err := store.DeploymentByID(t.Context(), candidates[0].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				frozen, frozenErr := deployment.ScopedWorkloadRuntime()
				if frozenErr != nil {
					t.Fatalf("candidate runtime: %+v %v", deployment, frozenErr)
				}
				if !reflect.DeepEqual(frozen.SecretRefs, workload.SecretRefs) {
					t.Fatalf("candidate secret authority: got=%v want=%v", frozen.SecretRefs, workload.SecretRefs)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsServiceBindingRequiresPreparedTargetAndExplicitPort(t *testing.T) {
	for _, tc := range []struct {
		name          string
		targetRuntime json.RawMessage
		wantErr       bool
	}{
		{name: "retained target is not in candidate graph", wantErr: true},
		{name: "inherited port is not explicit", targetRuntime: json.RawMessage(`{"execution_mode":"service"}`), wantErr: true},
		{name: "scoped target port", targetRuntime: json.RawMessage(`{"port":8082,"execution_mode":"service"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, callerApp, _, _ := workloadIntentFixture(t, basic, "enforce")
				backend, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID,
					Slug: "shop-backend", WorkloadName: "backend", Type: state.AppTypeApp, Status: state.AppActive,
					RAMMB: 512, MaxConcurrency: 1, WorkloadClass: state.WorkloadClassHTTP,
					Manifest: state.AppManifest{Port: 8079, ExecutionMode: api.ExecutionModeService}})
				if err != nil {
					t.Fatal(err)
				}
				backendDeployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: backend.ID, Scope: "production", Kind: state.DeploymentKindImage,
					Status: state.DeployLive, ImageDigest: "registry.example/backend@sha256:" + strings.Repeat("c", 64)})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateDeploymentStatus(t.Context(), backendDeployment.ID, state.DeployLive, ""); err != nil {
					t.Fatal(err)
				}
				caller := desired.Definition.Workloads["api"]
				caller.ServiceBindings = map[string]api.EnvironmentServiceBinding{"backend": {Workload: "backend", EnvKey: "BACKEND_URL"}}
				desired.Definition.Workloads["api"] = caller
				desired.Definition.Workloads["backend"] = api.EnvironmentWorkload{App: backend.Slug, Runtime: tc.targetRuntime}
				desired, err = environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
				if err != nil {
					t.Fatal(err)
				}
				adoptWorkloadIntent(t, store, source)
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "binding-candidate-gate", time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				_, err = basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
				if tc.wantErr {
					if !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
						t.Fatalf("incomplete binding graph accepted: %v", err)
					}
					deployments, listErr := store.ListDeploymentsForApp(t.Context(), backend.ID, 10, 0)
					if listErr != nil || len(deployments) != 1 {
						t.Fatalf("failed binding gate published a candidate: %+v %v", deployments, listErr)
					}
					callerDeployments, listErr := store.ListDeploymentsForApp(t.Context(), callerApp.ID, 10, 0)
					if listErr != nil || len(callerDeployments) != 1 {
						t.Fatalf("failed binding gate partially published caller: %+v %v", callerDeployments, listErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("complete binding graph rejected: %v", err)
				}
			})
		})
	}
}

func TestPgEnvironmentGitOpsImageCandidateDatabaseFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	pgStore := state.NewPgStore(pool)
	store, source, desired, app, previous, _ := workloadIntentFixture(t, pgStore, "enforce")
	workload := desired.Definition.Workloads["api"]
	workload.Variables = map[string]string{"MODE": "reviewed"}
	desired.Definition.Workloads["api"] = workload
	var err error
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
	if err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store, source)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "candidate-variable-fence", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	var appliedMode string
	if err := pool.QueryRow(t.Context(), `SELECT value FROM app_envs WHERE app_id=$1 AND scope='production' AND key='MODE'`, app.ID).Scan(&appliedMode); err != nil || appliedMode != "reviewed" {
		t.Fatalf("GitOps variable was not persisted to app_envs: value=%q err=%v", appliedMode, err)
	}
	// Candidate preparation is bound to the converged intent snapshot. Applying
	// the reviewed changes advances that snapshot, so use its fresh plan hash.
	plan = claimedIntentPlan(t, store, lease, desired)
	if plan.HasDrift() {
		var drift []string
		for _, change := range plan.Changes {
			if change.Action != "keep" && change.Action != "retain_unmanaged" {
				drift = append(drift, change.Resource+"#"+change.Path+"="+change.Action)
			}
		}
		t.Fatalf("applied candidate plan still has drift: %s", strings.Join(drift, ", "))
	}
	candidates, err := pgStore.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
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
	if frozen.Variables["MODE"] != "reviewed" {
		t.Fatalf("candidate did not freeze reviewed variables: %+v", frozen.Variables)
	}
	tampered := *frozen
	tampered.Variables = map[string]string{"MODE": "console-only"}
	tamperedRaw, _ := json.Marshal(tampered)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	_, insertErr := tx.Exec(t.Context(), `insert into deployments(app_id,scope,kind,image_digest,status,commit_sha,environment_workload_runtime,environment_workload_held) values($1,'production','image',$2,'pending',$3,$4,true)`, app.ID, dep.ImageDigest, dep.CommitSHA, tamperedRaw)
	_ = tx.Rollback(t.Context())
	if insertErr == nil || !strings.Contains(insertErr.Error(), "environment workload preparation lost its reviewed authority") {
		t.Fatalf("database candidate guard accepted changed reviewed variables: %v", insertErr)
	}
	frozen.EnvironmentID = strings.Repeat("0", 8) + "-0000-0000-0000-" + strings.Repeat("0", 12)
	raw, _ := json.Marshal(frozen)
	if _, err := pool.Exec(t.Context(), `insert into deployments(app_id,scope,kind,image_digest,status,environment_workload_runtime) values($1,'production','image',$2,'pending',$3)`, app.ID, dep.ImageDigest, raw); err == nil {
		t.Fatal("candidate inserted without current lease/original scope")
	}
	failed, err := store.SetDeploymentFailed(t.Context(), id, "build_failed", "injected build failure")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != state.DeployFailed || !failed.EnvironmentWorkloadHeld() {
		t.Fatalf("candidate failure lost its execution hold: %+v", failed)
	}
	serving, err := store.LiveDeploymentForScope(t.Context(), app.ID, "production")
	if err != nil || serving.ID != previous.ID || serving.TrafficPercent != 100 {
		t.Fatalf("failed held candidate disrupted the previous serving deployment: %+v %v", serving, err)
	}
	if _, err := store.RetryDeploymentFromStage(t.Context(), id, state.StageImageBuild); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("ordinary retry escaped graph authority: %v", err)
	}
}

func TestPgEnvironmentGitOpsScheduleAndVariableRemovalAreCoveredByCandidateFence(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
	}{{name: "schedule", path: "schedule"}, {name: "variable", path: "variables/MODE"}} {
		t.Run(test.name, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			store := state.NewPgStore(pool)
			_, lease, _, _, _, _ := appliedImageCandidateFixture(t, store)

			plan, _ := json.Marshal(map[string]any{"source_id": lease.Source.ID})
			steps, _ := json.Marshal([]state.EnvironmentGitOpsStep{{
				Resource: "workload/api", Path: test.path, Action: "remove", Status: "applied",
			}})
			now := time.Now().UTC()
			if err := store.FinishEnvironmentGitOps(t.Context(), lease, "partial", plan, steps, "test_removal", now, now); err != nil {
				t.Fatalf("finish run with applied %s removal: %v", test.name, err)
			}
			input, _ := json.Marshal(map[string]any{
				"source_id": lease.Source.ID, "revision_id": lease.Revision.ID,
				"generation": lease.Source.Generation, "resource": "workload/api",
			})
			var authorized bool
			if err := pool.QueryRow(t.Context(), `select environment_workload_candidate_applied_removal_valid($1::jsonb)`, input).Scan(&authorized); err != nil {
				t.Fatal(err)
			}
			if !authorized {
				t.Fatalf("database candidate fence did not recognize the applied %s removal", test.name)
			}
		})
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
