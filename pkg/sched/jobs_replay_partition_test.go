// adr: 366 — a replayed job task keeps its original partition identity.

package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestEngineWakeJobReplayKeepsPartition reproduces production-us: a 4-task
// fan-out whose partition 2 failed was replayed with `jobs replay-failed`,
// and the replay ran with GREGALE_TASK_INDEX=0 and GREGALE_TASK_COUNT=1. It
// redid partition 0's work, reported OK, and never redid partition 2. Both the
// replay and a replay of that replay must run as partition 2 of 4.
func TestEngineWakeJobReplayKeepsPartition(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, job, _ := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, nil, 4)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	failOnly := func(runID string, failed, tasks int) {
		t.Helper()
		for i := 0; i < tasks; i++ {
			status, exit := "succeeded", 0
			if i == failed {
				status, exit = "failed", 3
			}
			if err := store.JobTaskMarkTerminal(ctx, runID, i, status, exit, "", "", time.Now()); err != nil {
				t.Fatalf("JobTaskMarkTerminal(%d): %v", i, err)
			}
		}
		if _, err := store.JobRunRecompute(ctx, runID); err != nil {
			t.Fatalf("JobRunRecompute: %v", err)
		}
	}
	vmm := &recordingJobVMM{}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	assertPartition := func(runID string) {
		t.Helper()
		if _, err := e.WakeJob(ctx, acct.ID, runID, 0); err != nil {
			t.Fatalf("WakeJob(%s): %v", runID, err)
		}
		for key, want := range map[string]string{
			"GREGALE_TASK_INDEX": "2", "GREGALE_PARTITION_INDEX": "2",
			"GREGALE_TASK_COUNT": "4", "GREGALE_PARTITION_COUNT": "4",
		} {
			if got := vmm.spec.Env[key]; got != want {
				t.Errorf("run %s %s = %q, want %q", runID, key, got, want)
			}
		}
	}

	failOnly(run.ID, 2, 4)
	replay, tasks, err := store.JobRunReplayFailed(ctx, run.ID, acct.ID)
	if err != nil || len(tasks) != 1 || tasks[0].TaskIndex != 0 {
		t.Fatalf("replay = %+v tasks=%+v err=%v", replay, tasks, err)
	}
	assertPartition(replay.ID)

	failOnly(replay.ID, 0, 1)
	second, tasks, err := store.JobRunReplayFailed(ctx, replay.ID, acct.ID)
	if err != nil || len(tasks) != 1 || tasks[0].SourceTaskIndex == nil || *tasks[0].SourceTaskIndex != 2 {
		t.Fatalf("replay of replay = %+v tasks=%+v err=%v; want source_task_index 2", second, tasks, err)
	}
	assertPartition(second.ID)
}
