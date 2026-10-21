package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryHealthAlertRetainsChild(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		review, app, _, now := state.SeedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
			p.HealthAlerts = &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}
		})
		state.RunAutomaticPolicy(t, s, now)
		operator := s.(state.WorkflowRecoveryPolicyOperatorStore)
		q, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil || q.Outstanding != 1 {
			t.Fatalf("child %+v %v", q, err)
		}
		child := q.Entries[0].Action.ChildJobID
		p := q.Policy
		p.Policy.Enabled = false
		if _, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
			t.Fatal(err)
		}
		worker := s.(state.WorkflowRecoveryHealthAlertStore)
		if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err = worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, now.Add(6*time.Minute)); err != nil {
			t.Fatal(err)
		}
		q, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if q.Outstanding != 1 || q.HourlyExecutions != 1 || q.HealthIncident == nil || q.HealthIncident.Status != "stalled" || q.HealthIncident.AlertNotification == nil || q.Entries[0].Action.ChildJobID != child || q.Entries[0].Action.Result != nil || q.Entries[0].PolicyVersion != review.PolicyVersion {
			t.Fatalf("monitor changed committed child %+v", q)
		}
	})
}
