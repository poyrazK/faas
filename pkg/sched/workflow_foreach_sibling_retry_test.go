// adr: 500
package sched_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// waitForPersistedRetry blocks until the failing item's retry is stored, or
// five seconds pass.
func waitForPersistedRetry(ctx context.Context, store state.Store, runID, failing string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		steps, err := store.GetWorkflowSteps(ctx, runID)
		if err == nil {
			for _, step := range steps {
				if step.StepName == failing && step.Status == state.WorkflowStepStatusPending && step.NextRetryAt != nil {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// siblingRetryExecutor fails one item at once. With holdSiblings it keeps the
// other items in flight until that failure's retry is persisted.
type siblingRetryExecutor struct {
	store        state.Store
	runID        string
	failing      string
	holdSiblings bool
	mu           sync.Mutex
	calls        map[string]int
}

func (e *siblingRetryExecutor) ExecuteStep(ctx context.Context, _, _ string, _ string, headers map[string]string, input []byte, _ time.Duration) (int, []byte, error) {
	name := headers["X-Faas-Workflow-Step"]
	if name == "summary" {
		return 200, slices.Clone(input), nil
	}
	e.mu.Lock()
	e.calls[name]++
	e.mu.Unlock()
	if name == e.failing {
		return 503, []byte(`{}`), nil
	}
	if e.holdSiblings {
		waitForPersistedRetry(ctx, e.store, e.runID, e.failing)
	}
	return 200, slices.Clone(input), nil
}

// orchestrationStore is the store surface the orchestrator type-asserts; a
// wrapper must keep it or item calls lose their attempt checks.
type orchestrationStore interface {
	state.Store
	state.WorkflowOperationStore
	state.WorkflowOutboundStore
	state.WorkflowOperationDeadlineStore
	state.WorkflowRunLeaseStore
	state.ManagedWorkflowStepCommitter
}

// lateSiblingStore holds every item but the failing one before it starts
// until the failing item's retry is persisted.
type lateSiblingStore struct {
	orchestrationStore
	runID   string
	failing string
}

func (s *lateSiblingStore) StartWorkflowStep(ctx context.Context, runID, stepName string, attempt int, input json.RawMessage) (json.RawMessage, error) {
	if runID == s.runID && stepName != s.failing && stepName != "summary" {
		waitForPersistedRetry(ctx, s.orchestrationStore, s.runID, s.failing)
	}
	return s.orchestrationStore.StartWorkflowStep(ctx, runID, stepName, attempt, input)
}

// TestWorkflowForEachSiblingRetryKeepsBatchRunning pins H9-155: scheduling one
// for_each item's retry parked the whole run mid-batch. On production-us
// siblings still in flight had their 200 rejected as stale and were re-run by
// lease recovery 5.5 minutes later; siblings not yet started could never
// start, and the dispatch pass spun on their rejected starts.
func TestWorkflowForEachSiblingRetryKeepsBatchRunning(t *testing.T) {
	cases := []struct {
		name         string
		holdSiblings bool
		lateStart    bool
	}{
		{name: "siblings in flight", holdSiblings: true},
		{name: "siblings not yet started", lateStart: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				spec := foreachOrchestrationSpec()
				spec.Steps[1].ForEach.MaxParallel = 3
				run := sched.CreateGuardedRun(t, store, spec, `{"items":[0,1,2]}`)
				failing := api.WorkflowForEachItemName("batch", 2)
				executor := &siblingRetryExecutor{store: store, runID: run.ID, failing: failing, holdSiblings: tc.holdSiblings, calls: map[string]int{}}
				dispatchStore := store
				if tc.lateStart {
					full, ok := store.(orchestrationStore)
					if !ok {
						t.Fatalf("%T lacks the orchestrator's optional store interfaces", store)
					}
					dispatchStore = &lateSiblingStore{orchestrationStore: full, runID: run.ID, failing: failing}
				}
				if err := sched.NewWorkflowOrchestrator(dispatchStore, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
					t.Fatalf("DispatchTick: %v", err)
				}

				steps, err := store.GetWorkflowSteps(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				var retryAt *time.Time
				for _, step := range steps {
					if step.StepName == failing {
						retryAt = step.NextRetryAt
					}
					for i := range 2 {
						if step.StepName == api.WorkflowForEachItemName("batch", i) && (step.Status != state.WorkflowStepStatusSucceeded || step.Attempt != 1) {
							t.Fatalf("sibling %s = %s attempt %d, want succeeded on its first attempt", step.StepName, step.Status, step.Attempt)
						}
					}
				}
				executor.mu.Lock()
				for i := range 2 {
					if n := executor.calls[api.WorkflowForEachItemName("batch", i)]; n != 1 {
						t.Fatalf("sibling %d executed %d times, want once", i, n)
					}
				}
				executor.mu.Unlock()

				// The pass parks the run at the failed item's retry once the batch drains.
				final, err := store.GetWorkflowRun(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if retryAt == nil || final.Status != state.WorkflowRunStatusPending || !final.ScheduledFor.Equal(*retryAt) {
					t.Fatalf("run after batch = %s scheduled %s, want pending at the retry %v", final.Status, final.ScheduledFor, retryAt)
				}
			})
		})
	}
}
