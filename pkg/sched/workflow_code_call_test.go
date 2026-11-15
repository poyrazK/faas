package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// Call is the fake SDK's wf.call: a recorded call returns its output or its
// error; a new one is reported with its callee and input.
func (w *codeTestWF) Call(name, function string, input any) (json.RawMessage, error) {
	entry, recorded := w.claim(name, workflowcode.KindCall)
	switch {
	case recorded && entry.Status == workflowcode.EntrySucceeded:
		return entry.Output, nil
	case recorded && entry.Status == workflowcode.EntryFailed:
		return nil, errors.New(entry.Error)
	case recorded:
		panic(errCodeSuspend)
	}
	raw, _ := json.Marshal(input)
	w.ops = append(w.ops, workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindCall, Function: function, Input: raw})
	panic(errCodeSuspend)
}

// callWorkflowRun is a code run whose app may call a sibling "users" app.
func callWorkflowRun(t *testing.T, store state.Store) (*state.WorkflowRun, state.App) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	callee, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "users-" + uuid.NewString()[:8],
		Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	workflowApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "orders-" + uuid.NewString()[:8],
		Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	manifest := workflowApp.Manifest
	manifest.FunctionCallables = map[string]string{"users": callee.Slug}
	// A manifest whose only setting is the callables must survive the
	// store's round trip (it used to be stored as {}).
	if _, err := store.UpdateApp(ctx, workflowApp.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	spec := codeSpec(nil)
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: workflowApp.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{"email":"a@b"}`), DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return run, callee
}

func callStep(t *testing.T, store state.Store, runID, name string) (*state.WorkflowStep, workflowcode.Descriptor) {
	t.Helper()
	steps, err := store.GetWorkflowSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.StepName == name {
			descriptor, _ := workflowcode.DecodeDescriptor(step.Input)
			return step, descriptor
		}
	}
	t.Fatalf("no step %q", name)
	return nil, workflowcode.Descriptor{}
}

// ADR-20261113120824 §2: wf.call enqueues the callee's async invocation with
// the operation and parks the run; the invocation's completion settles the
// operation with the callee's output and wakes the run, which then finishes.
func TestCodeWorkflowCallParksUntilTheCalleeFinishes(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run, callee := callWorkflowRun(t, store)
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			user, err := wf.Call("users", "users", map[string]string{"email": "a@b"})
			if err != nil {
				return nil, err
			}
			return map[string]json.RawMessage{"user": user}, nil
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		dispatchCode(t, orchestrator)

		parked, _ := store.GetWorkflowRun(ctx, run.ID)
		step, descriptor := callStep(t, store, run.ID, "users")
		if parked.Status != state.WorkflowRunStatusAwaitingEvent || step.Status != state.WorkflowStepStatusPending ||
			descriptor.Kind != workflowcode.KindCall || descriptor.Function != "users" || descriptor.InvocationID == "" {
			t.Fatalf("after the call: run=%s step=%s error=%s descriptor=%+v", parked.Status, step.Status, stepError(step), descriptor)
		}
		inv, err := store.InvocationByID(ctx, descriptor.InvocationID)
		if err != nil || inv.AppID != callee.ID || inv.State != state.InvocationPending || !sched.WorkflowJSONEqual(inv.Payload, []byte(`{"email":"a@b"}`)) {
			t.Fatalf("callee invocation = %+v, %v", inv, err)
		}
		var headers map[string]string
		_ = json.Unmarshal(inv.Headers, &headers)
		if headers[api.FunctionCallHeader] != "1" || headers["Idempotency-Key"] == "" {
			t.Fatalf("callee headers = %v", headers)
		}
		send, err := store.(state.InvocationSendReader).InvocationSend(ctx, inv.ID)
		if err != nil || send.SenderAppID != run.AppID || send.WorkflowRunID != run.ID || send.WorkflowOp != "users" {
			t.Fatalf("send record = %+v, %v", send, err)
		}
		calls := executor.calls
		dispatchCode(t, orchestrator)
		if executor.calls != calls {
			t.Fatalf("the code ran again while the call was pending: %d calls", executor.calls)
		}

		if _, err := store.ClaimInvocation(ctx, inv.ID, "instance-1", 30); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{"id":"u1"}`)); err != nil {
			t.Fatal(err)
		}
		woken, _ := store.GetWorkflowRun(ctx, run.ID)
		if step, _ := callStep(t, store, run.ID, "users"); step.Status != state.WorkflowStepStatusSucceeded || woken.Status != state.WorkflowRunStatusPending {
			t.Fatalf("after completion: step=%s run=%s", step.Status, woken.Status)
		}
		dispatchCode(t, orchestrator)
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || !sched.WorkflowJSONEqual(final.Output, []byte(`{"user":{"id":"u1"}}`)) {
			t.Fatalf("final run = %s %s", final.Status, final.Output)
		}
	})
}

// A name the workflow app may not call fails the operation without
// enqueueing anything; the code sees the error and may handle it.
func TestCodeWorkflowCallToUnknownFunctionFails(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run, _ := callWorkflowRun(t, store)
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			if _, err := wf.Call("billing", "billing", nil); err != nil {
				return map[string]string{"handled": err.Error()}, nil //nolint:nilerr // the workflow handles the failed call
			}
			return nil, errors.New("the call should have failed")
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		dispatchCode(t, orchestrator)
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || !sched.WorkflowJSONEqual(final.Output, []byte(`{"handled":"function \"billing\" is not callable from this workflow"}`)) {
			t.Fatalf("final run = %s %s", final.Status, final.Output)
		}
	})
}

func stepError(step *state.WorkflowStep) string {
	if step.Error == nil {
		return ""
	}
	return *step.Error
}
