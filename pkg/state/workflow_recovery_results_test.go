package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestWorkflowRecoveryRetainedResultsAndNotifications(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed", "cancelled", "unknown", "skipped"} {
		t.Run(outcome, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				ctx := context.Background()
				run, app := seedResumeRun(t, store, simpleResumeSpec())
				failResumeRun(t, store, run, "send")
				hook, err := store.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://recovery.example.test/finished", SecretSealed: []byte("test-sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryExecutionFinished)}})
				if err != nil {
					t.Fatal(err)
				}
				recovery := store.(EventRecoveryStore)
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "skipped" {
					if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				notices := store.(EventRecoveryExecutionNotificationStore)
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
					if mem, ok := store.(*MemStore); ok {
						mem.mu.Lock()
						out, err = mem.recoveryExecutionHealthLocked(ctx, app.AccountID, app.ID, time.Now().UTC())
						mem.mu.Unlock()
					} else {
						pg := store.(*PgStore)
						out, err = observeRecoveryExecutionHealth(ctx, sqlc.New(), pg.pool, app.AccountID, app.ID, time.Now().UTC())
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
					if _, err = store.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
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
				if mem, ok := store.(*MemStore); ok {
					mem.mu.Lock()
					delete(mem.workflowRuns, run.ID)
					delete(mem.workflowResumes, run.ID)
					mem.mu.Unlock()
				} else {
					pg := store.(*PgStore)
					if _, err = pg.pool.Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); err != nil {
						t.Fatal(err)
					}
					// New store instance proves capture survives a server restart.
					fresh := NewPgStore(pg.pool.Pool)
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
				if _, err = store.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
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
				report, err := store.(EventRecoveryNotificationsStore).GetEventRecoveryNotifications(ctx, app.AccountID, job.ID, now)
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
