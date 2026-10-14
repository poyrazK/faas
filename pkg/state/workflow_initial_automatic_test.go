package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func seedInitialAutomatic(t *testing.T, s Store) (*WorkflowRun, App, api.WorkflowInitialRecoveryReview, api.WorkflowRecoveryPolicyResponse) {
	t.Helper()
	run, app, failure, p := seedInitialReview(t, s)
	p.Policy.Mode, p.Policy.HourlyExecutionBudget, p.Policy.PauseAfterFailures = "automatic_initial", 1, 1
	var err error
	p, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(context.Background(), app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.(WorkflowInitialRecoveryReviewStore).PrepareWorkflowInitialRecoveryReview(context.Background(), app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	return run, app, review, p
}
func pollInitialAutomatic(t *testing.T, s Store, now time.Time) {
	t.Helper()
	if _, err := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}
func initialAutomaticReview(t *testing.T, s Store, app App, id string) api.WorkflowInitialRecoveryReview {
	t.Helper()
	return initialAutomaticReviewWithContext(t, context.Background(), s, app, id)
}

func initialAutomaticReviewWithContext(t *testing.T, ctx context.Context, s Store, app App, id string) api.WorkflowInitialRecoveryReview {
	t.Helper()
	r, err := s.(WorkflowInitialRecoveryReviewStore).GetWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestWorkflowInitialAutomaticLifecycle(t *testing.T) {
	for _, scenario := range []string{"failure_pause", "success", "unknown", "pause_before_admission", "changed_before_admission", "cancel_pending", "expiry", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				run, app, review, p := seedInitialAutomatic(t, s)
				var hookID string
				if scenario == "failure_pause" {
					h, err := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://first.example.test/paused", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryPolicyPaused)}})
					if err != nil {
						t.Fatal(err)
					}
					hookID = h.ID
				}
				now := time.Now().UTC()
				operator := s.(WorkflowRecoveryPolicyOperatorStore)
				reviews := s.(WorkflowInitialRecoveryReviewStore)
				queue, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || queue.Pending != 1 || queue.RemainingBudget != 1 || queue.Entries[0].Action.ActionID != review.ID {
					t.Fatalf("pending queue %+v %v", queue, err)
				}
				if scenario == "expiry" {
					mutateInitialReview(t, s, review.ID, func(r *api.WorkflowInitialRecoveryReview) {
						r.PreparedAt = r.PreparedAt.Add(-25 * time.Hour)
						r.ExpiresAt = time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
					})
				}
				if scenario == "cancel_pending" {
					action, err := s.(WorkflowRecoveryAutomaticActionOperatorStore).SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, review.ID, api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: p.Version, Reason: "operator declined"})
					if err != nil || action.Status != "suppressed" {
						t.Fatalf("suppress %+v %v", action, err)
					}
				}
				if scenario == "concurrent" {
					var wg sync.WaitGroup
					errs := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, e := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now)
							errs <- e
						}()
					}
					wg.Wait()
					close(errs)
					for e := range errs {
						if e != nil {
							t.Fatal(e)
						}
					}
				} else {
					pollInitialAutomatic(t, s, now)
				}
				r := initialAutomaticReview(t, s, app, review.ID)
				if scenario == "expiry" || scenario == "cancel_pending" {
					if r.JobID != "" {
						t.Fatalf("dismissed/expired review submitted %+v", r)
					}
					return
				}
				if r.Status != "submitted" || r.JobID == "" || r.AutomaticEvaluation == nil {
					t.Fatalf("submission %+v", r)
				}
				first := r.JobID
				pollInitialAutomatic(t, s, now.Add(time.Minute))
				r = initialAutomaticReview(t, s, app, review.ID)
				if r.JobID != first || r.AutomaticResult != nil {
					t.Fatalf("duplicate/premature result %+v", r)
				}
				queue, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || queue.Outstanding != 1 || queue.HourlyExecutions != 1 || queue.RemainingBudget != 0 {
					t.Fatalf("outstanding queue %+v %v", queue, err)
				}
				if scenario == "pause_before_admission" {
					request := api.ControlWorkflowRecoveryPolicyRequest{Operation: "pause", ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Reason: "investigate"}
					p, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, request)
					if err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "changed_before_admission" {
					p.Policy.Reason = "new incident settings"
					p, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
					if err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "unknown" {
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.eventRecoveryJobs, first)
						m.mu.Unlock()
					} else {
						if _, err = s.(*PgStore).pool.Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", first); err != nil {
							t.Fatal(err)
						}
					}
					pollInitialAutomatic(t, s, now.Add(2*time.Minute))
					r = initialAutomaticReview(t, s, app, review.ID)
					if r.AutomaticResult != nil || r.AutomaticEvaluation.Reason != "evidence_unknown" {
						t.Fatalf("unknown became final %+v", r)
					}
					return
				}
				if did, e := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil || !did {
					t.Fatalf("admit %v %v", did, e)
				}
				current, err := s.GetWorkflowRun(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "pause_before_admission" || scenario == "changed_before_admission" {
					if current.ResumeCount != 0 {
						t.Fatalf("obsolete/paused job admitted %+v", current)
					}
					if scenario == "pause_before_admission" {
						request := api.ControlWorkflowRecoveryPolicyRequest{Operation: "resume", ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, ExpectedPausedAt: p.AutomaticExecution.PausedAt, Reason: "resolved"}
						if _, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, request); err != nil {
							t.Fatal(err)
						}
					}
					pollInitialAutomatic(t, s, now.Add(2*time.Minute))
					r = initialAutomaticReview(t, s, app, review.ID)
					if r.AutomaticResult == nil || r.AutomaticResult.Outcome != "cancelled" {
						t.Fatalf("refusal result %+v", r)
					}
					return
				}
				if current.ResumeCount != 1 {
					t.Fatalf("wrong resume %+v", current)
				}
				if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
					t.Fatal(err)
				}
				if scenario == "success" {
					if err = s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					failResumeRun(t, s, run, "send")
				}
				pollInitialAutomatic(t, s, now.Add(2*time.Minute))
				r = initialAutomaticReview(t, s, app, review.ID)
				expected := "failed"
				if scenario == "success" {
					expected = "succeeded"
				}
				if r.AutomaticResult == nil || r.AutomaticResult.Outcome != expected || !r.AutomaticResult.Complete || r.AutomaticResult.ResumeCount != 1 {
					t.Fatalf("final result %+v", r)
				}
				p, err = s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "success" {
					if p.AutomaticExecution.PausedAt != nil || p.AutomaticExecution.ConsecutiveFailures != 0 {
						t.Fatalf("success paused %+v", p)
					}
				} else {
					if p.AutomaticExecution.PausedAt == nil || p.AutomaticExecution.ConsecutiveFailures != 1 {
						t.Fatalf("failure did not pause %+v", p)
					}
				}
				if hookID != "" {
					if _, err = s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
						t.Fatal(err)
					}
					deliveries, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, now.Add(3*time.Minute))
					if err != nil {
						t.Fatal(err)
					}
					count := 0
					for _, d := range deliveries {
						if d.Event == AppWebhookEventRecoveryPolicyPaused {
							count++
							var payload api.WorkflowRecoveryPolicyPausedWebhookPayload
							if json.Unmarshal(d.Payload, &payload) != nil || d.WebhookID != hookID || payload.ConsecutiveFailures != 1 {
								t.Fatalf("pause notification %s", d.Payload)
							}
						}
					}
					if count != 1 {
						t.Fatalf("pause notifications %d", count)
					}
				}
				if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "retry request"}); err != nil {
					t.Fatal(err)
				}
				queue, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || queue.Outstanding != 0 || queue.HourlyExecutions != 1 {
					t.Fatalf("completed queue %+v %v", queue, err)
				}
			})
		})
	}
}
func TestWorkflowInitialAutomaticExecutionPressure(t *testing.T) {
	for _, scenario := range []string{"hourly", "cooldown", "outstanding", "manual_pending"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				run, app, r, p := seedInitialAutomatic(t, s)
				if scenario == "manual_pending" {
					if _, err := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); err != nil {
						t.Fatal(err)
					}
				} else {
					pollInitialAutomatic(t, s, time.Now().UTC())
					if scenario != "outstanding" {
						if _, err := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
							t.Fatal(err)
						}
						if _, err := s.ClaimNextDueWorkflowRun(ctx); err != nil {
							t.Fatal(err)
						}
						if err := s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
							t.Fatal(err)
						}
						pollInitialAutomatic(t, s, time.Now().UTC().Add(time.Minute))
					}
					if scenario == "cooldown" {
						p.Policy.HourlyExecutionBudget = 10
					}
					p.Policy.Reason = "new version keeps charges"
					var err error
					p, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
					if err != nil {
						t.Fatal(err)
					}
					// A distinct eligible generation-zero run in this app.
					fresh := &WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot}
					if err = s.CreateWorkflowRun(ctx, fresh); err != nil {
						t.Fatal(err)
					}
					if err = s.CreateWorkflowSteps(ctx, fresh.ID, []*WorkflowStep{{StepName: "send", Input: fresh.Input}}); err != nil {
						t.Fatal(err)
					}
					if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
						t.Fatal(err)
					}
					failResumeRun(t, s, fresh, "send")
					page, err := s.(WorkflowInitialRecoveryStore).ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, api.WorkflowInitialFailureOptions{})
					if err != nil {
						t.Fatal(err)
					}
					var failure string
					for _, e := range page.Items {
						if e.RunID == fresh.ID {
							failure = e.ID
						}
					}
					// Preparation cooldown also persists. Backdate only preparation evidence to isolate execution pressure.
					mutateInitialReview(t, s, r.ID, func(v *api.WorkflowInitialRecoveryReview) { v.PreparedAt = v.PreparedAt.Add(-2 * time.Minute) })
					r, err = s.(WorkflowInitialRecoveryReviewStore).PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
					if err != nil {
						t.Fatal(err)
					}
				}
				_, err := s.(WorkflowInitialRecoveryReviewStore).DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "must remain bounded"})
				if !errors.Is(err, ErrWorkflowInitialReviewBlocked) {
					t.Fatalf("pressure bypass %v", err)
				}
				pollInitialAutomatic(t, s, time.Now().UTC())
				r = initialAutomaticReview(t, s, app, r.ID)
				if r.JobID != "" {
					t.Fatalf("pressure submitted %+v", r)
				}
			})
		})
	}
}

func TestWorkflowInitialAutomaticPauseIdentity(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	p := api.WorkflowRecoveryPolicyResponse{AppID: uuid.NewString(), Version: 2, Policy: api.WorkflowRecoveryPolicy{Mode: "automatic_initial"}, AutomaticExecution: &api.WorkflowRecoveryPolicyAutomaticExecution{PausedAt: &at}}
	first := recoveryPolicyPausedPayload(p, "app")
	again := recoveryPolicyPausedPayload(p, "app")
	if first.EventID != again.EventID {
		t.Fatal("replayed pause must deduplicate")
	}
	next := at.Add(time.Minute)
	p.AutomaticExecution.PausedAt = &next
	if recoveryPolicyPausedPayload(p, "app").EventID == first.EventID {
		t.Fatal("pause after resume must emit a new event")
	}
}
