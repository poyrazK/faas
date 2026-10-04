package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func foreachTestSpec() api.WorkflowSpec {
	return api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{
		{Name: "lookup", Run: "lookup"},
		{Name: "batch", DependsOn: []string{"lookup"}, ForEach: &api.WorkflowForEachSpec{Items: "steps.lookup.output.items", Action: api.WorkflowForEachActionSpec{Run: "send", Input: json.RawMessage(`{"item":"{{input.item}}","index":"{{input.index}}"}`), Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}}}},
		{Name: "next", Run: "next", DependsOn: []string{"batch"}},
	}}
}

func seedForEach(t *testing.T, store Store, spec api.WorkflowSpec, items string) *WorkflowRun {
	t.Helper()
	run := seedGuardRunWithSpec(t, store, spec, `{}`)
	if err := store.MarkWorkflowStepStatus(context.Background(), run.ID, "lookup", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"items":`+items+`}`), nil); err != nil {
		t.Fatal(err)
	}
	return run
}

func finishForEachItem(t *testing.T, store Store, runID string, item *WorkflowStep, output string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.StartWorkflowStep(ctx, runID, item.StepName, item.Attempt+1, item.Input); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowStepAttemptStatus(ctx, runID, item.StepName, WorkflowStepStatusSucceeded, item.Attempt+1, nil, json.RawMessage(output), nil); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowForEachSnapshotsSequentialProgressAndRecovery(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedForEach(t, store, foreachTestSpec(), `[{"id":9007199254740993},{"literal":"{{input.secret}}"}]`)
		first, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil || first.Item == nil || first.Item.ForEachIndex == nil || *first.Item.ForEachIndex != 0 {
			t.Fatalf("first item: %+v %v", first, err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "batch", 1, json.RawMessage(`{}`)); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("parent ran an executor: %v", err)
		}
		second := guardStep(t, store, run.ID, api.WorkflowForEachItemName("batch", 1))
		if _, err := store.StartWorkflowStep(ctx, run.ID, second.StepName, 1, second.Input); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("out-of-order start: %v", err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, first.Item.StepName, 1, json.RawMessage(`{"changed":true}`)); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("item input changed: %v", err)
		}
		finishForEachItem(t, store, run.ID, first.Item, `{"n":9007199254740993}`)
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "lookup", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"items":["replaced"]}`), nil); err != nil {
			t.Fatal(err)
		}
		next, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil || next.Item == nil || *next.Item.ForEachIndex != 1 || !strings.Contains(string(next.Item.Input), "{{input.secret}}") {
			t.Fatalf("snapshot changed: %+v %v", next, err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, next.Item.StepName, 1, next.Item.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		recovered, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil || recovered.Item == nil || recovered.Item.Attempt != 1 || *recovered.Item.ForEachIndex != 1 {
			t.Fatalf("recovery reset completed progress/attempt: %+v %v", recovered, err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, next.Item.StepName, WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`"stale"`), nil); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("stale result committed: %v", err)
		}
		finishForEachItem(t, store, run.ID, recovered.Item, `[true,null]`)
		outcome, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil || !outcome.Complete {
			t.Fatalf("batch stranded: %+v %v", outcome, err)
		}
		parent := guardStep(t, store, run.ID, "batch")
		if parent.Status != WorkflowStepStatusSucceeded || parent.Attempt != 0 || parent.ForEachCount == nil || *parent.ForEachCount != 2 || !equalWorkflowJSON(parent.Output, json.RawMessage(`[{"n":9007199254740993},[true,null]]`)) {
			t.Fatalf("ordered results lost: %+v", parent)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, next.Item.StepName)
		if err != nil || len(attempts) != 2 || attempts[0].Status != WorkflowAttemptStatusFailed || attempts[1].Status != WorkflowAttemptStatusSucceeded {
			t.Fatalf("attempt history: %+v %v", attempts, err)
		}
		*parent.ForEachCount = 0
		if *guardStep(t, store, run.ID, "batch").ForEachCount != 2 {
			t.Fatal("inspection modified batch metadata")
		}
	})
}

func TestWorkflowForEachFailureBoundsAndCancellation(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedForEach(t, store, foreachTestSpec(), `[0,1,2]`)
		first, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil {
			t.Fatal(err)
		}
		finishForEachItem(t, store, run.ID, first.Item, `"`+strings.Repeat("x", int(api.WorkflowForEachMaxOutputBytes)/2)+`"`)
		next, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, next.Item.StepName, 1, next.Item.Input); err != nil {
			t.Fatal(err)
		}
		oversized := json.RawMessage(`"` + strings.Repeat("y", int(api.WorkflowForEachMaxOutputBytes)/2) + `"`)
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, next.Item.StepName, WorkflowStepStatusSucceeded, 1, nil, oversized, nil); !errors.Is(err, ErrWorkflowForEachOutputLimit) {
			t.Fatalf("collection cap bypassed: %v", err)
		}
		message := "output limit"
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, next.Item.StepName, WorkflowStepStatusFailed, 1, nil, nil, &message); err != nil {
			t.Fatal(err)
		}
		if result, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch"); err != nil || !result.Complete {
			t.Fatalf("failed batch did not finish: %+v %v", result, err)
		}
		if parent := guardStep(t, store, run.ID, "batch"); parent.Status != WorkflowStepStatusFailed || len(parent.Output) == 0 || int64(len(parent.Output)) > api.WorkflowForEachMaxOutputBytes {
			t.Fatalf("failed batch summary: %+v", parent)
		}
		if item := guardStep(t, store, run.ID, api.WorkflowForEachItemName("batch", 2)); item.Status != WorkflowStepStatusSkipped || item.Attempt != 0 {
			t.Fatalf("failed batch activated next item: %+v", item)
		}
		other := seedForEach(t, store, foreachTestSpec(), `[0,1]`)
		item, err := store.ResolveWorkflowForEach(ctx, other.ID, "batch")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, other.ID, item.Item.StepName, 1, item.Item.Input); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := store.MarkWorkflowStepAttemptStatus(ctx, other.ID, item.Item.StepName, WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`true`), nil)
			if err != nil && !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := store.CancelWorkflowRun(ctx, other.ID, "cancel"); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		if _, err := store.ResolveWorkflowForEach(ctx, other.ID, "batch"); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("cancelled batch resumed: %v", err)
		}
		if attempts, err := store.GetWorkflowStepAttempts(ctx, other.ID, item.Item.StepName); err != nil || len(attempts) != 1 || attempts[0].Status == WorkflowAttemptStatusRunning {
			t.Fatalf("cancel left a running attempt: %+v %v", attempts, err)
		}
	})
}

func TestWorkflowForEachEmptyArrayAndLeaseRecovery(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		empty := seedForEach(t, store, foreachTestSpec(), `[]`)
		if result, err := store.ResolveWorkflowForEach(ctx, empty.ID, "batch"); err != nil || !result.Complete || string(guardStep(t, store, empty.ID, "batch").Output) != "[]" {
			t.Fatalf("empty batch: %+v %v", result, err)
		}
		run := seedForEach(t, store, foreachTestSpec(), `[true]`)
		item, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, item.Item.StepName, 1, item.Item.Input); err != nil {
			t.Fatal(err)
		}
		switch store := store.(type) {
		case *MemStore:
			store.mu.Lock()
			store.workflowRunLeases[run.ID] = time.Now().Add(-time.Minute)
			store.mu.Unlock()
		case *PgStore:
			if _, err := store.pool.Exec(ctx, `UPDATE workflow_runs SET lease_until=now()-interval '1 minute' WHERE id=$1`, run.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, item.Item.StepName, WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`"expired"`), nil); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("expired item completion committed: %v", err)
		}
		if err := store.ScheduleWorkflowStepRetryWithHTTPStatus(ctx, run.ID, item.Item.StepName, 1, time.Now(), nil, "expired"); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("expired item retry committed: %v", err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		resumed, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
		if err != nil || resumed.Item == nil || resumed.Item.Attempt != 1 {
			t.Fatalf("lease recovery replayed item attempt: %+v %v", resumed, err)
		}
	})
}

func TestWorkflowForEachOutboundRecoveryRetainsReplayPolicy(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsafe mutation", true: "provider idempotency"}[safe], func(t *testing.T) {
			workflowOutboundRecoveryStores(t, func(t *testing.T, store Store, recoverRun func(string)) {
				ctx := context.Background()
				spec := foreachTestSpec()
				spec.Steps[1].ForEach.Action = api.WorkflowForEachActionSpec{Outbound: &api.WorkflowOutboundSpec{IntegrationID: "00000000-0000-0000-0000-000000000001", Method: "POST", Path: "/send", IdempotencySupported: safe}}
				run := seedForEach(t, store, spec, `[{},{}]`)
				item, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.StartWorkflowStep(ctx, run.ID, item.Item.StepName, 1, item.Item.Input); err != nil {
					t.Fatal(err)
				}
				recoverRun(run.ID)
				result, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
				if err != nil {
					t.Fatal(err)
				}
				if safe {
					if result.Item == nil || result.Item.Attempt != 1 || *result.Item.ForEachIndex != 0 {
						t.Fatalf("safe item lost attempt fence: %+v", result)
					}
				} else if !result.Complete || guardStep(t, store, run.ID, "batch").Status != WorkflowStepStatusDead || guardStep(t, store, run.ID, api.WorkflowForEachItemName("batch", 1)).Status != WorkflowStepStatusSkipped {
					t.Fatal("unknown unsafe mutation was replayed or next item activated")
				}
				if attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, item.Item.StepName); err != nil || len(attempts) != 1 || attempts[0].Status != WorkflowAttemptStatusFailed {
					t.Fatalf("interrupted item ledger: %+v %v", attempts, err)
				}
			})
		})
	}
}

func TestWorkflowForEachConcurrentInitializationAndAdmission(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedForEach(t, store, foreachTestSpec(), `[true,false]`)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				result, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
				if err != nil || result.Item == nil || *result.Item.ForEachIndex != 0 {
					t.Errorf("concurrent snapshot: %+v %v", result, err)
				}
			})
		}
		wg.Wait()
		steps, err := store.GetWorkflowSteps(ctx, run.ID)
		if err != nil || len(steps) != 5 {
			t.Fatalf("duplicate item records: %d %v", len(steps), err)
		}
		item := guardStep(t, store, run.ID, api.WorkflowForEachItemName("batch", 0))
		var admissions int
		var mu sync.Mutex
		for range 4 {
			wg.Go(func() {
				_, err := store.StartWorkflowStep(ctx, run.ID, item.StepName, 1, item.Input)
				if err == nil {
					mu.Lock()
					admissions++
					mu.Unlock()
				} else if !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if admissions != 1 {
			t.Fatalf("item dispatched %d times", admissions)
		}
	})
}

func TestWorkflowForEachMigrationRollbackAndReapply(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	ctx := context.Background()
	run := seedForEach(t, store, foreachTestSpec(), `[true]`)
	item, err := store.ResolveWorkflowForEach(ctx, run.ID, "batch")
	if err != nil {
		t.Fatal(err)
	}
	finishForEachItem(t, store, run.ID, item.Item, `true`)
	if _, err := store.CancelWorkflowRun(ctx, run.ID, "downgrade"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/20261003160000001_workflow_foreach.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	if _, err := store.pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_steps WHERE run_id=$1`, run.ID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("rollback left internal items or deleted parents: %d %v", count, err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_step_attempts WHERE run_id=$1`, run.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left orphan item attempts: %d %v", count, err)
	}
	if _, err := store.pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := store.pool.QueryRow(ctx, `SELECT workflow_foreach_item_name($1,0)`, "büç").Scan(&name); err != nil || name != api.WorkflowForEachItemName("büç", 0) {
		t.Fatalf("SQL/Go identity disagrees: %s %v", name, err)
	}
}
