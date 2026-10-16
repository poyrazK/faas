// ADR-1194: workflow operations park on timers and declared approvals without
// an execution, and wait failures are certain rather than reconciliations.
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type waitOperationFixture struct {
	workflowOperationFixture
	finished     *atomic.Int32
	orchestrator *sched.WorkflowOrchestrator
	inputs       *[]string
}

// waitOperationSetup deploys validate -> review (approval) -> cool (timer) -> finish.
func waitOperationSetup(t *testing.T, store state.Store, approver string, approvalTimeout time.Duration, limits ...api.OperationLimits) waitOperationFixture {
	t.Helper()
	ctx := context.Background()
	base := workflowOperationSetup(t, store)
	spec := api.WorkflowSpec{Name: "refund-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "review", WaitForCallback: true, Timeout: approvalTimeout, DependsOn: []string{"validate"}},
		{Name: "cool", WaitForDuration: time.Second, DependsOn: []string{"review"}},
		{Name: "finish", Path: "/finish", DependsOn: []string{"cool"}},
	}}
	raw, err := json.Marshal([]api.WorkflowSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: base.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:operation-waits", Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := base.ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: base.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: base.app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{
			Name: "refund", Workflow: spec.Name, Method: "POST", Path: "/refunds", Owner: api.OperationOwnerPlatformTenant,
			InputSchema:    []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`),
			OutputSchema:   []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`),
			ProgressStages: []string{"validate", "review", "cool", "finish"},
			Approvals:      []api.OperationApprovalSpec{{Step: "review", Approver: approver}},
			Limits:         operationLimitsArg(limits),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := waitOperationFixture{workflowOperationFixture: base, finished: &atomic.Int32{}}
	f.definition = def
	f.admission.DefinitionID, f.admission.IdempotencyKey = def.ID, "refund-1"
	executor := operationWorkflowExecutor{execute: func(_ context.Context, _ sched.WorkflowStepIdentity, path string, _ map[string]string, _ []byte) (int, []byte, error) {
		if path == "/finish" {
			f.finished.Add(1)
			return 200, []byte(`{"file":"refund.csv"}`), nil
		}
		return 200, []byte(`{"ok":true}`), nil
	}}
	f.orchestrator = sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	return f
}

func operationLimitsArg(limits []api.OperationLimits) *api.OperationLimits {
	if len(limits) == 0 {
		return nil
	}
	return &limits[0]
}

// drive advances due work until the operation satisfies ready.
func (f waitOperationFixture) drive(t *testing.T, id string, label string, ready func(state.Operation) bool) state.Operation {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if err := f.orchestrator.DispatchTick(ctx); err != nil {
			t.Fatalf("%s: dispatch: %v", label, err)
		}
		op := f.read(ctx, t, id)
		if ready(op) {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: operation state %s waiting=%+v failure=%q", label, op.State, op.Waiting, op.FailureCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f waitOperationFixture) decide(ctx context.Context, op state.Operation, kind, decision string) (state.Operation, error) {
	tenant := ""
	if kind == api.OperationApproverCustomer {
		tenant = f.tenant.ID
	}
	return f.store.(state.OperationApprovalStore).DecideOperationApproval(ctx, state.OperationApprovalInput{AccountID: f.account.ID, PlatformTenantID: tenant,
		OperationID: op.ID, Step: "review", ApproverKind: kind, ExpectedGeneration: op.Generation, Decision: decision, Comment: "checked the invoice",
		Actor: api.OperationApprovalActor{Kind: kind, ID: "key-1"}})
}

func waitingFor(kind, step string) func(state.Operation) bool {
	return func(op state.Operation) bool {
		return op.State == api.OperationWaiting && op.Waiting != nil && op.Waiting.Kind == kind && op.Waiting.Step == step
	}
}

func operationEventTypes(t *testing.T, f workflowOperationFixture, id string) []string {
	t.Helper()
	page, err := f.ops.OperationEvents(context.Background(), f.account.ID, f.tenant.ID, id, 0, api.OperationEventsPageMax)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, event := range page.Events {
		kinds = append(kinds, event.Type)
	}
	return kinds
}

func containsOrdered(have, want []string) bool {
	next := 0
	for _, kind := range have {
		if next < len(want) && kind == want[next] {
			next++
		}
	}
	return next == len(want)
}

func TestOperationApprovalResumesThroughTimer(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := waitOperationSetup(t, store, api.OperationApproverCustomer, time.Hour)
		op := f.start(t)
		waiting := f.drive(t, op.ID, "approval wait", waitingFor(api.OperationWaitApproval, "review"))
		if waiting.Waiting.Approver != api.OperationApproverCustomer || waiting.Waiting.Until == nil || waiting.Waiting.Until.Sub(waiting.Waiting.Since) != time.Hour || waiting.Progress == nil || waiting.Progress.Stage != "review" {
			t.Fatalf("approval waiting projection %+v progress %+v", waiting.Waiting, waiting.Progress)
		}
		if _, err := f.decide(ctx, waiting, api.OperationApproverAccount, api.OperationApprovalApproved); !errors.Is(err, state.ErrOperationApproverMismatch) {
			t.Fatalf("undeclared approver decided: %v", err)
		}
		stale := waiting
		stale.Generation++
		if _, err := f.decide(ctx, stale, api.OperationApproverCustomer, api.OperationApprovalApproved); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale generation decided: %v", err)
		}
		decided, err := f.decide(ctx, waiting, api.OperationApproverCustomer, api.OperationApprovalApproved)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := f.decide(ctx, waiting, api.OperationApproverCustomer, api.OperationApprovalApproved); err != nil || again.ID != decided.ID {
			t.Fatalf("repeated decision: %v", err)
		}
		if _, err := f.decide(ctx, waiting, api.OperationApproverCustomer, api.OperationApprovalRejected); !errors.Is(err, state.ErrOperationApprovalDecided) {
			t.Fatalf("changed decision: %v", err)
		}
		timer := f.drive(t, op.ID, "timer wait", waitingFor(api.OperationWaitTimer, "cool"))
		if timer.Waiting.Approver != "" || timer.Waiting.Until == nil || timer.Waiting.Until.Sub(timer.Waiting.Since) != time.Second {
			t.Fatalf("timer waiting projection %+v", timer.Waiting)
		}
		done := f.drive(t, op.ID, "completion", func(op state.Operation) bool { return op.State.Terminal() })
		if done.State != api.OperationSucceeded || done.Waiting != nil || f.finished.Load() != 1 {
			t.Fatalf("settled %s waiting=%+v finished=%d", done.State, done.Waiting, f.finished.Load())
		}
		kinds := operationEventTypes(t, f.workflowOperationFixture, op.ID)
		if !containsOrdered(kinds, []string{"accepted", "running", "waiting", "approval_decided", "resumed", "waiting", "resumed", "succeeded"}) {
			t.Fatalf("operation history %v", kinds)
		}
	})
}

func TestOperationApprovalRejectionFailsWithoutReconciliation(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := waitOperationSetup(t, store, api.OperationApproverAccount, time.Hour)
		op := f.start(t)
		waiting := f.drive(t, op.ID, "approval wait", waitingFor(api.OperationWaitApproval, "review"))
		if _, err := f.decide(context.Background(), waiting, api.OperationApproverAccount, api.OperationApprovalRejected); err != nil {
			t.Fatal(err)
		}
		done := f.drive(t, op.ID, "rejection", func(op state.Operation) bool {
			return op.State.Terminal() || op.State == api.OperationRequiresReconciliation
		})
		if done.State != api.OperationFailed || done.FailureCode != api.OperationFailureApprovalDenied || f.finished.Load() != 0 {
			t.Fatalf("rejected approval settled %s %q finished=%d", done.State, done.FailureCode, f.finished.Load())
		}
	})
}

func TestOperationApprovalExpiresWithoutReconciliation(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := waitOperationSetup(t, store, api.OperationApproverAccount, time.Second)
		op := f.start(t)
		waiting := f.drive(t, op.ID, "approval wait", waitingFor(api.OperationWaitApproval, "review"))
		done := f.drive(t, op.ID, "expiry", func(op state.Operation) bool {
			return op.State.Terminal() || op.State == api.OperationRequiresReconciliation
		})
		if done.State != api.OperationFailed || done.FailureCode != api.OperationFailureApprovalExpiry || f.finished.Load() != 0 {
			t.Fatalf("expired approval settled %s %q", done.State, done.FailureCode)
		}
		if _, err := f.decide(context.Background(), waiting, api.OperationApproverAccount, api.OperationApprovalApproved); err == nil {
			t.Fatal("an expired approval accepted a decision")
		}
	})
}

func TestOperationCancellationWhileWaitingIsCertain(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := waitOperationSetup(t, store, api.OperationApproverAccount, time.Hour)
		op := f.start(t)
		f.drive(t, op.ID, "approval wait", waitingFor(api.OperationWaitApproval, "review"))
		cancelled, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1)
		if err != nil || cancelled.State != api.OperationCancelled || cancelled.Waiting != nil {
			t.Fatalf("cancelled waiting operation %+v: %v", cancelled, err)
		}
		steps, err := store.GetWorkflowSteps(ctx, op.WorkflowRunID)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range steps {
			if step.StepName == "review" && step.Status != state.WorkflowStepStatusSkipped {
				t.Fatalf("parked approval step left %s", step.Status)
			}
		}
	})
}
