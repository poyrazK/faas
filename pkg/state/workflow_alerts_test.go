package state

import (
	"context"
	"testing"
	"time"
)

func TestWorkflowAlertSignalsRespectOwnershipWindowAndTerminalState(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		app, _ := seedWorkflowSchedule(t, store, "allow")
		create := func() *WorkflowRun {
			run := &WorkflowRun{AppID: app.ID, WorkflowName: "nightly", DefinitionSnapshot: []byte(`{"name":"nightly","steps":[{"name":"timer","wait_for_duration":"1h"}]}`)}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			return run
		}
		for _, status := range []string{WorkflowRunStatusFailed, WorkflowRunStatusDead} {
			run := create()
			if err := store.MarkWorkflowRunStatus(ctx, run.ID, status, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		cancelled := create()
		if _, err := store.CancelWorkflowRun(ctx, cancelled.ID, "operator"); err != nil {
			t.Fatal(err)
		}
		future := &WorkflowRun{AppID: app.ID, WorkflowName: "nightly", ScheduledFor: time.Now().UTC().Add(time.Hour), DefinitionSnapshot: []byte(`{"name":"nightly","steps":[{"name":"timer","wait_for_duration":"1h"}]}`)}
		if err := store.CreateWorkflowRun(ctx, future); err != nil {
			t.Fatal(err)
		}
		pending := create()
		waiting := create()
		if err := store.MarkWorkflowRunStatus(ctx, waiting.ID, WorkflowRunStatusRunning, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, waiting.ID, []*WorkflowStep{{StepName: "timer", Status: WorkflowStepStatusPending}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ParkWorkflowTimer(ctx, waiting.ID, "timer", time.Hour); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Add(15 * time.Minute)
		signals := store.(WorkflowAlertStore)
		snapshot, err := signals.WorkflowAlertSnapshot(ctx, app.AccountID, app.ID, now.Add(-time.Hour), now)
		if err != nil || snapshot.Failures != 2 || snapshot.PendingAgeSeconds < 899 || snapshot.WaitingAgeSeconds < 899 {
			t.Fatalf("snapshot=%+v err=%v", snapshot, err)
		}
		snapshot, err = signals.WorkflowAlertSnapshot(ctx, app.AccountID, app.ID, now, now)
		if err != nil || snapshot.Failures != 0 || snapshot.PendingAgeSeconds < 899 || snapshot.WaitingAgeSeconds < 899 {
			t.Fatalf("counts must honor window but ages reflect current state: %+v %v", snapshot, err)
		}
		snapshot, err = signals.WorkflowAlertSnapshot(ctx, "00000000-0000-4000-8000-000000000001", app.ID, now.Add(-time.Hour), now)
		if err != nil || snapshot != (WorkflowAlertSnapshot{}) {
			t.Fatalf("cross-account signals=%+v %v", snapshot, err)
		}
		if _, err := store.CancelWorkflowRun(ctx, waiting.ID, "operator"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, pending.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		snapshot, err = signals.WorkflowAlertSnapshot(ctx, app.AccountID, "", now.Add(-time.Hour), now)
		if err != nil || snapshot.Failures != 2 || snapshot.PendingAgeSeconds != 0 || snapshot.WaitingAgeSeconds != 0 {
			t.Fatalf("terminal runs included in ages: %+v %v", snapshot, err)
		}
	})
}
