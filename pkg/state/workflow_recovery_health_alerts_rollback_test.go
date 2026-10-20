package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryHealthAlertAtomicCapture(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		pg, ok := s.(*state.PgStore)
		if !ok {
			t.Skip("Postgres transaction failure")
		}
		ctx := context.Background()
		_, app, _, now := state.SeedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
			p.HealthAlerts = &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}
		})
		p, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		operator := s.(state.WorkflowRecoveryPolicyOperatorStore)
		if _, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Operation: "pause", Reason: "inspect"}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://health.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryHealthAlert)}}); err != nil {
			t.Fatal(err)
		}
		worker := s.(state.WorkflowRecoveryHealthAlertStore)
		if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now); err != nil {
			t.Fatal(err)
		}
		q, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		id := q.HealthIncident.EpisodeID
		before := state.PolicyChangeEvidence(t, s, app.ID)
		if _, err = (*pg.Pool()).Exec(ctx, "ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT health_alert_failure_test CHECK(event<>'automation.recovery_health_alert')"); err != nil {
			t.Fatal(err)
		}
		if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(time.Minute)); err == nil {
			t.Fatal("outbox failure accepted")
		}
		if string(before) != string(state.PolicyChangeEvidence(t, s, app.ID)) {
			t.Fatal("failed capture changed policy or outbox")
		}
		q, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil || q.HealthIncident.EpisodeID != id || q.HealthIncident.AlertNotification != nil {
			t.Fatalf("partial capture %+v %v", q.HealthIncident, err)
		}
		if _, err = (*pg.Pool()).Exec(ctx, "ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT health_alert_failure_test"); err != nil {
			t.Fatal(err)
		}
		if worked, err := worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(time.Minute)); err != nil || !worked {
			t.Fatalf("retry %t %v", worked, err)
		}
		q, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil || q.HealthIncident.EpisodeID != id || q.HealthIncident.AlertNotification == nil {
			t.Fatalf("retry failed %+v %v", q.HealthIncident, err)
		}
	})
}
