package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1275
func TestWorkflowInitialManualCompletion(t *testing.T) {
	for _, scenario := range []string{"succeeded", "failed", "cancelled", "missing_evidence", "paused_failed", "preview_succeeded"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "manual-approver")
				outcome := scenario
				if scenario == "paused_failed" {
					outcome = "failed"
				}
				if scenario == "preview_succeeded" {
					outcome = "succeeded"
				}
				run, app, failure, p := state.SeedInitialReview(t, s)
				reviews := s.(state.WorkflowInitialRecoveryReviewStore)
				r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
				if err != nil {
					t.Fatal(err)
				}
				r, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve recovery"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
					t.Fatal(err)
				}
				if scenario == "missing_evidence" {
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						delete((*m.EventRecoveryJobs()), r.JobID)
						(*m.Mu()).Unlock()
					} else {
						if _, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", r.JobID); err != nil {
							t.Fatal(err)
						}
					}
				}
				// Policy changes cannot revoke observation of a previously approved job.
				p.Policy.Mode, p.Policy.Enabled, p.Policy.HourlyExecutionBudget, p.Policy.PauseAfterFailures = "automatic_initial", false, 1, 1
				if scenario == "paused_failed" {
					p.Policy.Enabled = true
				}
				if scenario == "preview_succeeded" {
					p.Policy.Mode, p.Policy.HourlyExecutionBudget, p.Policy.PauseAfterFailures = "preview", 0, 0
				}
				p, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "paused_failed" {
					p, err = s.(state.WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.ControlWorkflowRecoveryPolicyRequest{Operation: "pause", ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Reason: "manual pause"})
					if err != nil {
						t.Fatal(err)
					}
				}
				status := state.WorkflowRunStatusSucceeded
				if outcome == "failed" {
					status = state.WorkflowRunStatusFailed
				}

				if scenario == "cancelled" {
					if _, err = s.CancelWorkflowRun(ctx, run.ID, "operator cancellation"); err != nil {
						t.Fatal(err)
					}
				} else if scenario != "missing_evidence" {
					if err = s.MarkWorkflowRunStatus(ctx, run.ID, status, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				// Age the review beyond normal retention; unresolved submissions survive.
				state.MutateInitialReview(t, s, r.ID, func(saved *api.WorkflowInitialRecoveryReview) {
					saved.PreparedAt = time.Now().UTC().Add(-2 * api.WorkflowRecoveryHistoryRetention).Truncate(time.Microsecond)
					saved.ExpiresAt = saved.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
				})
				if _, err = reviews.GetWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID); err != nil {
					t.Fatal("unresolved review expired", err)
				}
				// Restore a current prepared date so the completed review remains inspectable.
				state.MutateInitialReview(t, s, r.ID, func(saved *api.WorkflowInitialRecoveryReview) {
					saved.PreparedAt = time.Now().UTC().Truncate(time.Microsecond)
					saved.ExpiresAt = saved.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
				})
				now := time.Now().UTC()
				worker := s.(state.WorkflowInitialManualCompletionStore)
				processed, err := worker.ProcessNextWorkflowInitialManualCompletion(ctx, now)
				if err != nil || !processed {
					t.Fatalf("observation %v %v", processed, err)
				}
				if pg, ok := s.(*state.PgStore); ok {
					s = state.NewPgStore((*pg.Pool()).Pool)
					reviews = s.(state.WorkflowInitialRecoveryReviewStore)
					worker = s.(state.WorkflowInitialManualCompletionStore)
				}
				for _, at := range []time.Time{now, now.Add(time.Minute)} {
					if _, err = worker.ProcessNextWorkflowInitialManualCompletion(ctx, at); err != nil {
						t.Fatal(err)
					}
				}
				saved, err := reviews.GetWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if saved.AutomaticResult != nil || saved.AutomaticEvaluation != nil {
					t.Fatal("manual result changed automatic state")
				}
				if scenario == "missing_evidence" {
					if saved.Result != nil {
						t.Fatal("unverified result recorded")
					}
				} else if saved.Result == nil || saved.Result.Outcome != outcome || saved.Result.ResumeCount != 1 || !saved.Result.Complete {
					t.Fatalf("result %+v", saved.Result)
				}
				current, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if current.AutomaticExecution != nil && (current.AutomaticExecution.ConsecutiveFailures != 0 || (current.AutomaticExecution.PausedAt != nil) != (scenario == "paused_failed")) {
					t.Fatal("manual result charged automatic failure accounting")
				}
				history, err := s.(state.WorkflowRecoveryHistoryStore).GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{RunID: run.ID, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, e := range history.Entries {
					if e.ReviewID == r.ID && e.Kind == "initial_recovery_result" {
						count++
						if e.Outcome != outcome || e.ActorID != "initial_completion_observer" || e.PolicyVersion != r.PolicyVersion || e.JobID != r.JobID {
							t.Fatalf("history %+v", e)
						}
					}
				}
				want := 1
				if scenario == "missing_evidence" {
					want = 0
				}
				if count != want {
					t.Fatalf("result history count %d", count)
				}
			})
		})
	}
}
