package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowScheduleReplayBatchRespectsRemainingQuota(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		app, deployment := state.SeedWorkflowSchedule(t, store, "allow")
		at := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
		schedules := store.(state.WorkflowScheduleStore)
		if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
			t.Fatal(err)
		}
		limit := api.PlanHobby.WorkflowMaxConcurrentRuns()
		runs := make([]*state.WorkflowRun, limit)
		for i := range runs {
			runs[i] = &state.WorkflowRun{AppID: app.ID, WorkflowName: "filler", DefinitionSnapshot: json.RawMessage(`{"name":"filler","steps":[]}`)}
			if err := store.CreateWorkflowRun(ctx, runs[i]); err != nil {
				t.Fatal(err)
			}
		}
		for i := 1; i <= 2; i++ {
			cursor, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Duration(i)*time.Minute))
			if err != nil || cursor.Status != state.WorkflowScheduleSkippedQuota {
				t.Fatalf("skipped occurrence: %+v, %v", cursor, err)
			}
		}
		history := store.(state.WorkflowScheduleHistoryStore)
		rows, err := history.ListWorkflowScheduleOccurrences(ctx, app.ID, "", "", 10)
		if err != nil || len(rows) != 2 {
			t.Fatalf("occurrences: %+v, %v", rows, err)
		}
		if _, err := store.CancelWorkflowRun(ctx, runs[0].ID, "free one slot"); err != nil {
			t.Fatal(err)
		}
		results, err := store.(state.WorkflowScheduleReplayStore).ReplayWorkflowScheduleOccurrences(ctx, app.ID, []string{rows[0].ID, rows[1].ID})
		if err != nil || len(results) != 2 || results[0].Outcome != state.WorkflowScheduleReplayReplayed || results[1].Outcome != state.WorkflowScheduleReplayQuotaFull {
			t.Fatalf("replay results: %+v, %v", results, err)
		}
	})
}
