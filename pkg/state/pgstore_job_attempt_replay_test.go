package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgJobsAttemptJournalAndReplay(t *testing.T) {
	st, pool, ctx := pgJobsStoreWithPool(t)
	job, run, tasks := pgJobsSeed(t, st, ctx, "attempt-replay")
	if _, err := st.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready",
		"sha256:"+strings.Repeat("a", 64), "jobs/attempt-replay.ext4", ""); err != nil {
		t.Fatal(err)
	}
	refreshed, err := st.JobRunGetByID(ctx, run.ID)
	if err != nil || refreshed.ImageStorageKeySnapshot != "jobs/attempt-replay.ext4" {
		t.Fatalf("bound run = %+v, err %v", refreshed, err)
	}
	if err := st.JobTaskMarkTerminal(ctx, run.ID, tasks[0].TaskIndex, "failed", 1, "failed", "first", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.JobTaskRetry(ctx, run.ID, tasks[0].TaskIndex, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.JobTaskMarkTerminal(ctx, run.ID, tasks[0].TaskIndex, "failed", 2, "failed", "second", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks[1:] {
		if err := st.JobTaskMarkTerminal(ctx, run.ID, task.TaskIndex, "succeeded", 0, "", "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	attempts, err := st.JobTaskAttemptList(ctx, run.ID, 0, 10, 0)
	if err != nil || len(attempts) != 2 || attempts[0].Attempt != 1 || attempts[1].Attempt != 2 {
		t.Fatalf("attempts = %+v, err %v", attempts, err)
	}
	replay, fanned, err := st.JobRunReplayFailed(ctx, run.ID, job.AccountID)
	if err != nil || replay.SourceRunID == nil || *replay.SourceRunID != run.ID || len(fanned) != 1 ||
		fanned[0].SourceTaskIndex == nil || *fanned[0].SourceTaskIndex != 0 {
		t.Fatalf("replay = %+v, tasks = %+v, err %v", replay, fanned, err)
	}
	if _, err := pool.Exec(ctx, `update job_runs set image_resolved_digest_snapshot = null where id = $1::uuid`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.JobRunReplayFailed(ctx, run.ID, job.AccountID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("legacy run without digest replay error = %v, want conflict", err)
	}
}

func TestPgFlexibleJobUsageKeepsExecutionClassWithoutRateChange(t *testing.T) {
	st, pool, ctx := pgJobsStoreWithPool(t)
	acct, err := st.CreateAccount(ctx, "flexible-meter-evidence@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.JobCreate(ctx, acct.ID, "meter-evidence", "batch", "registry.example/job:v1",
		[]string{"/bin/job"}, 256, 60, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("b", 64), "jobs/meter-evidence.ext4", ""); err != nil {
		t.Fatal(err)
	}
	start, latest := time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour)
	run, _, err := st.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, json.RawMessage(`{}`), 1,
		state.JobRunOptions{ExecutionClass: "flexible", EligibleAt: &start, LatestStartAt: &latest})
	if err != nil {
		t.Fatal(err)
	}
	instanceID, lease := uuid.NewString(), uuid.NewString()
	nodeID := resolveDefaultLocal(t, ctx, st)
	if _, err := st.CreateJobInstance(ctx, instanceID, job.ID, run.ID, 0, "running", job.RAMMB, nodeID, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.JobTaskMarkClaimed(ctx, run.ID, 0, instanceID, lease, time.Now().Add(time.Minute), nodeID); err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	if err := st.AppendJobUsage(context.Background(), acct.ID, job.ID, instanceID, minute, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	var gotRun, gotClass string
	var mbSeconds int64
	if err := pool.QueryRow(ctx, `select job_run_id, job_execution_class, mb_seconds from usage_minutes where instance_id = $1::uuid and minute = $2`, instanceID, minute).Scan(&gotRun, &gotClass, &mbSeconds); err != nil {
		t.Fatal(err)
	}
	if gotRun != run.ID || gotClass != "flexible" || mbSeconds != 100 {
		t.Fatalf("usage evidence = %s %s %d", gotRun, gotClass, mbSeconds)
	}
}
