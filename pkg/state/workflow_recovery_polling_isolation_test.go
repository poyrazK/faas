package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1278
func TestWorkflowRecoveryPollingIsolation(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		_, automaticApp, automaticReview, _ := seedInitialAutomatic(t, s)
		type pendingReview struct {
			app    App
			review api.WorkflowInitialRecoveryReview
		}
		pending := []pendingReview{}
		for range api.WorkflowInitialManualCompletionPollBatch + 1 {
			_, app, failure, p := seedInitialReview(t, s)
			r, err := s.(WorkflowInitialRecoveryReviewStore).PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
			if err != nil {
				t.Fatal(err)
			}
			r, err = s.(WorkflowInitialRecoveryReviewStore).DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve manual job"})
			if err != nil {
				t.Fatal(err)
			}
			pending = append(pending, pendingReview{app: app, review: r})
		}
		now := time.Now().UTC()
		automatic := s.(WorkflowRecoveryAutomaticPolicyStore)
		manual := s.(WorkflowInitialManualCompletionStore)
		if _, err := manual.ProcessNextWorkflowInitialManualCompletion(ctx, time.Time{}); !errors.Is(err, ErrEventRecoveryQuery) {
			t.Fatalf("manual zero clock %v", err)
		}
		if did, err := automatic.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); err != nil || !did {
			t.Fatalf("automatic work starved %v %v", did, err)
		}
		submitted := initialAutomaticReview(t, s, automaticApp, automaticReview.ID)
		if submitted.Status != "submitted" || submitted.JobID == "" {
			t.Fatal("manual backlog delayed automatic submission")
		}
		for _, item := range pending {
			r := initialAutomaticReview(t, s, item.app, item.review.ID)
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
			r := initialAutomaticReview(t, s, item.app, item.review.ID)
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
