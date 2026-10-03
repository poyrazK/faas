package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemStoreJobAttemptsReplayAndSnapshot(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	job, err := st.JobCreate(ctx, "acct-replay", "batch-replay", "batch", "registry.example/job:v1",
		[]string{"/bin/job", "--old"}, 256, 60, 2, 1, json.RawMessage(`{"BASE":"old"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/old.ext4", ""); err != nil {
		t.Fatal(err)
	}
	run, tasks, err := st.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil,
		json.RawMessage(`{"RUN":"one"}`), 2, JobRunOptions{Inputs: []JobInput{{ID: "a", Ref: "obj-a"}, {ID: "b", Ref: "obj-b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if run.ImageStorageKeySnapshot != "jobs/old.ext4" || run.RAMMBSnapshot == nil || *run.RAMMBSnapshot != 256 ||
		string(run.EffectiveEnvSnapshot) != `{"BASE":"old","RUN":"one"}` {
		t.Fatalf("snapshot = %+v", run)
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
	if err := st.JobTaskMarkTerminal(ctx, run.ID, tasks[1].TaskIndex, "succeeded", 0, "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	attempts, err := st.JobTaskAttemptList(ctx, run.ID, 0, 10, 0)
	if err != nil || len(attempts) != 2 || attempts[0].Attempt != 1 || attempts[1].Attempt != 2 ||
		attempts[0].InputID != "a" || attempts[0].ErrorMessage == nil || *attempts[0].ErrorMessage != "first" {
		t.Fatalf("attempts = %+v, err %v", attempts, err)
	}
	replay, replayTasks, err := st.JobRunReplayFailed(ctx, run.ID, job.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if replay.SourceRunID == nil || *replay.SourceRunID != run.ID || len(replayTasks) != 1 ||
		replayTasks[0].InputID != "a" || replayTasks[0].SourceTaskIndex == nil || *replayTasks[0].SourceTaskIndex != 0 {
		t.Fatalf("replay = %+v, tasks = %+v", replay, replayTasks)
	}
	legacy := st.jobRuns[run.ID]
	legacy.ImageResolvedDigestSnapshot = ""
	st.jobRuns[run.ID] = legacy
	if _, _, err := st.JobRunReplayFailed(ctx, run.ID, job.AccountID); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy run without digest replay error = %v, want conflict", err)
	}
}
