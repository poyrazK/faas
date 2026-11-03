package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEventsOutlivePrunedRunUntilTheirOwnRetention(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "retention", DefinitionSnapshot: json.RawMessage(`{"name":"retention","steps":[]}`)}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		for _, event := range []*state.WorkflowEvent{
			{RunID: run.ID, EventName: "first", Payload: json.RawMessage(`{}`)},
			{RunID: run.ID, EventName: "second", Payload: json.RawMessage(`{}`)},
		} {
			if err := store.InsertWorkflowEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
			t.Fatalf("prune run: %d, %v", n, err)
		}
		events, err := store.GetWorkflowEventsForRun(ctx, run.ID)
		if err != nil || len(events) != 2 {
			t.Fatalf("events after run cleanup: %+v, %v", events, err)
		}
		if n, err := store.SweepExpiredWorkflowEvents(ctx, 30*24*time.Hour); err != nil || n != 0 {
			t.Fatalf("recent events pruned early: %d, %v", n, err)
		}
		if n, err := store.SweepExpiredWorkflowEvents(ctx, 0); err != nil || n != 2 {
			t.Fatalf("prune expired events: %d, %v", n, err)
		}
	})
}
