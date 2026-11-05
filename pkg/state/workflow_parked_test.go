package state_test

// adr: 20261023215834

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Runs parked on an event stay off the concurrency cap up to the parked
// allowance; parked runs beyond it count as concurrent again.
func TestParkedWorkflowRunsStayOffTheConcurrencyCap(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		ctx := t.Context()
		const maxActive, maxParked = 1, 1
		admit := func() (*state.WorkflowRun, int, error) {
			run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "manual", DefinitionSnapshot: json.RawMessage(`{}`)}
			active, err := store.CreateWorkflowRunAdmitted(ctx, run, maxActive, maxParked)
			return run, active, err
		}
		park := func(run *state.WorkflowRun) {
			t.Helper()
			state.QueueHealthRunning(t, store, run)
			if err := store.ScheduleWorkflowRun(ctx, run.ID, state.WorkflowRunStatusAwaitingEvent, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		first, _, err := admit()
		if err != nil {
			t.Fatal(err)
		}
		if _, active, err := admit(); !errors.Is(err, state.ErrWorkflowRunQuotaExceeded) || active != 1 {
			t.Fatalf("second run beside a pending one: active=%d %v", active, err)
		}
		park(first)
		second, active, err := admit()
		if err != nil || active != 1 {
			t.Fatalf("run beside a parked one: active=%d %v", active, err)
		}
		park(second)
		if _, active, err := admit(); !errors.Is(err, state.ErrWorkflowRunQuotaExceeded) || active != 1 {
			t.Fatalf("parked beyond the allowance: active=%d %v", active, err)
		}
		if _, err := store.CreateWorkflowRunAdmitted(ctx, &state.WorkflowRun{AppID: app.ID, WorkflowName: "manual", DefinitionSnapshot: json.RawMessage(`{}`)}, 2, 0); !errors.Is(err, state.ErrWorkflowRunQuotaExceeded) {
			t.Fatalf("no allowance counts every parked run: %v", err)
		}
	})
}
