package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1278
func TestWorkflowRecoveryPollingIsolation(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		_, automaticApp, automaticReview, _ := state.SeedInitialAutomatic(t, s)
		type pendingReview struct {
			app    state.App
			review api.WorkflowInitialRecoveryReview
		}
		pending := []pendingReview{}
		for range api.WorkflowInitialManualCompletionPollBatch + 1 {
			_, app, failure, p := state.SeedInitialReview(t, s)
			r, err := s.(state.WorkflowInitialRecoveryReviewStore).PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
			if err != nil {
				t.Fatal(err)
			}
			r, err = s.(state.WorkflowInitialRecoveryReviewStore).DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve manual job"})
			if err != nil {
				t.Fatal(err)
			}
			pending = append(pending, pendingReview{app: app, review: r})
		}
		now := time.Now().UTC()
		automatic := s.(state.WorkflowRecoveryAutomaticPolicyStore)
		manual := s.(state.WorkflowInitialManualCompletionStore)
		if _, err := manual.ProcessNextWorkflowInitialManualCompletion(ctx, time.Time{}); !errors.Is(err, state.ErrEventRecoveryQuery) {
			t.Fatalf("manual zero clock %v", err)
		}
		if did, err := automatic.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); err != nil || !did {
			t.Fatalf("automatic work starved %v %v", did, err)
		}
		submitted := state.InitialAutomaticReview(t, s, automaticApp, automaticReview.ID)
		if submitted.Status != "submitted" || submitted.JobID == "" {
			t.Fatal("manual backlog delayed automatic submission")
		}
		for _, item := range pending {
			r := state.InitialAutomaticReview(t, s, item.app, item.review.ID)
			if r.Attention != nil || r.Result != nil {
				t.Fatal("automatic worker consumed manual observation")
			}
		}
		for range api.WorkflowInitialManualCompletionPollBatch {
			if did, err := manual.ProcessNextWorkflowInitialManualCompletion(ctx, now); err != nil || !did {
				t.Fatalf("manual work starved %v %v", did, err)
			}
		}
		observed := 0
		for _, item := range pending {
			r := state.InitialAutomaticReview(t, s, item.app, item.review.ID)
			if r.Attention != nil {
				observed++
			}
			if r.Result != nil || r.JobID != item.review.JobID || r.AutomaticEvaluation != nil {
				t.Fatal("observation granted execution or completion")
			}
		}
		if observed != api.WorkflowInitialManualCompletionPollBatch {
			t.Fatalf("observed %d", observed)
		}
		if did, err := manual.ProcessNextWorkflowInitialManualCompletion(ctx, now); err != nil || !did {
			t.Fatalf("remaining manual review %v %v", did, err)
		}
		if did, err := manual.ProcessNextWorkflowInitialManualCompletion(ctx, now); err != nil || did {
			t.Fatalf("due review replayed %v %v", did, err)
		}
	})
}
