package pgintegration_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func appliedSourceCandidateFixture(t *testing.T, basic gitOpsTestStore, kind string) (intentTestStore, state.EnvironmentGitOpsLease, state.App, state.Deployment, environmentsync.Plan) {
	t.Helper()
	store, source, desired, app, previous, _ := workloadIntentFixture(t, basic, "enforce")
	adoptWorkloadIntent(t, store, source)
	w := desired.Definition.Workloads["api"]
	w.Source = &api.EnvironmentWorkloadSource{Kind: kind, Directory: "apps/api"}
	if kind == "dockerfile" {
		w.Source.Dockerfile = "deploy/Dockerfile"
	}
	desired.Definition.Workloads["api"] = w
	desired, err := environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40))); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "source-preparer", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
		t.Fatalf("apply source intent: %v", err)
	}
	return store, lease, app, previous, claimedIntentPlan(t, store, lease, desired)
}

func sourceArtifact(t *testing.T, request state.EnvironmentWorkloadSourceRequest) state.EnvironmentWorkloadSourceArtifact {
	t.Helper()
	root := t.TempDir()
	return state.EnvironmentWorkloadSourceArtifact{RevisionID: request.RevisionID, CommitSHA: request.CommitSHA, DefinitionDigest: request.DefinitionDigest, BuildID: request.BuildID, Path: filepath.Join(root, "source.tar.gz"),
		SHA256: strings.Repeat("d", 64), Bytes: 256, LogPath: filepath.Join(root, "build.log")}
}

func TestEnvironmentGitOpsSourceCandidatesAreAtomicPinnedAndHeld(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile"} {
		t.Run(kind, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, lease, app, previous, plan := appliedSourceCandidateFixture(t, basic, kind)
				preparer := basic.(state.EnvironmentGitOpsPreparationStore)
				intent, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), lease.Source.AccountID, app.ID, lease.Source.EnvironmentID)
				if err != nil || intent.SourceRevision != lease.Revision.CommitSHA {
					t.Fatalf("scoped source commit: %+v %v", intent, err)
				}
				intent.SourceRevision = strings.Repeat("e", 40)
				if _, err := basic.(state.EnvironmentWorkloadIntentStore).PutEnvironmentWorkloadIntent(t.Context(), intent); !errors.Is(err, state.ErrEnvironmentGitManaged) {
					t.Fatalf("console replaced the owned source commit: %v", err)
				}
				requests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
				if err != nil || len(requests) != 1 || requests[0].Source.Directory != "apps/api" || (kind == "dockerfile" && requests[0].Source.Dockerfile != "deploy/Dockerfile") {
					t.Fatalf("requests: %+v %v", requests, err)
				}
				if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, nil); !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
					t.Fatalf("missing archive accepted: %v", err)
				}
				artifact := sourceArtifact(t, requests[0])
				wrongRevision := artifact
				wrongRevision.CommitSHA = strings.Repeat("c", 40)
				if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: wrongRevision}); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("archive from another commit accepted: %v", err)
				}
				invalid := artifact
				invalid.SHA256 = "unverified"
				if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: invalid}); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("unverified archive accepted: %v", err)
				}
				candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: artifact})
				if err != nil || len(candidates) != 1 {
					t.Fatalf("prepare: %+v %v", candidates, err)
				}
				dep, err := store.DeploymentByID(t.Context(), candidates[0].DeploymentID)
				if err != nil || dep.Kind != state.DeploymentKindGitHub || dep.CommitSHA != strings.Repeat("b", 40) || dep.SourceRoot != "apps/api" || dep.SourceSHA256 != artifact.SHA256 || dep.SourceBytes != artifact.Bytes || dep.BuildID != artifact.BuildID || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeployBuilding || dep.TrafficPercent != 0 {
					t.Fatalf("candidate: %+v %v", dep, err)
				}
				frozen, err := dep.ScopedWorkloadRuntime()
				if err != nil || (kind == "dockerfile" && frozen.Source.Dockerfile != "deploy/Dockerfile") || !reflect.DeepEqual(*frozen.SourceArchive, artifact) || string(frozen.Runtime["port"]) != "8080" {
					t.Fatalf("frozen source: %+v %v", frozen, err)
				}
				build, err := basic.(state.Store).BuildByID(t.Context(), artifact.BuildID)
				if err != nil || build.DeploymentID != dep.ID || build.Status != state.BuildQueued || build.Kind != state.DeploymentKindGitHub || build.SourceBytes != artifact.Bytes || build.LogPath != artifact.LogPath {
					t.Fatalf("queue: %+v %v", build, err)
				}
				requests, err = preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
				if err != nil || len(requests) != 0 {
					t.Fatalf("retry requested another archive: %+v %v", requests, err)
				}
				retry, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, nil)
				if err != nil || len(retry) != 1 || retry[0].DeploymentID != dep.ID || retry[0].BuildID != build.ID {
					t.Fatalf("retry duplicated durable work: %+v %v", retry, err)
				}
				if err := basic.(state.Store).MarkDeploymentLive(t.Context(), dep.ID); err == nil {
					t.Fatal("unqualified source candidate became live")
				}
				if _, err := basic.(state.Store).CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 512, "", ""); err == nil {
					t.Fatal("unqualified source candidate booted")
				}
				live, err := basic.(state.Store).LiveDeploymentForScope(t.Context(), app.ID, "production")
				if err != nil || live.ID != previous.ID {
					t.Fatalf("source preparation replaced serving deployment: %+v %v", live, err)
				}
				current, _ := store.AppByID(t.Context(), app.ID)
				if !reflect.DeepEqual(current.Manifest, app.Manifest) {
					t.Fatal("source preparation changed the shared app")
				}
				command := "./changed"
				if _, err := basic.(state.Store).UpdateApp(t.Context(), app.ID, state.UpdateAppParams{StartCommand: &command}); err != nil {
					t.Fatal(err)
				}
				if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, nil); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("stale source review accepted: %v", err)
				}
			})
		})
	}
}

func TestPgEnvironmentGitOpsSourceCandidateQueueAndDatabaseFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	_, lease, app, _, plan := appliedSourceCandidateFixture(t, store, "source")
	requests, err := store.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests: %+v %v", requests, err)
	}
	artifact := sourceArtifact(t, requests[0])
	if _, err := pool.Exec(t.Context(), `update app_environment_workload_intents set source_revision=repeat('c',40) where app_id=$1 and environment_id=$2`, app.ID, lease.Source.EnvironmentID); err == nil {
		t.Fatal("SQL bypassed source commit ownership")
	}
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: artifact}
	if _, err := pool.Exec(t.Context(), `create function fail_gitops_build_queue() returns trigger language plpgsql as $$ begin raise exception 'injected source queue failure'; end $$;
 create trigger fail_gitops_build_queue before insert on builds for each row execute function fail_gitops_build_queue()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, artifacts); err == nil || !strings.Contains(err.Error(), "injected source queue") {
		t.Fatalf("queue failure hidden: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `select count(*) from deployments where app_id=$1 and environment_workload_runtime is not null`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("queue failure committed partial candidate: %d %v", count, err)
	}
	if _, err := pool.Exec(t.Context(), `drop trigger fail_gitops_build_queue on builds`); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, artifacts)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("recovery: %+v %v", candidates, err)
	}
	for _, query := range []string{
		`update deployments set source_root='other' where id=$1`,
		`update deployments set source_bytes=source_bytes+1 where id=$1`,
		`update deployments set source_url='github://other/repo@wrong' where id=$1`,
		`update deployments set source_sha256=repeat('f',64) where id=$1`,
		`update deployments set build_id=null where id=$1`,
		`update deployments set inferred_profile='{"framework":"docker"}'::jsonb where id=$1`,
		`update deployments set github_source_ref='refs/heads/main' where id=$1`,
		`update deployments set status='live' where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, candidates[0].DeploymentID); err == nil {
			t.Fatalf("SQL changed frozen source: %s", query)
		}
	}
	dep, err := store.DeploymentByID(t.Context(), candidates[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("database candidate cannot be consumed: %v", err)
	}
}

func TestEnvironmentGitOpsSourceCandidateNewCommitAndSupersession(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, _, _, plan := appliedSourceCandidateFixture(t, basic, "source")
		preparer := basic.(state.EnvironmentGitOpsPreparationStore)
		requests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
		if err != nil || len(requests) != 1 {
			t.Fatalf("requests: %+v %v", requests, err)
		}
		oldArtifact := sourceArtifact(t, requests[0])
		var definition api.EnvironmentDefinition
		if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
			t.Fatal(err)
		}
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(lease.Source, desired, strings.Repeat("c", 40))); err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: oldArtifact}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded archive download created work: %v", err)
		}
		current, err := store.ClaimEnvironmentGitOps(t.Context(), "source-next-commit", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		next := claimedIntentPlan(t, store, current, desired)
		if !next.HasDrift() || !next.CanApply() {
			t.Fatalf("unchanged definition hid new code commit: %+v", next)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), current, next); err != nil {
			t.Fatal(err)
		}
		next = claimedIntentPlan(t, store, current, desired)
		if next.HasDrift() {
			t.Fatalf("source commit intent did not converge: %+v", next)
		}
		if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), current, next, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: oldArtifact}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("new generation reused the old archive: %v", err)
		}
		requests, err = preparer.EnvironmentGitOpsSourceRequests(t.Context(), current, next)
		if err != nil || len(requests) != 1 || requests[0].CommitSHA != strings.Repeat("c", 40) {
			t.Fatalf("new commit requests: %+v %v", requests, err)
		}
		prepared, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), current, next, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
		if err != nil || len(prepared) != 1 {
			t.Fatalf("current generation: %+v %v", prepared, err)
		}
	})
}
