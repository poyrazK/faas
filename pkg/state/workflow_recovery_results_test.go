package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestWorkflowRecoveryRetainedResultsAndNotifications(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed", "cancelled", "unknown", "skipped"} {
		t.Run(outcome, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
				state.FailResumeRun(t, store, run, "send")
				hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://recovery.example.test/finished", SecretSealed: []byte("test-sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryExecutionFinished)}})
				if err != nil {
					t.Fatal(err)
				}
				recovery := store.(state.EventRecoveryStore)
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "skipped" {
					if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.ResumeOptions(run, app, 0)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				notices := store.(state.EventRecoveryExecutionNotificationStore)
				poll := time.Now().UTC()
				if did, err := notices.ProcessNextEventRecoveryExecutionNotification(ctx, poll); err != nil || !did {
					t.Fatalf("queued poll=%v %v", did, err)
				}
				before, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
				if err != nil || before.ExecutionFinishedAt != nil {
					t.Fatalf("premature completion=%+v %v", before, err)
				}
				health := func() *api.EventRecoveryExecutionHealth {
					t.Helper()
					var out *api.EventRecoveryExecutionHealth
					var err error
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						out, err = mem.RecoveryExecutionHealthLocked(ctx, app.AccountID, app.ID, time.Now().UTC())
						(*mem.Mu()).Unlock()
					} else {
						pg := store.(*state.PgStore)
						out, err = state.ObserveRecoveryExecutionHealth(ctx, sqlc.New(), (*pg.Pool()), app.AccountID, app.ID, time.Now().UTC())
					}
					if err != nil {
						t.Fatal(err)
					}
					return out
				}
				if outcome == "skipped" {
					if before.QueuedCount != 0 || before.SkippedCount != 1 || health().WaitingJobs != 0 {
						t.Fatalf("skipped=%+v", before)
					}
					if _, err = notices.ProcessNextEventRecoveryExecutionNotification(ctx, poll.Add(time.Second)); err != nil {
						t.Fatal(err)
					}
					if _, err = store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
						t.Fatal(err)
					}
					rows, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 100, "")
					if err != nil || len(rows) != 0 {
						t.Fatalf("zero-admission notices=%+v %v", rows, err)
					}
					return
				}
				if h := health(); h.WaitingJobs != 1 || len(h.Jobs) != 1 || h.Jobs[0].Execution.Queued != 1 {
					t.Fatalf("queued health=%+v", h)
				}
				if outcome == "cancelled" {
					if _, err = store.CancelWorkflowRun(ctx, run.ID, "operator stop"); err != nil {
						t.Fatal(err)
					}
				} else if outcome != "unknown" {
					if err = store.MarkWorkflowRunStatus(ctx, run.ID, outcome, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				// Prune the workflow independently of the owning recovery job.
				if mem, ok := store.(*state.MemStore); ok {
					(*mem.Mu()).Lock()
					delete((*mem.WorkflowRuns()), run.ID)
					delete((*mem.WorkflowResumes()), run.ID)
					(*mem.Mu()).Unlock()
				} else {
					pg := store.(*state.PgStore)
					if _, err = (*pg.Pool()).Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); err != nil {
						t.Fatal(err)
					}
					// New store instance proves capture survives a server restart.
					fresh := state.NewPgStore((*pg.Pool()).Pool)
					recovery = fresh
					notices = fresh
				}
				now := poll.Add(api.EventRecoveryExecutionNotificationPollInterval + time.Second)
				if _, err = notices.ProcessNextEventRecoveryExecutionNotification(ctx, now); err != nil {
					t.Fatal(err)
				}
				if _, err = notices.ProcessNextEventRecoveryExecutionNotification(ctx, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err = store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 100, "")
				if err != nil {
					t.Fatal(err)
				}
				got, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "unknown" {
					if len(deliveries) != 0 || got.ExecutionFinishedAt != nil || got.Execution.Unknown != 1 || got.Execution.SavedResults != 0 {
						t.Fatalf("unknown completed=%+v deliveries=%d", got, len(deliveries))
					}
					if h := health(); h.UnknownJobs != 1 {
						t.Fatalf("unknown health=%+v", h)
					}
					return
				}
				if len(deliveries) != 1 || got.ExecutionFinishedAt == nil || got.Execution.SavedResults != 1 {
					t.Fatalf("retained=%+v deliveries=%d", got, len(deliveries))
				}
				if h := health(); h.WaitingJobs != 0 {
					t.Fatalf("finished health=%+v", h)
				}
				report, err := store.(state.EventRecoveryNotificationsStore).GetEventRecoveryNotifications(ctx, app.AccountID, job.ID, now)
				if err != nil {
					t.Fatal(err)
				}
				for _, notice := range report.Notifications {
					if notice.Kind == "execution" && notice.CaptureStatus != "captured" {
						t.Fatalf("execution notification=%+v", notice)
					}
				}
				var payload api.EventRecoveryExecutionFinishedWebhookPayload
				if err = json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
					t.Fatal(err)
				}
				want := "finished_with_non_success"
				if outcome == "succeeded" {
					want = "all_succeeded"
				}
				if payload.Mode != "workflow" || payload.Outcome != want || payload.Execution.SavedResults != 1 || payload.UnresolvedCount != 0 || payload.ItemsURL != "/v1/event-recoveries/"+job.ID+"/items" {
					t.Fatalf("payload=%+v", payload)
				}
				items, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 1)
				if err != nil || len(items.Items) != 1 || items.Items[0].Execution.State != outcome || items.Items[0].Execution.EvidenceSource != "workflow_run" {
					t.Fatalf("items=%+v %v", items, err)
				}
			})
		})
	}
}
