package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the source descriptor is synthetic; CI admission creates an ordinary
// Jobs run through the real PostgreSQL adapter. No guest command executes.
type ciReplaySource struct{}

func (ciReplaySource) Stage(_ context.Context, req ci.JobsSourceRequest) (ci.SourceArchive, error) {
	digest := strings.Repeat("a", 64)
	return ci.SourceArchive{Key: fmt.Sprintf("ci/accounts/%s/executions/%s/attempts/%s/source/%s.tar.gz", req.Key.AccountID, req.Key.ExecutionID, req.Key.AttemptID, digest), Digest: "sha256:" + digest, Bytes: 1}, nil
}

type ciReplayOutput struct{}

func (ciReplayOutput) Collect(context.Context, ci.JobsSourceRequest, ci.PlannedJob, string, *int) (ci.Observation, error) {
	return ci.Observation{}, errors.New("no guest output in replay guard test")
}

func TestPgCIJobsCannotReplayThroughJobsAPI(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "ci-replay-"+uuid.NewString()+"@example.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	job, err := s.JobCreate(ctx, account.ID, "ci-replay-test", "batch", image, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, api.CIJobsRAMMB, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE jobs SET image_materialization_status='ready',image_resolved_digest=$2,image_storage_key=$3 WHERE id=$1", job.ID, "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+"/image.ext4"); err != nil {
		t.Fatal(err)
	}
	store := ci.NewPostgresStore(pool)
	revision, err := store.PutRevision(ctx, account.ID, "checks", ci.Definition{Version: 1, Repository: "gregale/ci-replay", Jobs: map[string]ci.Job{"test": {Image: image, TimeoutSeconds: 60, Steps: []ci.Step{{Name: "unit", Run: "synthetic command; never executed"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.PutGitHubBinding(ctx, account.ID, "source", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(name string) string {
		switch name {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv:
			return "1"
		case ci.AccountsEnv:
			return account.ID
		}
		return ""
	})
	verify := func(context.Context, string, int64, int64, string) error { return nil }
	execution, _, err := ci.AdmitJobs(ctx, cfg, store, verify, account.ID, "fresh", ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: binding.ID, Templates: map[string]string{"test": job.ID}})
	if err != nil {
		t.Fatal(err)
	}
	backend := ci.JobsBackend{Config: cfg, Store: store, Source: ciReplaySource{}, Output: ciReplayOutput{}}
	execution, err = (ci.Engine{Store: store, Backend: backend}).Tick(ctx, account.ID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	runID := execution.Jobs[0].Attempt.ID
	if _, err := pool.Exec(ctx, "UPDATE job_tasks SET status='failed',exit_code=1,finished_at=clock_timestamp() WHERE run_id=$1", runID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JobRunRecompute(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JobRunReplayFailed(ctx, runID, account.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("CI command was replayable with its old attempt identity: %v", err)
	}
	if err := s.JobTaskRetry(ctx, runID, 0, time.Now()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("CI command gained a second task attempt: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM job_runs WHERE account_id=$1", account.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay created a second CI job: %d %v", count, err)
	}
}
