package state_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1281
func TestWorkflowRecoveryPollingAlerts(t *testing.T) {
	for _, scope := range []string{"manual_initial", "automatic_policy"} {
		for _, subscribed := range []bool{true, false} {
			name := scope + map[bool]string{true: "/subscribed", false: "/late_receiver"}[subscribed]
			t.Run(name, func(t *testing.T) {
				state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
					ctx := context.Background()
					var app state.App
					var review api.WorkflowInitialRecoveryReview
					var originalPolicy api.WorkflowRecoveryPolicy
					if scope == "automatic_policy" {
						_, app, review, _ = state.SeedInitialAutomatic(t, s)
						p, e := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
						if e != nil {
							t.Fatal(e)
						}
						originalPolicy = p.Policy
					} else {
						_, a, failure, p := state.SeedInitialReviewWithContext(t, ctx, s)
						app = a
						var e error
						reviews := s.(state.WorkflowInitialRecoveryReviewStore)
						review, e = reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
						if e != nil {
							t.Fatal(e)
						}
						review, e = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve recovery"})
						if e != nil {
							t.Fatal(e)
						}
					}
					mutate := func(broken bool) {
						t.Helper()
						if scope == "manual_initial" {
							state.MutateInitialReviewWithContext(t, ctx, s, review.ID, func(r *api.WorkflowInitialRecoveryReview) {
								r.Target.RunID = review.Target.RunID
								if broken {
									r.Target.RunID = "invalid"
								}
							})
							return
						}
						p := originalPolicy
						if broken {
							p.HourlyExecutionBudget = 0
						}
						if m, ok := s.(*state.MemStore); ok {
							(*m.Mu()).Lock()
							(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Response.Policy = p
							(*m.Mu()).Unlock()
							return
						}
						raw, e := json.Marshal(p)
						if e != nil {
							t.Fatal(e)
						}
						if _, e = (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_recovery_policies SET policy=$2::jsonb WHERE app_id=$1::uuid", app.ID, raw); e != nil {
							t.Fatal(e)
						}
					}
					read := func() (*api.WorkflowRecoveryPollingNotification, time.Time) {
						t.Helper()
						if scope == "manual_initial" {
							r := state.InitialAutomaticReviewWithContext(t, ctx, s, app, review.ID)
							var retry time.Time
							if r.ObservationFailure != nil {
								retry = r.ObservationFailure.RetryAt
							}
							return r.PollingNotification, retry
						}
						p, e := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
						if e != nil {
							t.Fatal(e)
						}
						var retry time.Time
						if p.AutomaticExecution.PollingFailure != nil {
							retry = p.AutomaticExecution.PollingFailure.RetryAt
						}
						return p.PollingNotification, retry
					}
					hookCount := 0
					hook := func() string {
						hookCount++
						t.Helper()
						h, e := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: fmt.Sprintf("https://polling-alert.example.test/hook/%d", hookCount), SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryPollingStatus)}})
						if e != nil {
							t.Fatal(e)
						}
						return h.ID
					}
					initialHook := ""
					if subscribed {
						initialHook = hook()
					}
					mutate(true)
					now := time.Now().UTC().Truncate(time.Microsecond)
					poll := func() {
						t.Helper()
						var did bool
						var e error
						if scope == "manual_initial" {
							did, e = s.(state.WorkflowInitialManualCompletionStore).ProcessNextWorkflowInitialManualCompletion(ctx, now)
						} else {
							did, e = s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now)
						}
						if e != nil || !did {
							t.Fatalf("poll %v %v", did, e)
						}
					}
					for attempt := 1; attempt <= api.WorkflowRecoveryPollingAlertAttempts; attempt++ {
						poll()
						n, retry := read()
						if attempt < api.WorkflowRecoveryPollingAlertAttempts && n != nil {
							t.Fatal("early alert")
						}
						now = retry
					}
					blocked, _ := read()
					if blocked == nil || blocked.Payload.State != "failed" || blocked.Payload.Sequence != 1 || !blocked.Payload.MonitoringOnly || blocked.Payload.InspectionURL == "" || blocked.Payload.Scope != scope {
						t.Fatalf("blocked receipt %+v", blocked)
					}
					if subscribed {
						if blocked.Status != "queued" || !reflect.DeepEqual(blocked.ReceiverWebhookIDs, []string{initialHook}) {
							t.Fatal("recipient snapshot", blocked)
						}
					} else if blocked.Status != "no_receivers" || len(blocked.ReceiverWebhookIDs) != 0 {
						t.Fatal("no receiver snapshot", blocked)
					}
					hook()
					poll()
					n, retry := read()
					now = retry
					if !reflect.DeepEqual(n, blocked) {
						t.Fatal("unchanged failure replayed or changed recipients")
					}
					if pg, ok := s.(*state.PgStore); ok {
						s = state.NewPgStore((*pg.Pool()).Pool)
					}
					poll()
					n, retry = read()
					now = retry
					if !reflect.DeepEqual(n, blocked) {
						t.Fatal("alert lost/replayed on restart")
					}
					mutate(false)
					poll()
					recovered, _ := read()
					if recovered == nil || recovered.Payload.State != "recovered" || recovered.Payload.Sequence != 2 || recovered.Payload.RetryAt != nil || !recovered.Payload.MonitoringOnly {
						t.Fatal("recovery receipt", recovered)
					}
					if r := state.InitialAutomaticReviewWithContext(t, ctx, s, app, review.ID); r.Result != nil || r.AutomaticResult != nil {
						t.Fatal("monitoring recovery invented a workflow result")
					}
					// Polling clocks advance into the future to exercise backoff without
					// sleeping. Inspect durable storage rather than bypassing history's
					// public fence, which deliberately rejects future read windows.
					var entries []api.WorkflowRecoveryHistoryEntry
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						for _, event := range (*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].History {
							entries = append(entries, event.Entry)
						}
						(*m.Mu()).Unlock()
					} else {
						rows, e := (*s.(*state.PgStore).Pool()).Query(ctx, "SELECT entry FROM workflow_recovery_history_events WHERE app_id=$1::uuid AND entry->>'kind' IN ('polling_failed','polling_recovered')", app.ID)
						if e != nil {
							t.Fatal(e)
						}
						for rows.Next() {
							var raw []byte
							if e = rows.Scan(&raw); e != nil {
								rows.Close()
								t.Fatal(e)
							}
							var entry api.WorkflowRecoveryHistoryEntry
							if e = json.Unmarshal(raw, &entry); e != nil {
								rows.Close()
								t.Fatal(e)
							}
							entries = append(entries, entry)
						}
						if e = rows.Err(); e != nil {
							rows.Close()
							t.Fatal(e)
						}
						rows.Close()
					}
					failedCount, recoveredCount := 0, 0
					for _, entry := range entries {
						if entry.Kind == "polling_failed" {
							failedCount++
							if !reflect.DeepEqual(entry.PollingNotification, blocked) {
								t.Fatal("history changed frozen failure payload")
							}
						}
						if entry.Kind == "polling_recovered" {
							recoveredCount++
						}
					}
					if failedCount != 1 || recoveredCount != 1 {
						t.Fatalf("history failed %d recovered %d", failedCount, recoveredCount)
					}
					if _, e := s.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 100); e != nil {
						t.Fatal(e)
					}
					deliveries, e := s.ClaimDueAppWebhookDeliveries(ctx, 100, now.Add(time.Hour))
					if e != nil {
						t.Fatal(e)
					}
					failedDeliveries, recoveredDeliveries := 0, 0
					for _, d := range deliveries {
						if d.Event != state.AppWebhookEventRecoveryPollingStatus {
							continue
						}
						var payload api.WorkflowRecoveryPollingWebhookPayload
						if e = json.Unmarshal(d.Payload, &payload); e != nil {
							t.Fatal(e)
						}
						if payload.State == "failed" {
							failedDeliveries++
							if d.WebhookID != initialHook {
								t.Fatal("historical failure replayed to late receiver")
							}
						} else {
							recoveredDeliveries++
						}
					}
					wantFailed, wantRecovered := 0, 1
					if subscribed {
						wantFailed, wantRecovered = 1, 2
					}
					if failedDeliveries != wantFailed || recoveredDeliveries != wantRecovered {
						t.Fatalf("deliveries %d %d", failedDeliveries, recoveredDeliveries)
					}
				})
			})
		}
	}
}

func TestWorkflowRecoveryPollingAlertTransition(t *testing.T) {
	app := state.App{ID: "11111111-1111-4111-8111-111111111111", Slug: "orders"}
	now := time.Now().UTC()
	blocked := state.PollingAlertTransition(app, "automatic_policy", "", 1, "invalid_policy_authority", 3, &now, nil, now)
	n := state.PollingAlertReceipt(*blocked, []string{})
	if state.PollingAlertTransition(app, "automatic_policy", "", 1, "invalid_policy_authority", 32, &now, &n, now) != nil {
		t.Fatal("unchanged failure was not quiet")
	}
	if p := state.PollingAlertTransition(app, "automatic_policy", "", 1, "invalid_polling_data", 4, &now, &n, now); p == nil || p.Sequence != 2 {
		t.Fatal("changed failure did not notify")
	}
	if p := state.PollingAlertTransition(app, "automatic_policy", "", 2, "invalid_policy_authority", 3, &now, &n, now); p == nil || p.Sequence != 2 {
		t.Fatal("changed policy did not notify")
	}
	if state.PollingAlertTransition(app, "automatic_policy", "", 1, "", 0, nil, nil, now) != nil {
		t.Fatal("recovery without prior alert")
	}
}

func TestWorkflowRecoveryPollingAlertPolicyIntent(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		_, app, _, original := state.SeedInitialAutomatic(t, s)
		if m, ok := s.(*state.MemStore); ok {
			(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Response.Policy.HourlyExecutionBudget = 0
		} else if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_recovery_policies SET policy=jsonb_set(policy,'{hourly_execution_budget}','0') WHERE app_id=$1::uuid", app.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		for range api.WorkflowRecoveryPollingAlertAttempts {
			if did, err := s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); err != nil || !did {
				t.Fatalf("poll %v %v", did, err)
			}
			p, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			now = p.AutomaticExecution.PollingFailure.RetryAt
		}
		original.Policy.Enabled = false
		paused, err := s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: original.Version, Policy: original.Policy})
		if err != nil {
			t.Fatal(err)
		}
		if paused.PollingNotification == nil || paused.PollingNotification.Payload.State != "failed" {
			t.Fatal("policy intent falsely cleared notification")
		}
		if did, e := s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); e != nil || did {
			t.Fatalf("disabled policy polled %v %v", did, e)
		}
		original.Policy.Enabled = true
		resumed, err := s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: paused.Version, Policy: original.Policy})
		if err != nil {
			t.Fatal(err)
		}
		if resumed.PollingNotification == nil || resumed.PollingNotification.Payload.State != "failed" {
			t.Fatal("reenabling intent falsely proved recovery")
		}
		if did, e := s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); e != nil || !did {
			t.Fatalf("successful poll %v %v", did, e)
		}
		current, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.PollingNotification.Payload.State != "recovered" || current.PollingNotification.Payload.PolicyVersion != current.Version {
			t.Fatal("actual polling did not clear prior alert")
		}
	})
}

func TestWorkflowRecoveryPollingAlertCaptureRollback(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		pg, ok := s.(*state.PgStore)
		if !ok {
			t.Skip("PostgreSQL capture atomicity")
		}
		ctx := context.Background()
		_, app, _, _ := state.SeedInitialAutomatic(t, s)
		if _, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://polling-alert.example.test/atomic", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryPollingStatus)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_recovery_policies SET policy=jsonb_set(policy,'{hourly_execution_budget}','0') WHERE app_id=$1::uuid", app.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		for range api.WorkflowRecoveryPollingAlertAttempts - 1 {
			if did, err := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); err != nil || !did {
				t.Fatalf("poll %v %v", did, err)
			}
			p, err := pg.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			now = p.AutomaticExecution.PollingFailure.RetryAt
		}
		// Reject receipt persistence after both history and outbox have been written.
		if _, err := (*pg.Pool()).Exec(ctx, "ALTER TABLE workflow_recovery_policies ADD CONSTRAINT test_polling_receipt_failure CHECK (automatic_poll_notification IS NULL)"); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); err == nil {
			t.Fatal("capture failure swallowed")
		}
		p, err := pg.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.AutomaticExecution.PollingFailure.Attempts != int64(api.WorkflowRecoveryPollingAlertAttempts-1) || p.PollingNotification != nil {
			t.Fatal("partial polling state committed")
		}
		var count int
		if err = (*pg.Pool()).QueryRow(ctx, "SELECT count(*) FROM workflow_recovery_history_events WHERE app_id=$1::uuid AND entry->>'kind'='polling_failed'", app.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial alert history committed", count, err)
		}
		if err = (*pg.Pool()).QueryRow(ctx, "SELECT count(*) FROM app_webhook_event_outbox WHERE app_id=$1::uuid AND event='automation.recovery_polling_status'", app.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial alert outbox committed", count, err)
		}
		if _, err = (*pg.Pool()).Exec(ctx, "ALTER TABLE workflow_recovery_policies DROP CONSTRAINT test_polling_receipt_failure"); err != nil {
			t.Fatal(err)
		}
		pg = state.NewPgStore((*pg.Pool()).Pool)
		if did, e := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); e != nil || !did {
			t.Fatalf("capture replay %v %v", did, e)
		}
		p, err = pg.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil || p.PollingNotification == nil || p.PollingNotification.Payload.Sequence != 1 {
			t.Fatal("capture replay lost stable first sequence", err)
		}
	})
}
