package api

import (
	"github.com/google/uuid"
	"testing"
)

func TestWorkflowRecoveryRequestValidation(t *testing.T) {
	valid := func() EventRecoveryRequest {
		return EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRouting: &EventWorkflowRecoveryPlan{Name: "paid", PublishedVersion: 1, Source: "billing", Events: []EventWorkflowRecoveryTarget{{EventID: "invoice", SubscriptionID: "recipient"}}}}
	}
	for _, test := range []struct {
		name   string
		change func(*EventRecoveryRequest)
		ok     bool
	}{
		{"legacy", func(r *EventRecoveryRequest) {}, true},
		{"generation zero", func(r *EventRecoveryRequest) { g := int64(0); r.WorkflowRouting.Events[0].Generation = &g }, true},
		{"missing key", func(r *EventRecoveryRequest) { r.RequestID = "" }, false},
		{"bad key", func(r *EventRecoveryRequest) { r.RequestID = "bad" }, false},
		{"parent", func(r *EventRecoveryRequest) { r.ParentJobID = uuid.NewString() }, false},
		{"execution", func(r *EventRecoveryRequest) { r.Mode = "execution" }, false},
		{"filter", func(r *EventRecoveryRequest) { r.EventSource = "billing" }, false},
		{"duplicate", func(r *EventRecoveryRequest) {
			r.WorkflowRouting.Events = append(r.WorkflowRouting.Events, r.WorkflowRouting.Events[0])
		}, false},
		{"negative count", func(r *EventRecoveryRequest) { r.WorkflowRouting.Events[0].ReplayCount = -1 }, false},
		{"negative generation", func(r *EventRecoveryRequest) { g := int64(-1); r.WorkflowRouting.Events[0].Generation = &g }, false},
		{"too many", func(r *EventRecoveryRequest) {
			r.WorkflowRouting.Events = make([]EventWorkflowRecoveryTarget, AutomationRecoveryBatchMaxRuns+1)
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := valid()
			test.change(&r)
			if err := r.Validate(); (err == nil) != test.ok {
				t.Fatalf("valid=%v err=%v", test.ok, err)
			}
		})
	}
}
