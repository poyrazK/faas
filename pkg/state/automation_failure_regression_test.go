package state

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFailurePolicyExcludesOperationRunsAfterOperationExpires(t *testing.T) {
	m := NewMemStore()
	now := time.Now().UTC()
	op := Operation{}
	op.ID, op.Generation, op.CreatedAt = "op", 1, now
	for _, id := range []string{"first", "second"} {
		run := WorkflowRun{ID: id, AppID: "app", WorkflowName: "hooked", Status: WorkflowRunStatusFailed, FinishedAt: &now}
		m.insertOperationWorkflowLocked(op, run, nil)
		op.Generation++
	}
	m.forgetOperationLocked(op.ID)
	ordinary := WorkflowRun{ID: "ordinary", AppID: "app", WorkflowName: "hooked", Status: WorkflowRunStatusFailed, FinishedAt: &now}
	m.workflowRuns[ordinary.ID] = ordinary
	out := m.failurePolicyAdmissionStateLocked("app", "hooked", now)
	if out.ObservedFailures != 1 || out.ObservedCompletedRuns != 1 {
		t.Fatalf("operation runs included: %+v", out)
	}
}

func TestFailurePolicyRetainedCountExcludesTerminalRecipients(t *testing.T) {
	m := NewMemStore()
	workflow := json.RawMessage(`{"name":"paid"}`)
	recipients := []PublishedEventRecipient{
		{ID: "pending", AppID: "app", Workflow: workflow}, {ID: "filtered", AppID: "app", Workflow: workflow},
		{ID: "failed", AppID: "app", Workflow: workflow}, {ID: "enqueued", AppID: "app", Workflow: workflow},
	}
	work := &PublishedEventWork{ID: 1, RecipientSnapshot: recipients, RecipientProgress: map[string]PublishedEventRecipientProgress{
		"filtered": {State: "filtered"}, "failed": {State: "failed"}, "enqueued": {State: "enqueued"},
	}}
	m.eventFanout = map[string]*PublishedEventWork{"legacy": work}
	if out := m.failurePolicyResponseLocked("app", "paid", time.Now()); out.RetainedEvents != 1 {
		t.Fatalf("legacy retained count: %+v", out)
	}
	work.RecipientClaims = true
	work.routingRecipients = map[string]*PublishedEventRecipientWork{
		"pending": {State: "processing"}, "filtered": {State: "filtered"}, "failed": {State: "failed"}, "enqueued": {State: "enqueued"},
	}
	if out := m.failurePolicyResponseLocked("app", "paid", time.Now()); out.RetainedEvents != 1 {
		t.Fatalf("claimed retained count: %+v", out)
	}
}
