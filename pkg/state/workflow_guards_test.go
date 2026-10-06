package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func workflowGuardStores(t *testing.T, test func(*testing.T, Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { test(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

func seedGuardRun(t *testing.T, store Store, input string) *WorkflowRun {
	t.Helper()

	spec := api.WorkflowSpec{Name: "guarded", Steps: []api.WorkflowStepSpec{
		{Name: "send", Run: "send", When: &api.WorkflowGuardSpec{Ref: "input.active", Op: "eq", Value: json.RawMessage("true")}},
		{Name: "next", Run: "next", DependsOn: []string{"send"}},
	}}
	return seedGuardRunWithSpec(t, store, spec, input)
}

func seedGuardRunWithSpec(t *testing.T, store Store, spec api.WorkflowSpec, input string) *WorkflowRun {
	t.Helper()

	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "guard-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(spec)
	run := &WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(input), DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	steps := make([]*WorkflowStep, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		steps = append(steps, &WorkflowStep{StepName: step.Name})
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, steps); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	return run
}

func guardStep(t *testing.T, store Store, runID, name string) *WorkflowStep {
	t.Helper()
	steps, err := store.GetWorkflowSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.StepName == name {
			return step
		}
	}
	t.Fatal("step missing")
	return nil
}

func TestWorkflowGuardDecisionSurvivesRetryAndRecovery(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedGuardRun(t, store, `{"active":true}`)
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, run.Input); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("started before decision: %v", err)
		}
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || !matched {
			t.Fatalf("decision=%v err=%v", matched, err)
		}
		first := guardStep(t, store, run.ID, "send")
		if first.WhenMatched == nil || !*first.WhenMatched || first.WhenEvaluatedAt == nil || first.Attempt != 0 {
			t.Fatalf("missing decision: %+v", first)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, run.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.ScheduleWorkflowStepRetry(ctx, run.ID, "send", 1, time.Now().Add(-time.Second), "retry"); err != nil {
			t.Fatal(err)
		}
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || !matched {
			t.Fatalf("retry changed decision: %v %v", matched, err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 2, run.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || !matched {
			t.Fatalf("recovery changed decision: %v %v", matched, err)
		}
		final := guardStep(t, store, run.ID, "send")
		if !final.WhenEvaluatedAt.Equal(*first.WhenEvaluatedAt) {
			t.Fatal("guard reevaluated after recovery")
		}
		if err := store.SkipWorkflowStep(ctx, run.ID, "next", WorkflowSkipDependencySkipped); err != nil {
			t.Fatal(err)
		}
		if err := store.SkipWorkflowStep(ctx, run.ID, "next", WorkflowSkipRouteNotTaken); err != nil {
			t.Fatal(err)
		}
		if got := guardStep(t, store, run.ID, "next"); got.SkipReason == nil || *got.SkipReason != WorkflowSkipDependencySkipped {
			t.Fatal("repeated skip overwrote reason")
		}
		// Inspection must not hand out mutable references to stored decisions.
		*final.WhenMatched = false
		if !*guardStep(t, store, run.ID, "send").WhenMatched {
			t.Fatal("inspection mutated stored decision")
		}
	})
}

func TestWorkflowGuardFalseAndCancellationAreAtomic(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedGuardRun(t, store, `{"active":false}`)
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || matched {
			t.Fatalf("decision=%v err=%v", matched, err)
		}
		step := guardStep(t, store, run.ID, "send")
		if step.Status != WorkflowStepStatusSkipped || step.WhenMatched == nil || *step.WhenMatched || step.WhenEvaluatedAt == nil || step.SkipReason == nil || *step.SkipReason != WorkflowSkipWhenFalse || step.FinishedAt == nil || step.Attempt != 0 {
			t.Fatalf("false decision not atomically skipped: %+v", step)
		}
		if attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send"); err != nil || len(attempts) != 0 {
			t.Fatalf("guard allocated execution attempt: %v %v", attempts, err)
		}
		if err := store.SkipWorkflowStep(ctx, run.ID, "send", WorkflowSkipDependencySkipped); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CancelWorkflowRun(ctx, run.ID, "cancel"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("cancelled decision accepted: %v", err)
		}
		if got := guardStep(t, store, run.ID, "send"); *got.SkipReason != WorkflowSkipWhenFalse || !got.WhenEvaluatedAt.Equal(*step.WhenEvaluatedAt) {
			t.Fatal("cancellation changed decision")
		}

		other := seedGuardRun(t, store, `{"active":false}`)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := store.ResolveWorkflowStepGuard(ctx, other.ID, "send")
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
		current, _ := store.GetWorkflowRun(ctx, other.ID)
		if current.Status != WorkflowRunStatusFailed || guardStep(t, store, other.ID, "send").Status != WorkflowStepStatusSkipped {
			t.Fatal("guard resurrected cancelled run")
		}
	})
}

func TestWorkflowGuardDecisionSurvivesLeaseExpiry(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run := seedGuardRun(t, store, `{"active":true}`)
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil {
			t.Fatal(err)
		}
		first := guardStep(t, store, run.ID, "send")
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, run.Input); err != nil {
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
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || !matched {
			t.Fatalf("lease recovery lost decision: %v %v", matched, err)
		}
		final := guardStep(t, store, run.ID, "send")
		if !final.WhenEvaluatedAt.Equal(*first.WhenEvaluatedAt) {
			t.Fatal("lease expiry reevaluated guard")
		}
	})
}

func TestWorkflowGuardWaitsForDependencyAndUsesFirstDecision(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		spec := api.WorkflowSpec{Name: "lookup", Steps: []api.WorkflowStepSpec{
			{Name: "lookup", Run: "lookup"},
			{Name: "send", Run: "send", DependsOn: []string{"lookup"}, When: &api.WorkflowGuardSpec{Ref: "steps.lookup.output.active", Op: "eq", Value: json.RawMessage("true")}},
		}}
		run := seedGuardRunWithSpec(t, store, spec, `{}`)
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); !errors.Is(err, ErrWorkflowGuardNotReady) {
			t.Fatalf("evaluated pending dependency: %v", err)
		}
		if guardStep(t, store, run.ID, "send").WhenMatched != nil {
			t.Fatal("pending dependency allocated decision")
		}
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "lookup", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"active":true}`), nil); err != nil {
			t.Fatal(err)
		}
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || !matched {
			t.Fatalf("dependency result ignored: %v %v", matched, err)
		}
		// Even an administrative replacement of a dependency result cannot
		// alter the already-recorded decision during replay.
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "lookup", WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"active":false}`), nil); err != nil {
			t.Fatal(err)
		}
		if matched, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil || !matched {
			t.Fatalf("guard reevaluated a dependency: %v %v", matched, err)
		}
	})
}
