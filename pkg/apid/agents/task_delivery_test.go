package agents_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTaskArtifactDeliveryRequiresLiveOriginalThreadGrant(t *testing.T) {
	f, _ := newToolFixture(t)
	f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread"})
	feed := &taskFeed{link: state.ExecutionTaskInvocation{AccountID: callbackAccount, ExecutionID: "run", Callback: &state.ExecutionTaskCallback{EndpointID: "ep-1", ThreadID: "thread", ToolCallID: "call", IssuedAt: f.h.Now()}}, row: state.Execution{ID: "run", Status: api.ExecutionStatusSucceeded}}
	f.h.TaskCallbacks, f.h.TaskExecutions = feed, feed
	caller := keyCaller(callbackAccount)
	if _, p := f.h.TaskDeliveryReceipt(t.Context(), caller, "run"); p != nil {
		t.Fatal(p)
	}
	if status := f.revoke(t, callbackAccount, "ep-1", "thread"); status != 200 {
		t.Fatal(status)
	}
	if _, p := f.h.TaskDeliveryReceipt(t.Context(), caller, "run"); p == nil || p.Status != 404 {
		t.Fatal("closed thread could promote a new output", p)
	}
}
