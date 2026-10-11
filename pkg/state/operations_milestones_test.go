package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowStateEvidenceMustReferenceSameTransactionMilestones(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro).Operations
	milestoneID := uuid.NewString()
	definition := OperationDefinition{OperationDefinitionResponse: api.OperationDefinitionResponse{Spec: api.OperationDefinitionSpec{
		Name: "fulfill-order", Method: "POST", Path: "/orders/fulfill", Owner: api.OperationOwnerPlatformTenant,
		InputSchema: []byte(`true`), OutputSchema: []byte(`true`), ProgressStages: []string{"complete"},
		HTTPTransactionVersion: api.OperationHTTPTransactionVersion,
		Milestones:             map[string]json.RawMessage{"paid": []byte(`true`)},
		WorkflowSteps: []api.OperationWorkflowSpec{{
			Workflow: "order-lifecycle", Title: "Order lifecycle", Version: 1, States: []string{"pending", "fulfilled"},
			TransitionsDeclared: true, Transitions: []api.OperationWorkflowTransition{{From: "pending", To: "fulfilled", RequiredMilestones: []string{"paid"}}},
			Step: "paid", Label: "Order paid", Milestone: "paid", InstanceIDFrom: "/workflow_run_id", Position: 1,
		}},
	}}}
	op := Operation{OperationResponse: api.OperationResponse{Subject: &api.OperationSubject{Type: "order", ID: "order-42"}}, PlanLimits: limits}
	report := api.OperationWorkflowStateReport{ID: uuid.NewString(), Workflow: "order-lifecycle", InstanceID: "run-1",
		FromState: "pending", State: "fulfilled", Revision: 1, OccurredAt: time.Now().UTC(),
		EvidenceMilestones: []api.OperationWorkflowEvidenceMilestone{{ID: milestoneID, Name: "paid"}}}
	milestone := api.OperationMilestoneRequest{ID: milestoneID, Name: "paid", Payload: []byte(`{"workflow_run_id":"run-1"}`), OccurredAt: time.Now().UTC()}

	if err := validateOperationWorkflowStateBatch(op, definition, []api.OperationWorkflowStateReport{report}, nil); err == nil {
		t.Fatal("evidence without its same-transaction milestone was accepted")
	}
	wrongFact := milestone
	wrongFact.Name = "not-paid"
	if err := validateOperationWorkflowStateBatch(op, definition, []api.OperationWorkflowStateReport{report}, []api.OperationMilestoneRequest{wrongFact}); err == nil {
		t.Fatal("evidence reference with a different milestone name was accepted")
	}
	if err := validateOperationWorkflowStateBatch(op, definition, []api.OperationWorkflowStateReport{report}, []api.OperationMilestoneRequest{milestone}); err != nil {
		t.Fatalf("same-transaction evidence rejected: %v", err)
	}
}
