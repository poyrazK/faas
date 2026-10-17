// adr: 1197
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// signalOperationSetup deploys validate -> payment (event wait) -> finish for
// orders keyed by subject order/<order_id>.
func signalOperationSetup(t *testing.T, store state.Store, eventTimeout time.Duration) waitOperationFixture {
	t.Helper()
	ctx := context.Background()
	base := workflowOperationSetup(t, store)
	spec := api.WorkflowSpec{Name: "order-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "payment", WaitForEvent: "payment.succeeded", Timeout: eventTimeout, DependsOn: []string{"validate"}},
		{Name: "finish", Path: "/finish", DependsOn: []string{"payment"}, Input: json.RawMessage(`{"charge":"{{steps.payment.output.charge}}"}`)},
	}}
	raw, err := json.Marshal([]api.WorkflowSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: base.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:operation-signals", Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := base.ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: base.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: base.app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{
			Name: "fulfil", Workflow: spec.Name, Method: "POST", Path: "/orders", Owner: api.OperationOwnerPlatformTenant,
			InputSchema:    []byte(`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"string"}},"additionalProperties":false}`),
			OutputSchema:   []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`),
			ProgressStages: []string{"validate", "payment", "finish"},
			Subject:        &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := waitOperationFixture{workflowOperationFixture: base, finished: &atomic.Int32{}}
	f.definition = def
	f.admission.DefinitionID, f.admission.IdempotencyKey, f.admission.Input = def.ID, "order-1", []byte(`{"order_id":"ord_1"}`)
	f.inputs = &[]string{}
	executor := operationWorkflowExecutor{execute: func(_ context.Context, _ sched.WorkflowStepIdentity, path string, _ map[string]string, input []byte) (int, []byte, error) {
		if path == "/finish" {
			f.finished.Add(1)
			*f.inputs = append(*f.inputs, string(input))
			return 200, []byte(`{"file":"order.pdf"}`), nil
		}
		return 200, []byte(`{"ok":true}`), nil
	}}
	f.orchestrator = sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	return f
}

func (f waitOperationFixture) signal(ctx context.Context, id, signalID, event, payload string) (api.OperationSignalReceipt, error) {
	return f.store.(state.OperationSignalStore).SignalOperation(ctx, state.OperationSignalInput{AccountID: f.account.ID, OperationID: id, SignalID: signalID, Event: event, Payload: json.RawMessage(payload)})
}

func TestOperationSignalResumesEventWait(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := signalOperationSetup(t, store, time.Hour)
		op := f.start(t)
		waiting := f.drive(t, op.ID, "event wait", waitingFor(api.OperationWaitEvent, "payment"))
		if waiting.Waiting.Event != "payment.succeeded" || waiting.Waiting.Until == nil {
			t.Fatalf("waiting state %+v", waiting.Waiting)
		}
		if _, err := f.signal(ctx, op.ID, "evt_unknown", "payment.failed", `{}`); !errors.Is(err, state.ErrOperationSignalUnexpected) {
			t.Fatalf("undeclared event accepted: %v", err)
		}
		receipt, err := f.signal(ctx, op.ID, "evt_1", "payment.succeeded", `{"charge":"ch_1"}`)
		if err != nil || !receipt.Waiting || receipt.Duplicate {
			t.Fatalf("signal receipt %+v %v", receipt, err)
		}
		again, err := f.signal(ctx, op.ID, "evt_1", "payment.succeeded", `{ "charge": "ch_1" }`)
		if err != nil || !again.Duplicate {
			t.Fatalf("repeat signal %+v %v", again, err)
		}
		if _, err := f.signal(ctx, op.ID, "evt_1", "payment.succeeded", `{"charge":"ch_2"}`); !errors.Is(err, state.ErrOperationSignalConflict) {
			t.Fatalf("signal id reused with another payload: %v", err)
		}
		done := f.drive(t, op.ID, "resume", func(op state.Operation) bool { return op.State == api.OperationSucceeded })
		if f.finished.Load() != 1 || len(*f.inputs) != 1 || workflowCanonical(t, []byte((*f.inputs)[0])) != `{"charge":"ch_1"}` {
			t.Fatalf("finish ran %d times with %v", f.finished.Load(), *f.inputs)
		}
		types := operationEventTypes(t, f.workflowOperationFixture, done.ID)
		if !containsOrdered(types, []string{"waiting", "signal_received", "resumed", "succeeded"}) || countOf(types, "signal_received") != 1 {
			t.Fatalf("events %v", types)
		}
		if _, err := f.signal(ctx, op.ID, "evt_2", "payment.succeeded", `{}`); !errors.Is(err, state.ErrOperationNotActive) {
			t.Fatalf("settled operation took a signal: %v", err)
		}
		if again, err := f.signal(ctx, op.ID, "evt_1", "payment.succeeded", `{"charge":"ch_1"}`); err != nil || !again.Duplicate {
			t.Fatalf("a delivered signal must stay idempotent after settlement: %+v %v", again, err)
		}
	})
}

func TestOperationSignalBeforeWaitIsHeld(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := signalOperationSetup(t, store, time.Hour)
		op := f.start(t)
		receipt, err := f.signal(ctx, op.ID, "early", "payment.succeeded", `{"charge":"ch_early"}`)
		if err != nil || receipt.Waiting {
			t.Fatalf("early signal %+v %v", receipt, err)
		}
		f.drive(t, op.ID, "held signal", func(op state.Operation) bool { return op.State == api.OperationSucceeded })
		if workflowCanonical(t, []byte((*f.inputs)[0])) != `{"charge":"ch_early"}` {
			t.Fatalf("held signal payload lost: %v", *f.inputs)
		}
	})
}

func TestOperationEventWaitTimesOut(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := signalOperationSetup(t, store, time.Second)
		op := f.start(t)
		f.drive(t, op.ID, "event wait", waitingFor(api.OperationWaitEvent, "payment"))
		got := f.drive(t, op.ID, "timeout", func(op state.Operation) bool { return op.State.Terminal() })
		if got.State != api.OperationFailed || got.FailureCode != api.OperationFailureEventExpiry || f.finished.Load() != 0 {
			t.Fatalf("timed out wait settled %s/%s finished=%d", got.State, got.FailureCode, f.finished.Load())
		}
	})
}

func TestOperationSubjectSignalTargetsActiveOperations(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := signalOperationSetup(t, store, time.Hour)
		first := f.start(t)
		f.admission.IdempotencyKey = "order-1-again"
		second := f.start(t)
		f.admission.IdempotencyKey, f.admission.Input = "order-2", []byte(`{"order_id":"ord_2"}`)
		other := f.start(t)
		signals := store.(state.OperationSignalStore)
		ids, err := signals.OperationsForSubject(ctx, f.account.ID, f.app.ID, first.Scope, "fulfil", api.OperationSubject{Type: "order", ID: "ord_1"}, 10)
		if err != nil || len(ids) != 2 || !slices.Contains(ids, first.ID) || !slices.Contains(ids, second.ID) || slices.Contains(ids, other.ID) {
			t.Fatalf("subject targets %v %v", ids, err)
		}
		if ids, err := signals.OperationsForSubject(ctx, f.account.ID, f.app.ID, first.Scope, "fulfil", api.OperationSubject{Type: "order", ID: "ord_1"}, 1); err != nil || len(ids) != 1 {
			t.Fatalf("subject target limit %v %v", ids, err)
		}
		if _, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, second.ID, 1); err != nil {
			t.Fatal(err)
		}
		ids, err = signals.OperationsForSubject(ctx, f.account.ID, f.app.ID, first.Scope, "fulfil", api.OperationSubject{Type: "order", ID: "ord_1"}, 10)
		if err != nil || len(ids) != 1 || ids[0] != first.ID {
			t.Fatalf("settled operation still targeted: %v %v", ids, err)
		}
	})
}

func countOf(items []string, want string) int {
	n := 0
	for _, item := range items {
		if item == want {
			n++
		}
	}
	return n
}
