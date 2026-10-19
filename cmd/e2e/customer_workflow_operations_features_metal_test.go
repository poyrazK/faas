//go:build metal

// adr: 20261019001500
package e2e

// Native qualification of approvals with structured input
// (ADR-20261019001500): the customer decides a real waiting workflow
// Operation with form input, the input is validated and replays exactly, and
// the run resumes through the real scheduler.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomerWorkflowOperationApprovalInputMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	openOperationFeatures(t, f.h, f.account.ID)
	def := f.defineWorkflow(t, "export-approval-input", "wait-chain", []string{"collect", "review", "payment", "finish"}, func(spec *api.OperationDefinitionSpec) {
		spec.Approvals = []api.OperationApprovalSpec{{Step: "review", Approver: api.OperationApproverCustomer}}
		spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}
		spec.InputSchema = json.RawMessage(`{"type":"object","required":["mode","order_id"],"properties":{"mode":{"type":"string"},"order_id":{"type":"string"}},"additionalProperties":false}`)
	})
	form := api.OperationApprovalFormRequest{Operation: def.Spec.Name, Step: "review", Required: true,
		InputSchema: json.RawMessage(`{"type":"object","required":["amount_cents"],"properties":{"amount_cents":{"type":"integer","minimum":1},"reason":{"type":"string"}},"additionalProperties":false}`)}
	mustGatedJSON(t, f.owner, http.MethodPut, "/v1/apps/"+f.app.Slug+"/operation-approval-forms", form, &api.OperationApprovalForm{}, http.StatusCreated)

	id := f.startDefinition(t, def.ID, map[string]any{"mode": "complete", "order_id": "ord_input"}, "approval-input-1")
	f.waitWaiting(t, id, api.OperationWaitApproval, "review")
	formPath := fmt.Sprintf("/v1/platform-tenant-self/customer-operations/%s/approvals/review/form", id)
	var view api.OperationApprovalFormView
	mustGatedJSON(t, f.customer, http.MethodGet, formPath, nil, &view, http.StatusOK)
	if !view.Required || view.Approver != api.OperationApproverCustomer || view.Decision != nil {
		t.Fatalf("pending form %+v", view)
	}

	decide := func(input string) int {
		req := api.OperationApprovalInputRequest{ExpectedGeneration: 1, Decision: api.OperationApprovalApproved}
		if input != "" {
			req.Input = json.RawMessage(input)
		}
		return gatedJSON(t, f.customer, http.MethodPost, fmt.Sprintf("/v1/platform-tenant-self/customer-operations/%s/approvals/review", id), req, &api.OperationResponse{})
	}
	for input, want := range map[string]int{"": http.StatusBadRequest, `{"amount_cents":0}`: http.StatusBadRequest} {
		if got := decide(input); got != want {
			t.Fatalf("decision with input %q: HTTP %d, want %d", input, got, want)
		}
	}
	if got := decide(`{"amount_cents":1250,"reason":"damaged"}`); got != http.StatusOK {
		t.Fatalf("valid decision: HTTP %d", got)
	}
	if got := decide(`{"amount_cents":1250,"reason":"damaged"}`); got != http.StatusOK {
		t.Fatalf("repeated decision: HTTP %d", got)
	}
	if got := decide(`{"amount_cents":999}`); got != http.StatusConflict {
		t.Fatalf("changed input: HTTP %d, want conflict", got)
	}

	// The run left the approval: it now waits on the payment event.
	f.waitWaiting(t, id, api.OperationWaitEvent, "payment")
	mustGatedJSON(t, f.owner, http.MethodGet, fmt.Sprintf("/v1/apps/%s/operations/%s/approvals/review/form", f.app.Slug, id), nil, &view, http.StatusOK)
	var input struct {
		AmountCents int    `json:"amount_cents"`
		Reason      string `json:"reason"`
	}
	if view.Decision == nil || view.Decision.Decision != api.OperationApprovalApproved || json.Unmarshal(view.Decision.Input, &input) != nil || input.AmountCents != 1250 || input.Reason != "damaged" {
		t.Fatalf("recorded decision %+v", view.Decision)
	}
	_, err := f.customer.SignalPlatformTenantSelfOperationsBySubject(t.Context(), api.PlatformTenantSubjectSignalRequest{
		DefinitionID: def.ID, Subject: api.OperationSubject{Type: "order", ID: "ord_input"}, SignalID: "pay-input", Event: "payment.succeeded", Payload: json.RawMessage(`{"charge":"ch_input"}`)})
	mustCustomerWorkflow(t, err)
	f.waitState(t, id, api.OperationSucceeded)
}
