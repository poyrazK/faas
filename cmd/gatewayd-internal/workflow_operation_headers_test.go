package main

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// A forged Operation header in a persisted envelope is cleared with the other
// guest identity headers; only the ledger-resolved step identity reaches the
// guest, and an unlinked step gets none.
func TestWorkflowOperationHeadersComeFromTheResolvedStep(t *testing.T) {
	h := http.Header{}
	h.Set(api.OperationIDHeader, "forged")
	h.Set(api.OperationWorkflowCapabilityHeader, "forged")
	gateway.Target{}.PlatformIdentity("", "workflow-1").ApplyGuestHeaders(h)
	applyWorkflowOperationHeaders(h, nil)
	if h.Get(api.OperationIDHeader) != "" || h.Get(api.OperationWorkflowCapabilityHeader) != "" {
		t.Fatalf("unlinked step kept forged Operation headers: %v", h)
	}
	step := &state.WorkflowOperationStep{OperationID: "op-1", RunID: "run-1", StepName: "collect", Generation: 2, Attempt: 3, Capability: "cap-1"}
	applyWorkflowOperationHeaders(h, step)
	want := map[string]string{api.OperationIDHeader: "op-1", api.OperationExecutionKindHeader: "workflow", api.OperationWorkflowRunHeader: "run-1",
		api.OperationWorkflowStepHeader: "collect", api.OperationGenerationHeader: "2", api.OperationAttemptHeader: "3", api.OperationWorkflowCapabilityHeader: "cap-1"}
	for name, value := range want {
		if h.Get(name) != value {
			t.Fatalf("%s = %q, want %q", name, h.Get(name), value)
		}
	}
}
