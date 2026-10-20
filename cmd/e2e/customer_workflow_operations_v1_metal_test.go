//go:build metal

// adr: 20261017102231
package e2e

// ADR-20261017102231 Stage 2 for workflow Operations: waits, approvals and signals
// (ADR-1194, ADR-1197, ADR-20261016100929, ADR-20261017100853), an event-wait timeout and the
// workflow execution budget (ADR-20261016110803), through the real daemons and guest.
// The base lane already covers results, recovery, cancellation, the step
// deadline and revocation.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const customerWorkflowProviderSecret = "whsec_native_customer_workflow"

// defineWorkflow registers another definition on the fixture's deployment,
// sharing the export output contract and completion webhook.
func (f *customerWorkflowFixture) defineWorkflow(t *testing.T, name, workflow string, stages []string, configure func(*api.OperationDefinitionSpec)) api.OperationDefinitionResponse {
	t.Helper()
	spec := f.definition.Spec
	spec.Name, spec.Workflow, spec.Path, spec.ProgressStages = name, workflow, "/exports/"+name, stages
	spec.Approvals, spec.Subject, spec.Limits = nil, nil, nil
	if configure != nil {
		configure(&spec)
	}
	def, err := f.owner.PutOperationDefinition(t.Context(), f.app.Slug, f.deployment.ID, name, spec)
	mustCustomerWorkflow(t, err)
	return def
}

func (f *customerWorkflowFixture) startDefinition(t *testing.T, definition string, input map[string]any, key string) string {
	t.Helper()
	raw, _ := json.Marshal(input)
	accepted, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: definition, Input: raw}, key)
	mustCustomerWorkflow(t, err)
	f.mu.Lock()
	f.operationIDs[accepted.ID] = true
	f.mu.Unlock()
	return accepted.ID
}

func (f *customerWorkflowFixture) waitWaiting(t *testing.T, id, kind, step string) api.OperationResponse {
	t.Helper()
	var op api.OperationResponse
	customerWorkflowEventually(t, 3*time.Minute, "native workflow operation waiting on "+step, func() bool {
		var err error
		op, err = f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
		return err == nil && op.State == api.OperationWaiting && op.Waiting != nil && op.Waiting.Kind == kind && op.Waiting.Step == step
	})
	return op
}

// signalSources lists the source of each signal_received event in order.
func (f *customerWorkflowFixture) signalSources(t *testing.T, id string) []string {
	t.Helper()
	page, err := f.customer.GetPlatformTenantSelfOperationEvents(t.Context(), id, 0)
	mustCustomerWorkflow(t, err)
	sources := []string{}
	for _, event := range page.Events {
		if event.Type != "signal_received" {
			continue
		}
		var data struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(event.Data, &data)
		sources = append(sources, data.Source)
	}
	return sources
}

func (f *customerWorkflowFixture) approve(t *testing.T, id string) {
	t.Helper()
	decision := api.OperationApprovalRequest{ExpectedGeneration: 1, Decision: api.OperationApprovalApproved, Comment: "native approval"}
	_, err := f.owner.DecideOperationApproval(t.Context(), f.app.Slug, id, "review", decision)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusForbidden {
		t.Fatalf("the account decided a customer approval: %v", err)
	}
	_, err = f.customer.DecidePlatformTenantSelfOperationApproval(t.Context(), id, "review", decision)
	mustCustomerWorkflow(t, err)
}

// One fixture boot covers both ways a waiting Operation resumes: a provider
// webhook through a signal-mode binding, and the customer signalling by subject.
func TestCustomerWorkflowOperationWaitsAndSignalsMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	def := f.defineWorkflow(t, "export-waits", "wait-chain", []string{"collect", "review", "payment", "finish"}, func(spec *api.OperationDefinitionSpec) {
		spec.Approvals = []api.OperationApprovalSpec{{Step: "review", Approver: api.OperationApproverCustomer}}
		spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}
		spec.InputSchema = json.RawMessage(`{"type":"object","required":["mode","order_id"],"properties":{"mode":{"type":"string"},"order_id":{"type":"string"}},"additionalProperties":false}`)
	})
	endpoint, err := f.owner.CreateInboundWebhookEndpoint(t.Context(), f.app.Slug, api.CreateInboundWebhookEndpointRequest{
		Name: "native-payments", Provider: "stripe", SigningSecret: customerWorkflowProviderSecret, DeliveryPath: "/internal/stripe"})
	mustCustomerWorkflow(t, err)
	zero := int64(0)
	_, err = f.owner.PutOperationWebhookBinding(t.Context(), f.app.Slug, endpoint.ID, api.PutOperationWebhookBindingRequest{
		ExpectedVersion: &zero, Operation: def.Spec.Name, PlatformTenantID: f.tenantID, TakeOverDelivery: true, Mode: api.OperationWebhookModeSignal,
		Subject: &api.OperationSubjectSpec{Type: "order", IDFrom: "/data/object/metadata/order_id"},
		Signals: []api.OperationWebhookSignal{{ProviderEvent: "payment_intent.succeeded", Event: "payment.succeeded"}}})
	mustCustomerWorkflow(t, err)

	// A: approved by the customer, resumed by the provider's webhook.
	a := f.startDefinition(t, def.ID, map[string]any{"mode": "complete", "order_id": "ord_a"}, "waits-a")
	f.waitWaiting(t, a, api.OperationWaitApproval, "review")
	f.approve(t, a)
	f.waitWaiting(t, a, api.OperationWaitEvent, "payment")
	paid := []byte(`{"id":"evt_native_pay_a","type":"payment_intent.succeeded","data":{"object":{"metadata":{"order_id":"ord_a"}}}}`)
	if r := postNativeProviderEvent(t, f.h, endpoint.EndpointURL, customerWorkflowProviderSecret, paid); r.Status != api.InboundWebhookSignaled || r.Duplicate {
		t.Fatalf("provider payment %+v", r)
	}
	if r := postNativeProviderEvent(t, f.h, endpoint.EndpointURL, customerWorkflowProviderSecret, paid); r.Status != api.InboundWebhookSignaled || !r.Duplicate {
		t.Fatalf("provider retry %+v", r)
	}
	unknown := []byte(`{"id":"evt_native_pay_x","type":"payment_intent.succeeded","data":{"object":{"metadata":{"order_id":"ord_x"}}}}`)
	if r := postNativeProviderEvent(t, f.h, endpoint.EndpointURL, customerWorkflowProviderSecret, unknown); r.Status != api.InboundWebhookUnmatched {
		t.Fatalf("unknown subject %+v", r)
	}
	f.waitState(t, a, api.OperationSucceeded)
	if sources := f.signalSources(t, a); len(sources) != 1 || sources[0] != api.OperationSignalSourceWebhook {
		t.Fatalf("A's signal sources %v", sources)
	}

	// B: the customer signals by subject with its own token.
	b := f.startDefinition(t, def.ID, map[string]any{"mode": "complete", "order_id": "ord_b"}, "waits-b")
	f.waitWaiting(t, b, api.OperationWaitApproval, "review")
	f.approve(t, b)
	f.waitWaiting(t, b, api.OperationWaitEvent, "payment")
	reached, err := f.customer.SignalPlatformTenantSelfOperationsBySubject(t.Context(), api.PlatformTenantSubjectSignalRequest{
		DefinitionID: def.ID, Subject: api.OperationSubject{Type: "order", ID: "ord_b"}, SignalID: "pay-b", Event: "payment.succeeded", Payload: json.RawMessage(`{"charge":"ch_b"}`)})
	mustCustomerWorkflow(t, err)
	if len(reached.Delivered) != 1 || reached.Delivered[0].OperationID != b || !reached.Delivered[0].Waiting {
		t.Fatalf("customer subject signal %+v", reached)
	}
	f.waitState(t, b, api.OperationSucceeded)
	if sources := f.signalSources(t, b); len(sources) != 1 || sources[0] != api.OperationSignalSourceCustomer {
		t.Fatalf("B's signal sources %v", sources)
	}
}

// An event wait that nobody signals settles failed with event_timeout, and the
// step after it never runs.
func TestCustomerWorkflowOperationEventTimeoutMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	def := f.defineWorkflow(t, "export-timeout", "timeout-chain", []string{"collect", "payment", "finish"}, nil)
	id := f.startDefinition(t, def.ID, map[string]any{"mode": "complete"}, "timeout-1")
	f.waitWaiting(t, id, api.OperationWaitEvent, "payment")
	op := f.waitState(t, id, api.OperationFailed)
	if op.FailureCode != api.OperationFailureEventExpiry {
		t.Fatalf("expired event wait failure code %q", op.FailureCode)
	}
	stored, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	attempts, err := f.store.GetWorkflowStepAttempts(t.Context(), stored.WorkflowRunID, "finish")
	mustCustomerWorkflow(t, err)
	if len(attempts) != 0 {
		t.Fatalf("finish ran %d times after the wait expired", len(attempts))
	}
}

// ADR-20261016110803: the execution budget cuts the final step well before its two-minute
// step timeout. The cut attempt may have had an effect, so the Operation needs
// reconciliation and a safe_to_retry recovery is refused.
func TestCustomerWorkflowOperationBudgetCutMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	const budget = 10 * time.Second
	def := f.defineWorkflow(t, "export-budget", f.workflow.Name, []string{"collect", "transform", "finish"}, func(spec *api.OperationDefinitionSpec) {
		spec.Limits = &api.OperationLimits{MaxExecutionSeconds: int(budget / time.Second)}
	})
	id := f.startDefinition(t, def.ID, map[string]any{"mode": "hold"}, "budget-1")
	privateID := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	control, err := api.NewClient(f.h.APIDURL, old.token).GetWorkflowOperationExecutionControl(t.Context(), id, old.proof)
	mustCustomerWorkflow(t, err)
	if remaining := control.DeadlineAt.Sub(control.ObservedAt); remaining > budget {
		t.Fatalf("the final step's deadline is %s away, beyond the %s budget", remaining, budget)
	}
	op := f.waitState(t, id, api.OperationRequiresReconciliation)
	if op.Usage == nil || op.Usage.ExecutionMS < budget.Milliseconds()-100 || op.Usage.Attempts != 3 {
		t.Fatalf("usage %+v after the budget cut", op.Usage)
	}
	f.assertUnpublished(t, id, privateID)
	f.assertStale(t, id, old)
	retry := api.OperationRecoveryRequest{RecoveryID: "retry-after-budget", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "native budget cut"}
	_, err = f.owner.RecoverOperation(t.Context(), f.app.Slug, id, retry)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict {
		t.Fatalf("recovery after the budget was spent: %v", err)
	}
}
