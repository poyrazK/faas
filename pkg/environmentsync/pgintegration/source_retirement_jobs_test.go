package pgintegration_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsSourceRetirementReleasesScheduledJob(t *testing.T) {
	for _, operation := range []string{"detach", "rebind"} {
		t.Run(operation, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, _, intent, _ := sourceScheduledJobFixture(t, basic, "github", "source", true)
				jobs := basic.(state.JobStore)
				before, err := jobs.JobGetByID(t.Context(), intent.JobID)
				if err != nil || before.EnvironmentGitSourceID != lease.Source.ID {
					t.Fatal("Job did not retain its exact owning source", before.EnvironmentGitSourceID, err)
				}
				if _, _, err := jobs.JobSoftDelete(t.Context(), intent.JobID); !errors.Is(err, state.ErrEnvironmentGitManaged) {
					t.Fatal("bound Job lost protection", err)
				}
				source := reportSourceForRetirement(t, basic, lease)
				if _, err := jobs.JobUpdate(t.Context(), intent.JobID, []string{"/bin/true"}, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, state.ErrEnvironmentGitManaged) {
					t.Fatal("report mode released Job ownership", err)
				}
				retireScheduledJobSource(t, basic, source, operation)
				after, err := jobs.JobGetByID(t.Context(), intent.JobID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("source retirement changed Job settings or attribution", err)
				}
				intents := basic.(state.EnvironmentWorkloadIntentStore)
				if _, err := intents.EnvironmentWorkloadIntentByJob(t.Context(), source.AccountID, intent.JobID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("retired Job still receives GitOps runtime authority", err)
				}
				retained, err := intents.EnvironmentWorkloadIntent(t.Context(), source.AccountID, intent.AppID, intent.EnvironmentID)
				if err != nil || !reflect.DeepEqual(intent, retained) {
					t.Fatal("source retirement discarded scoped intent history", err)
				}
				if _, err := jobs.JobUpdate(t.Context(), intent.JobID, []string{"/bin/true"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
					t.Fatal("detached Job command update", err)
				}
				cron := "0 * * * *"
				if _, err := basic.(state.JobScheduleUpdateStore).JobUpdateWithSchedule(t.Context(), intent.JobID, nil, nil, nil, nil, nil, nil, nil, nil, &cron, nil); err != nil {
					t.Fatal("detached Job schedule update", err)
				}
				image := "registry.example/manual@sha256:" + strings.Repeat("b", 64)
				updated, err := jobs.JobUpdate(t.Context(), intent.JobID, nil, &image, nil, nil, nil, nil, nil, nil)
				if err != nil || updated.ImageRef != image || updated.EnvironmentGitSourceID != source.ID {
					t.Fatal("detached Job could not select ordinary image input", err)
				}
				if deleted, live, err := jobs.JobSoftDelete(t.Context(), intent.JobID); err != nil || !deleted || live {
					t.Fatalf("detached Job deletion: deleted=%v live=%v err=%v", deleted, live, err)
				}
			})
		})
	}
}

func retireScheduledJobSource(t *testing.T, basic gitOpsTestStore, source state.EnvironmentGitSource, operation string) state.EnvironmentGitSource {
	t.Helper()
	lifecycle := basic.(state.EnvironmentGitOpsLifecycleStore)
	if operation == "detach" {
		if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
			t.Fatal(err)
		}
		return state.EnvironmentGitSource{}
	}
	spec := source.Spec
	spec.Prune, spec.ManifestPath = false, "environments/rebound.yaml"
	next, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestGitOpsReboundJobRequiresFreshReconciliation(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained_job", true: "deleted_job"}[deleted], func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, desired, intent, _ := sourceScheduledJobFixture(t, basic, "github", "source", true)
				source := reportSourceForRetirement(t, basic, lease)
				next := retireScheduledJobSource(t, basic, source, "rebind")
				jobs := basic.(state.JobStore)
				if _, err := jobs.JobUpdate(t.Context(), intent.JobID, []string{"/bin/true"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
					t.Fatal("new unapproved binding inherited old Job ownership", err)
				}
				if deleted {
					if ok, _, err := jobs.JobSoftDelete(t.Context(), intent.JobID); err != nil || !ok {
						t.Fatal("delete before fresh reconciliation", err)
					}
				}
				var err error
				next, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(next, desired, lease.Revision.CommitSHA))
				if err != nil {
					t.Fatal(err)
				}
				next, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), next.AccountID, next.ID,
					state.EnvironmentGitSourceUpdate{ExpectedGeneration: next.Generation, Mode: "enforce"})
				if err != nil {
					t.Fatal(err)
				}
				store := basic.(intentTestStore)
				adoptWorkloadIntent(t, store, next)
				fresh, err := basic.ClaimEnvironmentGitOps(t.Context(), "rebound-job", time.Now(), time.Minute)
				if err != nil || fresh.Source.ID != next.ID {
					t.Fatal("fresh controller claim", err)
				}
				if _, err := store.ApplyEnvironmentGitOps(t.Context(), fresh, claimedIntentPlan(t, store, fresh, desired)); err != nil {
					t.Fatal("fresh Job reconciliation", err)
				}
				plan := claimedIntentPlan(t, store, fresh, desired)
				preparer := basic.(state.EnvironmentGitOpsPreparationStore)
				requests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), fresh, plan)
				if err != nil {
					t.Fatal(err)
				}
				artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
				for _, request := range requests {
					artifacts[request.Resource] = sourceArtifact(t, request)
				}
				if _, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), fresh, plan, artifacts); err != nil {
					t.Fatal("rebound candidate preparation", err)
				}
				current, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), next.AccountID, intent.AppID, intent.EnvironmentID)
				if err != nil || (current.JobID == intent.JobID) == deleted {
					t.Fatal("reconciliation selected the wrong Job identity", err)
				}
				job, err := jobs.JobGetByID(t.Context(), current.JobID)
				if err != nil || job.EnvironmentGitSourceID != next.ID || job.Status != "paused" {
					t.Fatal("new source did not acquire the reviewed paused Job", err)
				}
				if _, _, err := jobs.JobSoftDelete(t.Context(), current.JobID); !errors.Is(err, state.ErrEnvironmentGitManaged) {
					t.Fatal("reconciled Job lost its new owner's protection", err)
				}
			})
		})
	}
}

func TestPgGitOpsLegacyJobDetachAndOwnershipFences(t *testing.T) {
	for _, alreadyDetached := range []bool{false, true} {
		t.Run(map[bool]string{false: "bound_at_upgrade", true: "detached_before_upgrade"}[alreadyDetached], func(t *testing.T) {
			testPgLegacyJobOwnership(t, alreadyDetached)
		})
	}
}

func testPgLegacyJobOwnership(t *testing.T, alreadyDetached bool) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, _, intent, _ := sourceScheduledJobFixture(t, store, "github", "source", true)
	if _, err := pool.Exec(t.Context(), "UPDATE jobs SET environment_git_source_id=NULL WHERE id=$1", intent.JobID); err == nil {
		t.Fatal("ordinary SQL cleared source ownership")
	}
	var source state.EnvironmentGitSource
	if alreadyDetached {
		source = reportSourceForRetirement(t, store, lease)
		retireScheduledJobSource(t, store, source, "detach")
	}
	body, err := migrations.FS.ReadFile("20261103200000001_gitops_job_source_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(body), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, script := range []string{parts[1], parts[0]} {
		if _, err := tx.Exec(t.Context(), script); err != nil {
			t.Fatal("populated migration replay", err)
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.JobGetByID(t.Context(), intent.JobID)
	if err != nil || legacy.EnvironmentGitSourceID != "" {
		t.Fatal("legacy Job was not restored", err)
	}
	if !alreadyDetached {
		if _, _, err := store.JobSoftDelete(t.Context(), intent.JobID); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatal("legacy attribution failed open", err)
		}
		source = reportSourceForRetirement(t, store, lease)
		retireScheduledJobSource(t, store, source, "detach")
		retired, err := store.JobGetByID(t.Context(), intent.JobID)
		if err != nil || retired.EnvironmentGitSourceID != source.ID {
			t.Fatal("detach did not attribute legacy Job", err)
		}
		if _, err := pool.Exec(t.Context(), "UPDATE jobs SET environment_git_source_id=NULL WHERE id=$1", intent.JobID); err == nil {
			t.Fatal("retired ownership history was mutable")
		}
	}
	if _, err := store.EnvironmentWorkloadIntentByJob(t.Context(), source.AccountID, intent.JobID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("legacy retired Job retained runtime authority", err)
	}
	if _, err := store.JobUpdate(t.Context(), intent.JobID, []string{"/bin/true"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal("legacy detached Job cannot be updated", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE jobs SET image_storage_key='unverified',image_materialization_status='ready' WHERE id=$1", intent.JobID); err == nil {
		t.Fatal("detached source Job accepted an unverified artifact")
	}
	if deleted, live, err := store.JobSoftDelete(t.Context(), intent.JobID); err != nil || !deleted || live {
		t.Fatal("attributed legacy Job could not be deleted", err)
	}
}
