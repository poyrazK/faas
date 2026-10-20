package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1283
func TestWorkflowRecoveryHealthOverview(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		_, app, failure, p := state.SeedInitialReview(t, s)
		reviews := s.(state.WorkflowInitialRecoveryReviewStore)
		r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		read := func() *api.WorkflowRecoveryOverview {
			t.Helper()
			h, e := s.(state.WorkflowRecoveryHealthOverviewStore).GetWorkflowRecoveryPolicyHealth(ctx, app.AccountID, app.ID, 300)
			if e != nil || h.Overview == nil {
				t.Fatalf("health %+v %v", h, e)
			}
			return h.Overview
		}
		o := read()
		if o.ManualAwaitingApproval != 1 || o.Status != "waiting" || len(o.Issues) != 1 || o.Issues[0].ReviewID != r.ID || o.Issues[0].InspectionURL != "/v1/apps/"+app.Slug+"/initial-recovery-reviews/"+r.ID {
			t.Fatalf("prepared %+v", o)
		}
		if _, e := s.(state.WorkflowRecoveryHealthOverviewStore).GetWorkflowRecoveryPolicyHealth(ctx, uuid.NewString(), app.ID, 300); !errors.Is(e, state.ErrNotFound) {
			t.Fatalf("cross account %v", e)
		}
		state.MutateInitialReview(t, s, r.ID, func(r *api.WorkflowInitialRecoveryReview) {
			r.PreparedAt = time.Now().UTC().Truncate(time.Microsecond).Add(-2 * time.Minute)
			r.ExpiresAt = time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
		})
		if o = read(); o.ManualAwaitingApproval != 0 || o.Status != "healthy" {
			t.Fatalf("expired %+v", o)
		}
		state.MutateInitialReview(t, s, r.ID, func(r *api.WorkflowInitialRecoveryReview) {
			r.ExpiresAt = time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		})
		r, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve"})
		if err != nil {
			t.Fatal(err)
		}
		policy := p.Policy
		policy.Enabled = false
		if _, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: policy, ExpectedVersion: p.Version}); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
		state.MutateInitialReview(t, s, r.ID, func(r *api.WorkflowInitialRecoveryReview) {
			r.ObservationFailure = &api.WorkflowInitialObservationFailure{Code: "invalid_recovery_identity", Attempts: 3, FailedAt: at, RetryAt: at.Add(time.Minute)}
		})
		o = read()
		if o.ManualUnresolved != 1 || o.ManualPollingFailures != 1 || o.Status != "needs_attention" || o.Issues[0].RetryAt == nil {
			t.Fatalf("submitted after policy change %+v", o)
		}
		hook, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://overview.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryPollingStatus)}})
		if err != nil {
			t.Fatal(err)
		}
		payload := state.PollingAlertTransition(app, "manual_initial", r.ID, p.Version, "invalid_recovery_identity", 3, nil, nil, at)
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			_, err = m.CapturePollingAlertLocked(app, payload)
			(*m.Mu()).Unlock()
		} else {
			tx, e := (*s.(*state.PgStore).Pool()).Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			_, err = state.CapturePollingAlertTx(ctx, sqlc.New(), tx, app, payload)
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		o = read()
		if o.LastAlert == nil || o.LastAlert.DeliveryStatus != "relay_pending" || o.LastAlert.Expected != 1 {
			t.Fatalf("capture %+v", o.LastAlert)
		}
		// A later subscriber must not change the frozen expected count.
		if _, err = s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://overview.example.test/late", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryPollingStatus)}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.(state.AppWebhookEventOutboxStore).RelayAppWebhookEventOutboxSource(ctx, state.AppWebhookEventRecoveryPollingStatus, payload.EventID); err != nil {
			t.Fatal(err)
		}
		o = read()
		if o.LastAlert.DeliveryStatus != "pending" || o.LastAlert.Pending != 1 || o.LastAlert.Expected != 1 {
			t.Fatalf("relay %+v", o.LastAlert)
		}
		claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, api.EventRecoveryNotificationReceiversMax, time.Now().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range claimed {
			if d.WebhookID == hook.ID && d.Event == state.AppWebhookEventRecoveryPollingStatus {
				err = s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, d.Attempt, d.NextAttemptAt, time.Now())
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		o = read()
		if o.LastAlert.DeliveryStatus != "delivered" || o.LastAlert.Succeeded != 1 || o.Status != "needs_attention" {
			t.Fatalf("delivered doesn't repair monitoring %+v", o)
		}
		// Pruning source reviews cannot erase the durable alert receipt.
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			delete((*m.InitialRecoveryReviews()), r.ID)
			(*m.Mu()).Unlock()
		} else {
			_, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM workflow_initial_recovery_reviews WHERE id=$1::uuid", r.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if o = read(); o.LastAlert == nil || o.LastAlert.DeliveryStatus != "delivered" || o.ManualUnresolved != 0 || o.MonitoringStatus != "unknown" {
			t.Fatalf("source pruned %+v", o)
		}
	})
}

func TestRecoveryOverviewIncompleteAndDeliveryStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alert api.WorkflowRecoveryOverviewAlert
		want  string
	}{
		{"no receivers", api.WorkflowRecoveryOverviewAlert{}, "no_receivers"},
		{"unknown missing", api.WorkflowRecoveryOverviewAlert{Missing: 1}, "unknown"},
		{"dead receiver", api.WorkflowRecoveryOverviewAlert{Dead: 1}, "failed"},
		{"in flight", api.WorkflowRecoveryOverviewAlert{InFlight: 1}, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "no receivers" {
				tc.alert.Notification.ReceiverWebhookIDs = []string{uuid.NewString()}
			}
			state.RecoveryOverviewDelivery(&tc.alert)
			if tc.alert.DeliveryStatus != tc.want {
				t.Fatalf("%+v", tc.alert)
			}
		})
	}
	h := api.WorkflowRecoveryPolicyHealth{Status: "healthy", Queue: api.WorkflowRecoveryAutomaticQueue{ObservedAt: time.Now(), Truncated: true}}
	if got := state.RecoveryOverview(h, state.RecoveryOverviewManual{}, nil, "app"); got.Overview.Status != "unknown" || !got.Overview.Truncated {
		t.Fatalf("incomplete %+v", got.Overview)
	}
}
