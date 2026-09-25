package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 099 — a stale claimed task retries within its budget, then dead-letters.
func TestReapStuckJobTasksUsesRetryBudget(t *testing.T) {
	store := state.NewMemStore()
	_, _, run := seedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	ctx := context.Background()
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	expired := time.Now().Add(-2 * time.Minute)
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, "instance-1", "lease-1", expired, state.DefaultLocalNodeName); err != nil {
		t.Fatal(err)
	}
	count, err := engine.ReapStuckJobTasks(ctx, StuckJobReaperConfig{TTL: time.Minute})
	if err != nil || count != 1 {
		t.Fatalf("first reaper sweep = (%d, %v)", count, err)
	}
	task, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || task.Status != "queued" || task.Attempt != 2 {
		t.Fatalf("retried task = %+v, %v", task, err)
	}
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, "instance-2", "lease-2", expired, state.DefaultLocalNodeName); err != nil {
		t.Fatal(err)
	}
	count, err = engine.ReapStuckJobTasks(ctx, StuckJobReaperConfig{TTL: time.Minute})
	if err != nil || count != 1 {
		t.Fatalf("second reaper sweep = (%d, %v)", count, err)
	}
	settled, err := store.JobRunGetByID(ctx, run.ID)
	if err != nil || settled.AggregateStatus != "dead_letter" || settled.DeadLetterCount != 1 || settled.TasksFailed != 1 {
		t.Fatalf("settled run = %+v, %v", settled, err)
	}
}
