package state_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"testing"
	"time"
)

func TestWorkflowRecoveryAttentionClassification(t *testing.T) {
	admitted := time.Now().UTC()
	for _, tc := range []struct {
		name, wait, state, source, want string
		age, progress, deadline         int
	}{
		{name: "stall boundary", state: "queued", age: 60, want: "stalled"},
		{name: "fresh", state: "running", age: 59, want: "running"},
		{name: "progress", state: "running", age: 100, progress: 50, want: "running"},
		{name: "capacity", state: "queued", wait: api.AutomationQueueAppCapacity, age: 100, want: "capacity_wait"},
		{name: "scheduled", state: "queued", wait: api.AutomationQueueScheduled, age: 100, want: "scheduled_wait"},
		{name: "deadline while scheduled", state: "queued", wait: api.AutomationQueueScheduled, age: 120, deadline: 120, want: "deadline_exceeded"},
		{name: "missing generation", state: "unknown", age: 120, deadline: 120, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := state.NewWorkflowRecoveryAttention(&api.WorkflowRunRecoveryPlan{ExecutionStallSeconds: 60, ExecutionDeadlineSeconds: tc.deadline}, admitted.Add(time.Duration(tc.age)*time.Second))
			state.AddWorkflowRecoveryAttention(&out, 1, "run", 1, api.EventRecoveryExecution{State: tc.state, Source: tc.source}, &admitted, admitted.Add(time.Duration(tc.progress)*time.Second), tc.wait)
			if len(out.Targets) != 1 || out.Targets[0].Status != tc.want {
				t.Fatalf("report=%+v", out)
			}
			if out.NeedsAttention != (tc.want == "stalled" || tc.want == "deadline_exceeded") {
				t.Fatalf("attention=%+v", out)
			}
		})
	}
	out := state.NewWorkflowRecoveryAttention(&api.WorkflowRunRecoveryPlan{}, admitted.Add(time.Hour))
	state.AddWorkflowRecoveryAttention(&out, 1, "run", 1, api.EventRecoveryExecution{State: "succeeded", Source: "recovery_result"}, &admitted, admitted, "")
	if len(out.Targets) != 0 || out.NeedsAttention {
		t.Fatalf("saved result=%+v", out)
	}
}

func TestWorkflowRecoveryAttentionCapture(t *testing.T) {
	for _, deadline := range []int{0, 60} {
		t.Run(time.Duration(deadline).String(), func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
				state.FailResumeRun(t, store, run, "send")
				hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://attention.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryExecutionAttention)}})
				if err != nil {
					t.Fatal(err)
				}
				recovery := store.(state.EventRecoveryStore)
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, ExecutionDeadlineSeconds: deadline, ExecutionStallSeconds: 60, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
					t.Fatalf("admit=%v %v", did, err)
				}
				monitor := store.(state.WorkflowRecoveryExecutionAttentionStore)
				now := time.Now().UTC().Add(61 * time.Second)
				if did, err := monitor.ProcessNextWorkflowRecoveryExecutionAttention(ctx, now); err != nil || !did {
					t.Fatalf("monitor=%v %v", did, err)
				}
				if pg, ok := store.(*state.PgStore); ok {
					monitor = state.NewPgStore((*pg.Pool()).Pool)
				}
				if did, err := monitor.ProcessNextWorkflowRecoveryExecutionAttention(ctx, now.Add(time.Hour)); err != nil || did {
					t.Fatalf("duplicate=%v %v", did, err)
				}
				if _, err := store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 100, "")
				if err != nil || len(deliveries) != 1 {
					t.Fatalf("deliveries=%+v %v", deliveries, err)
				}
				var payload api.WorkflowRecoveryExecutionAttentionWebhookPayload
				if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.JobID != job.ID || payload.EventID != state.RecoveryNotificationEventID(job.ID, string(state.AppWebhookEventRecoveryExecutionAttention)) || !payload.Attention.NeedsAttention || payload.Attention.Targets[0].DiagnosticsURL != "/v1/workflows/runs/"+run.ID+"/diagnostics" {
					t.Fatalf("payload=%+v", payload)
				}
				report, err := store.(state.EventRecoveryNotificationsStore).GetEventRecoveryNotifications(ctx, app.AccountID, job.ID, now)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, n := range report.Notifications {
					if n.Kind == "attention" {
						found = true
						if n.Event != "event_recovery.execution_attention" || n.CaptureStatus != "captured" {
							t.Fatalf("notification=%+v", n)
						}
					}
				}
				if !found {
					t.Fatalf("notifications=%+v", report)
				}
				var attention api.WorkflowRecoveryExecutionAttention
				if mem, ok := store.(*state.MemStore); ok {
					(*mem.Mu()).Lock()
					attention, err = mem.WorkflowRecoveryAttentionLocked(ctx, (*mem.EventRecoveryJobs())[state.CanonicalMemUUID(job.ID)], now)
					(*mem.Mu()).Unlock()
				} else {
					pg := store.(*state.PgStore)
					attention, err = state.WorkflowRecoveryAttentionRead(ctx, sqlc.New(), (*pg.Pool()), app.AccountID, job, now)
				}
				if err != nil || !attention.NotificationCaptured || !attention.NeedsAttention || len(attention.Targets) != 1 {
					t.Fatalf("attention=%+v %v", attention, err)
				}
				if deadline == 0 && attention.StalledCount != 1 || deadline > 0 && attention.DeadlineExceededCount != 1 {
					t.Fatalf("classification=%+v", attention)
				}
				var h *api.EventRecoveryExecutionHealth
				if mem, ok := store.(*state.MemStore); ok {
					(*mem.Mu()).Lock()
					h, err = mem.RecoveryExecutionHealthLocked(ctx, app.AccountID, app.ID, now)
					(*mem.Mu()).Unlock()
				} else {
					pg := store.(*state.PgStore)
					h, err = state.ObserveRecoveryExecutionHealth(ctx, sqlc.New(), (*pg.Pool()), app.AccountID, app.ID, now)
				}
				if err != nil || len(h.Jobs) != 1 || h.Jobs[0].Attention == nil || h.StalledJobs+h.DeadlineExceededJobs != 1 {
					t.Fatalf("health=%+v %v", h, err)
				}
				live, err := store.GetWorkflowRun(ctx, run.ID)
				if err != nil || live.ResumeCount != 1 || live.Status != state.WorkflowRunStatusPending {
					t.Fatalf("monitor mutated run=%+v %v", live, err)
				}
				if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
					t.Fatal(err)
				}
				if did, err := store.(state.EventRecoveryExecutionNotificationStore).ProcessNextEventRecoveryExecutionNotification(ctx, now.Add(time.Minute)); err != nil || !did {
					t.Fatalf("completion after attention=%v %v", did, err)
				}
				completed, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
				if err != nil || completed.ExecutionFinishedAt == nil || completed.Execution.SavedResults != 1 {
					t.Fatalf("completed=%+v %v", completed, err)
				}

			})
		})
	}
}

func TestWorkflowRecoveryAttentionDuringAdmission(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
		state.FailResumeRun(t, store, run, "send")
		other := &state.WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot}
		if err := store.CreateWorkflowRun(ctx, other); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, other.ID, []*state.WorkflowStep{{StepName: "send", Input: other.Input}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		state.FailResumeRun(t, store, other, "send")
		recovery := store.(state.EventRecoveryStore)
		job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), RatePerSecond: 1, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, ExecutionStallSeconds: 60, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}, {RunID: other.ID}}}})
		if err != nil {
			t.Fatal(err)
		}
		if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
			t.Fatalf("admit=%v %v", did, err)
		}
		for _, paused := range []bool{false, true} {
			if paused {
				if _, err := recovery.PauseEventRecovery(ctx, app.AccountID, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC().Add(61 * time.Second)
			var h *api.EventRecoveryExecutionHealth
			if mem, ok := store.(*state.MemStore); ok {
				(*mem.Mu()).Lock()
				h, err = mem.RecoveryExecutionHealthLocked(ctx, app.AccountID, app.ID, now)
				(*mem.Mu()).Unlock()
			} else {
				pg := store.(*state.PgStore)
				h, err = state.ObserveRecoveryExecutionHealth(ctx, sqlc.New(), (*pg.Pool()), app.AccountID, app.ID, now)
			}
			if err != nil || h.StalledJobs != 1 || len(h.Jobs) != 1 || h.Jobs[0].Attention == nil || len(h.Jobs[0].Attention.Targets) != 1 {
				t.Fatalf("paused=%v health=%+v %v", paused, h, err)
			}
		}
		if did, err := store.(state.WorkflowRecoveryExecutionAttentionStore).ProcessNextWorkflowRecoveryExecutionAttention(ctx, time.Now().UTC().Add(61*time.Second)); err != nil || !did {
			t.Fatalf("paused monitoring=%v %v", did, err)
		}
	})
}
