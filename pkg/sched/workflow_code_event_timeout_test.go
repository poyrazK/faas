// adr: 20261021204307
package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A wait that timed out received no event, so it must not use up an
// occurrence: the next wait for the same event takes the first event sent.
func TestCodeWorkflowEventAfterTimedOutWait(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		executor := &codeTestExecutor{run: func(wf *codeTestWF, _ int) (any, error) {
			_, firstTimedOut := wf.WaitForEvent("approval", "approved", time.Second)
			second, secondTimedOut := wf.WaitForEvent("approval#2", "approved", time.Hour)
			return map[string]any{"first_timed_out": firstTimedOut, "second": second, "second_timed_out": secondTimedOut}, nil
		}}
		run := sched.CreateGuardedRun(t, store, codeSpec(nil), `{}`)
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		dispatchCode(t, orchestrator)
		time.Sleep(1100 * time.Millisecond)
		dispatchCode(t, orchestrator)
		if statuses := codeStepStatuses(t, store, run.ID); statuses["approval"] != state.WorkflowStepStatusSucceeded || statuses["approval#2"] != state.WorkflowStepStatusAwaitingEvent {
			t.Fatalf("first wait must time out and the second must park: %v", statuses)
		}
		if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "approved", Payload: json.RawMessage(`{"ok":true}`)}); err != nil {
			t.Fatal(err)
		}
		dispatchCode(t, orchestrator)
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || !sched.WorkflowJSONEqual(final.Output, []byte(`{"first_timed_out":true,"second":{"ok":true},"second_timed_out":false}`)) {
			t.Fatalf("the event sent during the second wait was not delivered: status=%s output=%s steps=%v", final.Status, final.Output, codeStepStatuses(t, store, run.ID))
		}
	})
}
