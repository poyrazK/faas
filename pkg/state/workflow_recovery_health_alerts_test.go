package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryHealthAlertLifecycle(t *testing.T) {
	for _, scenario := range []string{"receivers", "no_receivers", "opt_out", "alert_only", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := context.Background()
				review, app, _, now := state.SeedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
					p.HealthAlerts = &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}
				})
				policyStore := s.(state.WorkflowRecoveryPolicyStore)
				operator := s.(state.WorkflowRecoveryPolicyOperatorStore)
				p, err := policyStore.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				p, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Operation: "pause", Reason: "investigate provider"})
				if err != nil {
					t.Fatal(err)
				}
				filters := []string{string(state.AppWebhookEventRecoveryHealthAlert), string(state.AppWebhookEventRecoveryHealthRecovered)}
				if scenario == "alert_only" {
					filters = filters[:1]
				}
				var hook state.AppWebhook
				if scenario != "no_receivers" {
					hook, err = s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://health.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: filters})
					if err != nil {
						t.Fatal(err)
					}
				}
				worker := s.(state.WorkflowRecoveryHealthAlertStore)
				if worked, err := worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now); err != nil || !worked {
					t.Fatalf("open %t %v", worked, err)
				}
				q, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || q.HealthIncident == nil || q.HealthIncident.AlertNotification != nil {
					t.Fatalf("early alert %+v %v", q.HealthIncident, err)
				}
				episode := q.HealthIncident.EpisodeID
				if worked, err := worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now); err != nil || worked {
					t.Fatalf("poll fence %t %v", worked, err)
				}
				before, err := policyStore.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "concurrent" {
					var wg sync.WaitGroup
					results := make(chan error, 2)
					for n := 0; n < 2; n++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, e := worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(time.Minute))
							results <- e
						}()
					}
					wg.Wait()
					close(results)
					for e := range results {
						var pgErr *pgconn.PgError
						if e != nil && (!errors.As(e, &pgErr) || pgErr.Code != "40001") {
							t.Fatal(e)
						}
					}
				}
				if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				q, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				incident := q.HealthIncident
				want := "queued"
				if scenario == "no_receivers" {
					want = "no_receivers"
				}
				if incident == nil || incident.EpisodeID != episode || incident.AlertNotification == nil || incident.AlertNotification.Status != want || q.Pending != 1 || q.Outstanding != 0 || q.HourlyExecutions != 0 {
					t.Fatalf("capture %+v queue %+v", incident, q)
				}
				after, err := policyStore.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if err != nil {
					t.Fatal(err)
				}
				a, _ := json.Marshal(before)
				b, _ := json.Marshal(after)
				if string(a) != string(b) {
					t.Fatal("health worker changed retry intent")
				}
				// Adding a receiver after capture must not expand recovery recipients.
				if _, err = s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://health.example.test/late", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryHealthAlert), string(state.AppWebhookEventRecoveryHealthRecovered)}}); err != nil {
					t.Fatal(err)
				}
				if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
				// Full replacement disables automatic execution and clears the pause;
				// it keeps alert capture and incident identity until the monitor closes it.
				p.Policy.Enabled = false
				if scenario == "opt_out" {
					p.Policy.HealthAlerts.Enabled = false
				}
				if _, err = policyStore.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
					t.Fatal(err)
				}
				if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(3*time.Minute)); err != nil {
					t.Fatal(err)
				}
				q, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if q.HealthIncident == nil || q.HealthIncident.ClosedAt == nil || q.HealthIncident.EpisodeID != episode {
					t.Fatalf("did not close %+v", q.HealthIncident)
				}
				if scenario == "opt_out" {
					if q.HealthIncident.RecoveryNotification != nil {
						t.Fatal("opt out emitted recovery")
					}
				} else if q.HealthIncident.RecoveryNotification == nil {
					t.Fatal("missing recovery capture")
				}
				if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(4*time.Minute)); err != nil {
					t.Fatal(err)
				}
				if _, err = s.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, api.EventRecoveryHistoryPageMax); err != nil {
					t.Fatal(err)
				}
				rows, err := s.ClaimDueAppWebhookDeliveries(ctx, api.EventRecoveryHistoryPageMax, now.Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				alerts, recovered := 0, 0
				for _, row := range rows {
					if row.Event != state.AppWebhookEventRecoveryHealthAlert && row.Event != state.AppWebhookEventRecoveryHealthRecovered {
						continue
					}
					var payload api.WorkflowRecoveryHealthWebhookPayload
					if json.Unmarshal(row.Payload, &payload) != nil || payload.EpisodeID != episode || !state.SameMemUUID(payload.AppID, app.ID) || !state.SameMemUUID(row.WebhookID, hook.ID) {
						t.Fatalf("payload %s receiver %s", row.Payload, row.WebhookID)
					}
					if row.Event == state.AppWebhookEventRecoveryHealthAlert {
						alerts++
						if payload.EventID != incident.AlertNotification.EventID || payload.PolicyVersion != review.PolicyVersion || payload.Status != "paused" {
							t.Fatalf("historical alert %s", row.Payload)
						}
					} else {
						recovered++
						if payload.Status != "inactive" || payload.PolicyVersion != review.PolicyVersion+1 {
							t.Fatalf("historical recovery %s", row.Payload)
						}
					}
				}
				wantAlert, wantRecovery := 1, 1
				if scenario == "no_receivers" {
					wantAlert, wantRecovery = 0, 0
				}
				if scenario == "opt_out" || scenario == "alert_only" {
					wantRecovery = 0
				}
				if alerts != wantAlert || recovered != wantRecovery {
					t.Fatalf("deliveries alert=%d recovery=%d", alerts, recovered)
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				beforeEvidence := state.PolicyChangeEvidence(t, s, app.ID)
				if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(cancelled, now.Add(time.Hour)); err == nil {
					t.Fatal("cancelled monitor succeeded")
				}
				if string(beforeEvidence) != string(state.PolicyChangeEvidence(t, s, app.ID)) {
					t.Fatal("cancelled monitor changed state")
				}
			})
		})
	}
}

func TestWorkflowRecoveryHealthIncidentTransitions(t *testing.T) {
	now := time.Now().UTC()
	cfg := &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}
	h := api.WorkflowRecoveryPolicyHealth{Status: "blocked", Reason: "execution_quota", Queue: api.WorkflowRecoveryAutomaticQueue{ObservedAt: now, Policy: api.WorkflowRecoveryPolicyResponse{Policy: api.WorkflowRecoveryPolicy{HealthAlerts: cfg}}}}
	i, event := state.RecoveryHealthTransition(nil, h, true)
	if event != "" || i == nil {
		t.Fatal("early event")
	}
	h.Status = "unknown"
	h.Queue.ObservedAt = now.Add(time.Minute)
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != "" || i.ClosedAt != nil {
		t.Fatal("unknown recovered")
	}
	h.Status = "stalled"
	h.Reason = "evaluation_overdue"
	h.Queue.ObservedAt = now.Add(90 * time.Second)
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != "" {
		t.Fatal("unknown gap counted as sustained health failure")
	}
	h.Queue.ObservedAt = now.Add(2 * time.Minute)
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != state.AppWebhookEventRecoveryHealthAlert {
		t.Fatal("missing due alert")
	}
	payload := state.RecoveryHealthPayload(i, h, "app", event)
	state.RecoveryHealthCapture(i, payload, event, []string{})
	id := i.EpisodeID
	h.Status = "paused"
	h.Queue.ObservedAt = now.Add(3 * time.Minute)
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != "" || i.EpisodeID != id {
		t.Fatal("reason change duplicated incident")
	}
	h.Status = "unknown"
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != "" || i.ClosedAt != nil {
		t.Fatal("unknown resolved alert")
	}
	h.Status = "healthy"
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != state.AppWebhookEventRecoveryHealthRecovered {
		t.Fatal("missing resolution")
	}
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != "" {
		t.Fatal("duplicate resolution")
	}
	h.Status = "blocked"
	i, event = state.RecoveryHealthTransition(i, h, true)
	if event != "" || i.EpisodeID == id {
		t.Fatal("new incident reused old identity")
	}
}
