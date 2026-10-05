package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func joinTestSpec() api.WorkflowSpec {
	return api.WorkflowSpec{Name: "joined", Steps: []api.WorkflowStepSpec{
		{Name: "a", Run: "a"},
		{Name: "b", Run: "b", When: &api.WorkflowGuardSpec{Ref: "input.active", Op: "eq", Value: json.RawMessage("true")}},
		{Name: "merge", DependsOn: []string{"a", "b"}, Join: &api.WorkflowJoinSpec{OutputFrom: []string{"b", "a"}}},
		{Name: "next", Run: "next", DependsOn: []string{"merge"}},
	}}
}

func TestWorkflowJoinWaitsSnapshotsOutputAndSurvivesRecovery(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedGuardRunWithSpec(t, store, joinTestSpec(), `{"active":true}`)
		if _, err := store.StartWorkflowStep(ctx, run.ID, "merge", 1, json.RawMessage(`{}`)); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("join allocated an executor attempt: %v", err)
		}
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "a", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"amount":9007199254740993}`), nil); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || ready {
			t.Fatalf("join selected early finisher: ready=%v err=%v", ready, err)
		}
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "b"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "b", 1, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err := store.ScheduleWorkflowStepRetry(ctx, run.ID, "b", 1, time.Now().Add(time.Hour), "retry"); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || ready {
			t.Fatalf("join bypassed branch retry: %v %v", ready, err)
		}
		// Complete the higher-priority branch last. Selection follows the
		// definition order, independently of storage and completion order.
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "b", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"amount":9007199254740994,"null":null}`), nil); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
			t.Fatalf("join not completed: %v %v", ready, err)
		}
		first := guardStep(t, store, run.ID, "merge")
		want := json.RawMessage(`{"source":"b","value":{"amount":9007199254740994,"null":null}}`)
		if first.Status != WorkflowStepStatusSucceeded || first.Attempt != 0 || !equalWorkflowJSON(first.Output, want) || first.FinishedAt == nil {
			t.Fatalf("wrong join snapshot: %+v", first)
		}
		if attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "merge"); err != nil || len(attempts) != 0 {
			t.Fatalf("join consumed attempts: %v %v", attempts, err)
		}
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "b", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"replaced":true}`), nil); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
			t.Fatalf("recovery lost join: %v %v", ready, err)
		}
		final := guardStep(t, store, run.ID, "merge")
		if !equalWorkflowJSON(final.Output, want) || !first.FinishedAt.Equal(*final.FinishedAt) {
			t.Fatal("recovery changed selected output or finish time")
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "next", 1, json.RawMessage(`{}`)); err != nil {
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
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
			t.Fatalf("lease recovery lost join: %v %v", ready, err)
		}
		if got := guardStep(t, store, run.ID, "merge"); !equalWorkflowJSON(got.Output, want) || !first.FinishedAt.Equal(*got.FinishedAt) {
			t.Fatal("lease recovery changed the join snapshot")
		}
		// Returned output must not alias the memory store's committed snapshot.
		final.Output[0] = 'x'
		if !equalWorkflowJSON(guardStep(t, store, run.ID, "merge").Output, want) {
			t.Fatal("inspection changed stored join output")
		}
	})
}

func TestWorkflowJoinConditionalSkipsAndCancellation(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedGuardRunWithSpec(t, store, joinTestSpec(), `{"active":false}`)
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "a", WorkflowStepStatusSucceeded, 1, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "b"); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
			t.Fatalf("false branch blocked join: %v %v", ready, err)
		}
		if got := guardStep(t, store, run.ID, "merge"); !equalWorkflowJSON(got.Output, json.RawMessage(`{"source":"a","value":null}`)) {
			t.Fatalf("empty branch output not represented: %s", got.Output)
		}
		other := seedGuardRunWithSpec(t, store, joinTestSpec(), `{"active":false}`)
		if err := store.MarkWorkflowStepStatus(ctx, other.ID, "a", WorkflowStepStatusSucceeded, 1, json.RawMessage(`true`), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ResolveWorkflowStepGuard(ctx, other.ID, "b"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := store.ResolveWorkflowStepJoin(ctx, other.ID, "merge")
			if err != nil && !errors.Is(err, ErrWorkflowGuardNotReady) {
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
		current, err := store.GetWorkflowRun(ctx, other.ID)
		if err != nil || current.Status != WorkflowRunStatusFailed {
			t.Fatalf("join resurrected cancellation: %+v %v", current, err)
		}
		if _, err := store.ResolveWorkflowStepJoin(ctx, other.ID, "merge"); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("join accepted cancelled run: %v", err)
		}
	})
}

func TestWorkflowJoinPriorityWinsOverCompletionOrder(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedGuardRunWithSpec(t, store, joinTestSpec(), `{"active":true}`)
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "b"); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"b", "a"} {
			if err := store.MarkWorkflowStepStatus(ctx, run.ID, name, WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
				t.Fatal(err)
			}
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
			t.Fatalf("join did not complete: %v %v", ready, err)
		}
		if got := guardStep(t, store, run.ID, "merge"); !equalWorkflowJSON(got.Output, json.RawMessage(`{"source":"b","value":{"ok":true}}`)) {
			t.Fatalf("completion order overrode explicit priority: %s", got.Output)
		}
	})
}

func TestWorkflowJoinCanMergeAnInactiveNestedJoin(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		spec := joinTestSpec()
		spec.Steps[0].When = spec.Steps[1].When
		spec.Steps = append(spec.Steps,
			api.WorkflowStepSpec{Name: "active", Run: "active"},
			api.WorkflowStepSpec{Name: "outer", DependsOn: []string{"merge", "active"}, Join: &api.WorkflowJoinSpec{OutputFrom: []string{"merge", "active"}}},
		)
		run := seedGuardRunWithSpec(t, store, spec, `{"active":false}`)
		for _, name := range []string{"a", "b"} {
			if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, name); err != nil || matched {
				t.Fatalf("inactive branch decision: %v %v", matched, err)
			}
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
			t.Fatalf("inactive inner join did not close: %v %v", ready, err)
		}
		if got := guardStep(t, store, run.ID, "merge"); got.Status != WorkflowStepStatusSkipped || got.SkipReason == nil || *got.SkipReason != WorkflowSkipDependencySkipped {
			t.Fatalf("inner join not conditionally inactive: %+v", got)
		}
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "active", WorkflowStepStatusSucceeded, 1, json.RawMessage(`[true,null,9007199254740993]`), nil); err != nil {
			t.Fatal(err)
		}
		if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "outer"); err != nil || !ready {
			t.Fatalf("outer join rejected inactive inner join: %v %v", ready, err)
		}
		if got := guardStep(t, store, run.ID, "outer"); !equalWorkflowJSON(got.Output, json.RawMessage(`{"source":"active","value":[true,null,9007199254740993]}`)) {
			t.Fatalf("nested join lost array output: %+v", got)
		}
	})
}

func TestWorkflowJoinChecksEntireSkipAncestry(t *testing.T) {
	for _, test := range []struct {
		name, parentStatus, reason, wantReason string
		ready                                  bool
	}{
		{"transitive guard skip", WorkflowStepStatusSucceeded, WorkflowSkipDependencySkipped, "", true},
		{"waiting ancestor", WorkflowStepStatusPending, WorkflowSkipDependencySkipped, "", false},
		{"failed sibling ancestor", WorkflowStepStatusFailed, WorkflowSkipDependencySkipped, WorkflowSkipDependencyFailed, true},
		{"dead sibling ancestor", WorkflowStepStatusDead, WorkflowSkipDependencySkipped, WorkflowSkipDependencyFailed, true},
		{"inactive exception route", WorkflowStepStatusSucceeded, WorkflowSkipRouteNotTaken, WorkflowSkipDependencyFailed, true},
		{"legacy skip without cause", WorkflowStepStatusSucceeded, "", WorkflowSkipDependencyFailed, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			workflowGuardStores(t, func(t *testing.T, store Store) {
				ctx := context.Background()
				spec := joinTestSpec()
				spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: "parent", Run: "parent"}, api.WorkflowStepSpec{Name: "child", Run: "child", DependsOn: []string{"b", "parent"}})
				spec.Steps[2].DependsOn = []string{"a", "child"}
				spec.Steps[2].Join.OutputFrom = []string{"child", "a"}
				run := seedGuardRunWithSpec(t, store, spec, `{"active":false}`)
				if err := store.MarkWorkflowStepStatus(ctx, run.ID, "a", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "b"); err != nil {
					t.Fatal(err)
				}
				if test.parentStatus != WorkflowStepStatusPending {
					if err := store.MarkWorkflowStepStatus(ctx, run.ID, "parent", test.parentStatus, 1, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if test.reason == "" {
					if err := store.MarkWorkflowStepStatus(ctx, run.ID, "child", WorkflowStepStatusSkipped, 0, nil, nil); err != nil {
						t.Fatal(err)
					}
				} else if err := store.SkipWorkflowStep(ctx, run.ID, "child", test.reason); err != nil {
					t.Fatal(err)
				}
				ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge")
				if err != nil || ready != test.ready {
					t.Fatalf("join ready=%v err=%v", ready, err)
				}
				got := guardStep(t, store, run.ID, "merge")
				if test.wantReason != "" {
					if got.Status != WorkflowStepStatusSkipped || got.SkipReason == nil || *got.SkipReason != test.wantReason || len(got.Output) != 0 {
						t.Fatalf("unsafe skip activated join: %+v", got)
					}
				} else if test.ready && got.Status != WorkflowStepStatusSucceeded {
					t.Fatalf("conditional descendant blocked join: %+v", got)
				}
				if !test.ready {
					if err := store.MarkWorkflowStepStatus(ctx, run.ID, "parent", WorkflowStepStatusSucceeded, 1, nil, nil); err != nil {
						t.Fatal(err)
					}
					if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready || guardStep(t, store, run.ID, "merge").Status != WorkflowStepStatusSucceeded {
						t.Fatalf("join stranded after ancestor finished: %v %v", ready, err)
					}
				}
			})
		})
	}
}
