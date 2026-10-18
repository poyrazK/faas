package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func testAutomaticRecoveryPolicy(name string) api.WorkflowRecoveryPolicy {
	p := testRecoveryPreviewPolicy(name)
	p.Mode = "automatic_retry"
	p.FailureStatuses = []string{"failed", "dead"}
	p.HourlyExecutionBudget = 10
	p.PauseAfterFailures = 2
	p.MaxResumeCount = 4
	return p
}
func seedAutomaticRecoveryReview(t *testing.T, s Store, modify func(*api.WorkflowRecoveryPolicy)) (api.WorkflowRecoveryPolicyReview, App, *WorkflowRun, time.Time) {
	t.Helper()
	ctx := context.Background()
	run, app := seedResumeRun(t, s, simpleResumeSpec())
	failResumeRun(t, s, run, "send")
	recovery := s.(EventRecoveryStore)
	if _, e := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); e != nil {
		t.Fatal(e)
	}
	if _, e := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ClaimNextDueWorkflowRun(ctx); e != nil {
		t.Fatal(e)
	}
	failResumeRun(t, s, run, "send")
	p := testAutomaticRecoveryPolicy(run.WorkflowName)
	if modify != nil {
		modify(&p)
	}
	policies := s.(WorkflowRecoveryPolicyStore)
	if _, e := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: p}); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Add(2 * time.Minute)
	if _, e := policies.ProcessNextWorkflowRecoveryPolicy(ctx, now); e != nil {
		t.Fatal(e)
	}
	return automaticReviewForGeneration(t, s, app, 1), app, run, now
}
func automaticReviewForGeneration(t *testing.T, s Store, app App, generation int) api.WorkflowRecoveryPolicyReview {
	t.Helper()
	page, e := s.(WorkflowRecoveryPolicyStore).ListWorkflowRecoveryPolicyReviews(context.Background(), app.AccountID, app.ID, "", 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range page.Reviews {
		if r.Plan != nil && len(r.Plan.Actions) == 1 && r.Plan.Actions[0].Retry != nil && r.Plan.Actions[0].Retry.WorkflowRuns.Runs[0].ExpectedResumeCount == generation {
			return r
		}
	}
	t.Fatalf("no prepared generation %d in %+v", generation, page)
	return api.WorkflowRecoveryPolicyReview{}
}
func runAutomaticPolicy(t *testing.T, s Store, now time.Time) {
	t.Helper()
	if _, e := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(context.Background(), now); e != nil {
		t.Fatal(e)
	}
}
func failAutomaticChild(t *testing.T, s Store, run *WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	if did, e := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil || !did {
		t.Fatalf("admission=%t %v", did, e)
	}
	if _, e := s.ClaimNextDueWorkflowRun(ctx); e != nil {
		t.Fatal(e)
	}
	failResumeRun(t, s, run, "send")
}
func automaticChild(t *testing.T, s Store, app App, r api.WorkflowRecoveryPolicyReview) (api.EventRecoveryJob, error) {
	t.Helper()
	return s.(WorkflowRecoveryActionReceiptStore).GetEventRecoveryByRequest(context.Background(), app.AccountID, app.ID, api.WorkflowRecoveryResponseActionID(*r.Plan, r.Plan.Actions[0]))
}
func TestWorkflowRecoveryAutomaticPauseAndRestart(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		review, app, run, now := seedAutomaticRecoveryReview(t, s, nil)
		hook, e := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://auto.example.test/pause", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryPolicyPaused)}})
		if e != nil {
			t.Fatal(e)
		}
		if review.AutomaticExecution == nil || review.AutomaticExecution.Actions[0].Status != "pending" {
			t.Fatalf("missing durable intent=%+v", review)
		}
		runAutomaticPolicy(t, s, now)
		child, e := automaticChild(t, s, app, review)
		if e != nil {
			t.Fatal(e)
		}
		if pg, ok := s.(*PgStore); ok {
			s = NewPgStore(pg.pool.Pool)
		}
		// Concurrent/restarted polls cannot create a second child or launch another
		// action until the first has proven its final execution outcome.
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				_, e := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now.Add(time.Minute))
				results <- e
			}()
		}
		for i := 0; i < 2; i++ {
			if e := <-results; e != nil {
				t.Fatal(e)
			}
		}
		same, e := automaticChild(t, s, app, review)
		if e != nil || same.ID != child.ID {
			t.Fatalf("duplicate child=%+v %v", same, e)
		}
		retained, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
		if e != nil || retained.AutomaticExecution.Actions[0].ChildJobID != child.ID || retained.AutomaticExecution.Actions[0].Status != "applied" {
			t.Fatalf("ledger=%+v %v", retained, e)
		}
		history, e := s.(EventRecoveryStore).ListEventRecoveryHistory(ctx, app.AccountID, child.ID, 0, 10)
		if e != nil || len(history.Entries) == 0 || history.Entries[0].ActorKind != "system" || !strings.HasPrefix(history.Entries[0].ActorID, "recovery_policy:") {
			t.Fatalf("audit=%+v %v", history, e)
		}
		failAutomaticChild(t, s, run)
		runAutomaticPolicy(t, s, now.Add(2*time.Minute))
		p, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if e != nil || p.AutomaticExecution.ConsecutiveFailures != 1 || p.AutomaticExecution.PausedAt != nil {
			t.Fatalf("first failure=%+v %v", p, e)
		}
		if _, e = s.(WorkflowRecoveryPolicyStore).ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(2*time.Minute)); e != nil {
			t.Fatal(e)
		}
		second := automaticReviewForGeneration(t, s, app, 2)
		runAutomaticPolicy(t, s, now.Add(2*time.Minute))
		if _, e = automaticChild(t, s, app, second); e != nil {
			t.Fatal(e)
		}
		failAutomaticChild(t, s, run)
		pauseAt := now.Add(4 * time.Minute)
		runAutomaticPolicy(t, s, pauseAt)
		p, e = s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if e != nil || p.Status != "paused" || p.AutomaticExecution.ConsecutiveFailures != 2 || p.AutomaticExecution.PausedAt == nil || p.AutomaticExecution.PauseReason != "consecutive_failures" {
			t.Fatalf("pause=%+v %v", p, e)
		}
		if did, e := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, pauseAt.Add(time.Hour)); e != nil || did {
			t.Fatalf("paused execution=%t %v", did, e)
		}
		if did, e := s.(WorkflowRecoveryPolicyStore).ProcessNextWorkflowRecoveryPolicy(ctx, pauseAt.Add(time.Hour)); e != nil || did {
			t.Fatalf("paused preparation=%t %v", did, e)
		}
		if _, e := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://auto.example.test/late", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryPolicyPaused)}}); e != nil {
			t.Fatal(e)
		}
		if _, e = s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); e != nil {
			t.Fatal(e)
		}
		rows, e := s.ClaimDueAppWebhookDeliveries(ctx, 32, pauseAt.Add(time.Minute))
		if e != nil {
			t.Fatal(e)
		}
		count := 0
		for _, row := range rows {
			if row.Event != AppWebhookEventRecoveryPolicyPaused {
				continue
			}
			count++
			var payload api.WorkflowRecoveryPolicyPausedWebhookPayload
			if json.Unmarshal(row.Payload, &payload) != nil || row.WebhookID != hook.ID || payload.ConsecutiveFailures != 2 || payload.EventID != recoveryPolicyPauseID(canonicalMemUUID(app.ID), p.Version) || payload.PolicyVersion != p.Version {
				t.Fatalf("pause event=%s", row.Payload)
			}
		}
		if count != 1 {
			t.Fatalf("pause deliveries=%d", count)
		}
		if _, e = s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, uuid.NewString(), app.ID); !errors.Is(e, ErrNotFound) {
			t.Fatalf("foreign=%v", e)
		}
		updated, e := s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
		if e != nil || updated.AutomaticExecution.PausedAt != nil || updated.AutomaticExecution.ConsecutiveFailures != 0 || updated.Version != p.Version+1 {
			t.Fatalf("resume=%+v %v", updated, e)
		}
		if _, e = s.(WorkflowRecoveryPolicyStore).ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(7*time.Minute)); e != nil {
			t.Fatal(e)
		}
		third := automaticReviewForGeneration(t, s, app, 3)
		runAutomaticPolicy(t, s, now.Add(7*time.Minute))
		if _, e = automaticChild(t, s, app, third); e != nil {
			t.Fatalf("explicit resume did not execute: %v", e)
		}
	})
}

func TestWorkflowRecoveryAutomaticAuthority(t *testing.T) {
	for _, scenario := range []string{"disabled", "preview", "version_changed", "dismissed", "manual", "stale", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				r, app, run, now := seedAutomaticRecoveryReview(t, s, nil)
				policies := s.(WorkflowRecoveryPolicyStore)
				switch scenario {
				case "disabled", "preview", "version_changed":
					p, e := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
					if e != nil {
						t.Fatal(e)
					}
					if scenario == "disabled" {
						p.Policy.Enabled = false
					}
					if scenario == "preview" {
						p.Policy.Mode = "preview"
						p.Policy.HourlyExecutionBudget = 0
						p.Policy.PauseAfterFailures = 0
					}
					if scenario == "version_changed" {
						p.Policy.Reason = "new reviewed scope"
					}
					if _, e = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); e != nil {
						t.Fatal(e)
					}
				case "dismissed":
					if _, e := policies.DecideWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowRecoveryReviewDecisionRequest{Decision: "dismissed", Reason: "operator opted out"}); e != nil {
						t.Fatal(e)
					}
				case "manual":
					if _, e := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, *r.Plan.Actions[0].Retry); e != nil {
						t.Fatal(e)
					}
				case "stale":
					if _, _, _, e := s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 1)); e != nil {
						t.Fatal(e)
					}
				case "unknown":
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.workflowRuns, run.ID)
						m.mu.Unlock()
					} else {
						if _, e := s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); e != nil {
							t.Fatal(e)
						}
					}
				}
				runAutomaticPolicy(t, s, now)
				_, e := automaticChild(t, s, app, r)
				if scenario == "manual" {
					if e != nil {
						t.Fatal(e)
					}
				} else if !errors.Is(e, ErrNotFound) {
					t.Fatalf("unauthorized child=%v", e)
				}
				saved, e := policies.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, r.ID)
				if e != nil {
					t.Fatal(e)
				}
				action := saved.AutomaticExecution.Actions[0]
				if action.Status == "applied" {
					t.Fatalf("automatic execution attributed unauthorized action=%+v", action)
				}
				if scenario == "manual" && action.Reason != "already_applied" {
					t.Fatalf("manual receipt=%+v", action)
				}
				if scenario == "unknown" && action.Status != "pending" {
					t.Fatalf("unknown was finalized=%+v", action)
				}
			})
		})
	}
}
func TestWorkflowRecoveryAutomaticBudgets(t *testing.T) {
	for _, scenario := range []string{"hourly", "hourly_pruned", "cooldown"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				first, app, run, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
					p.PauseAfterFailures = 4
					if scenario == "cooldown" {
						p.CooldownSeconds = 180
					}
					if strings.HasPrefix(scenario, "hourly") {
						p.HourlyExecutionBudget = 1
					}
				})
				applyAt := now.Add(10 * time.Minute)
				runAutomaticPolicy(t, s, applyAt)
				failAutomaticChild(t, s, run)
				checkAt := applyAt.Add(time.Minute)
				runAutomaticPolicy(t, s, checkAt)
				if _, e := s.(WorkflowRecoveryPolicyStore).ProcessNextWorkflowRecoveryPolicy(ctx, checkAt); e != nil {
					t.Fatal(e)
				}
				second := automaticReviewForGeneration(t, s, app, 2)
				runAutomaticPolicy(t, s, checkAt)
				retained, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, second.ID)
				wantReason := "hourly_budget"
				if scenario == "cooldown" {
					wantReason = "cooldown"
				}
				if e != nil || retained.AutomaticExecution.Actions[0].Reason != wantReason {
					t.Fatalf("deferred reason=%+v %v", retained, e)
				}
				evaluation := retained.AutomaticExecution.Actions[0].LastEvaluation
				if evaluation == nil || evaluation.Reason != wantReason || !evaluation.EvaluatedAt.Equal(checkAt) || evaluation.WaitingSince == nil || !evaluation.WaitingSince.Equal(checkAt) {
					t.Fatalf("budget evaluation %+v", evaluation)
				}
				if _, e := automaticChild(t, s, app, second); !errors.Is(e, ErrNotFound) {
					t.Fatalf("budget exceeded=%v", e)
				}
				if scenario == "hourly_pruned" {
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.eventRecoveryJobs, first.JobID)
						m.mu.Unlock()
					} else {
						if _, e := s.(*PgStore).pool.Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", first.JobID); e != nil {
							t.Fatal(e)
						}
					}
					runAutomaticPolicy(t, s, checkAt.Add(time.Minute))
					if _, e := automaticChild(t, s, app, second); !errors.Is(e, ErrNotFound) {
						t.Fatalf("pruning refunded spent execution=%v", e)
					}
				}
				next := applyAt.Add(4 * time.Minute)
				if strings.HasPrefix(scenario, "hourly") {
					next = applyAt.Add(61 * time.Minute)
				}
				runAutomaticPolicy(t, s, next)
				if _, e := automaticChild(t, s, app, second); e != nil {
					t.Fatalf("budget did not reopen=%v", e)
				}
			})
		})
	}
}

func TestWorkflowRecoveryAutomaticSuccessAndUnknown(t *testing.T) {
	for _, scenario := range []string{"success", "unknown_execution", "generation_limit", "quota"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				review, app, run, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
					if scenario == "generation_limit" {
						p.MaxResumeCount = 2
					}
				})
				if scenario == "quota" {
					// Retained pending jobs consume native recovery quota without advancing the
					// reviewed generation. Automatic application must defer without charging.
					jobs := []api.EventRecoveryJob{}
					for i := 0; i < api.EventRecoveryActiveJobsMax; i++ {
						job, e := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, ExpectedResumeCount: 1}}}})
						if e != nil {
							t.Fatal(e)
						}
						jobs = append(jobs, job)
					}
					runAutomaticPolicy(t, s, now)
					if _, e := automaticChild(t, s, app, review); !errors.Is(e, ErrNotFound) {
						t.Fatalf("quota child=%v", e)
					}
					retained, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if e != nil || retained.AutomaticExecution.Actions[0].Status != "pending" {
						t.Fatalf("quota consumed intent=%+v %v", retained, e)
					}
					if _, e = s.(EventRecoveryStore).CancelEventRecovery(ctx, app.AccountID, jobs[0].ID); e != nil {
						t.Fatal(e)
					}
					runAutomaticPolicy(t, s, now.Add(time.Minute))
					if _, e = automaticChild(t, s, app, review); e != nil {
						t.Fatalf("quota release=%v", e)
					}
					return
				}
				runAutomaticPolicy(t, s, now)
				if _, e := automaticChild(t, s, app, review); e != nil {
					t.Fatal(e)
				}
				if scenario == "unknown_execution" {
					if _, e := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil {
						t.Fatal(e)
					}
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.workflowRuns, run.ID)
						m.mu.Unlock()
					} else {
						if _, e := s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); e != nil {
							t.Fatal(e)
						}
					}
					runAutomaticPolicy(t, s, now.Add(time.Minute))
					p, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
					if e != nil || p.AutomaticExecution.PausedAt != nil || p.AutomaticExecution.ConsecutiveFailures != 0 {
						t.Fatalf("unknown charged failure=%+v %v", p, e)
					}
					r, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if e != nil || r.AutomaticExecution.Actions[0].Result != nil {
						t.Fatalf("unknown final proof=%+v %v", r, e)
					}
					return
				}
				failAutomaticChild(t, s, run)
				runAutomaticPolicy(t, s, now.Add(time.Minute))
				if scenario == "generation_limit" {
					if _, e := s.(WorkflowRecoveryPolicyStore).ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(2*time.Minute)); e != nil {
						t.Fatal(e)
					}
					page, e := s.(WorkflowRecoveryPolicyStore).ListWorkflowRecoveryPolicyReviews(ctx, app.AccountID, app.ID, "", 100)
					if e != nil {
						t.Fatal(e)
					}
					found := false
					for _, r := range page.Reviews {
						for _, d := range r.Decisions {
							if d.ResumeCount == 2 {
								found = true
								if d.Outcome != "generation_limit" || r.Plan != nil {
									t.Fatalf("generation limit bypassed=%+v", r)
								}
							}
						}
					}
					if !found {
						t.Fatal("generation limit not explained")
					}
					return
				}
				if _, e := s.(WorkflowRecoveryPolicyStore).ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(2*time.Minute)); e != nil {
					t.Fatal(e)
				}
				second := automaticReviewForGeneration(t, s, app, 2)
				runAutomaticPolicy(t, s, now.Add(2*time.Minute))
				if _, e := automaticChild(t, s, app, second); e != nil {
					t.Fatal(e)
				}
				if _, e := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil {
					t.Fatal(e)
				}
				if _, e := s.ClaimNextDueWorkflowRun(ctx); e != nil {
					t.Fatal(e)
				}
				if e := s.MarkWorkflowRunStatus(WithWorkflowRunGeneration(ctx, run.ID, 3), run.ID, WorkflowRunStatusSucceeded, nil, nil); e != nil {
					t.Fatal(e)
				}
				runAutomaticPolicy(t, s, now.Add(3*time.Minute))
				p, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if e != nil || p.AutomaticExecution.ConsecutiveFailures != 0 || p.AutomaticExecution.PausedAt != nil {
					t.Fatalf("success did not reset streak=%+v %v", p, e)
				}
				r, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, second.ID)
				if e != nil || r.AutomaticExecution.Actions[0].Result == nil || r.AutomaticExecution.Actions[0].Result.Outcome != "succeeded" {
					t.Fatalf("success proof=%+v %v", r, e)
				}
			})
		})
	}
}

func TestWorkflowRecoveryAutomaticAdmissionAndNoReceivers(t *testing.T) {
	for _, scenario := range []string{"no_receivers", "admission_refused", "admission_cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				review, app, run, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) { p.PauseAfterFailures = 1 })
				runAutomaticPolicy(t, s, now)
				child, e := automaticChild(t, s, app, review)
				if e != nil {
					t.Fatal(e)
				}
				switch scenario {
				case "no_receivers":
					failAutomaticChild(t, s, run)
				case "admission_refused":
					if _, _, _, e = s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 1)); e != nil {
						t.Fatal(e)
					}
					if _, e = s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil {
						t.Fatal(e)
					}
				case "admission_cancelled":
					if _, e = s.(EventRecoveryStore).CancelEventRecovery(ctx, app.AccountID, child.ID); e != nil {
						t.Fatal(e)
					}
				}
				runAutomaticPolicy(t, s, now.Add(time.Minute))
				p, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if e != nil {
					t.Fatal(e)
				}
				want := "failed"
				if scenario == "admission_cancelled" {
					want = "cancelled"
				}
				if scenario != "admission_cancelled" && p.Status != "paused" {
					t.Fatalf("failure not paused=%+v", p)
				}
				if scenario == "admission_cancelled" && (p.Status == "paused" || p.AutomaticExecution.ConsecutiveFailures != 0) {
					t.Fatalf("cancelled admission charged failure=%+v", p)
				}
				retained, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if e != nil || retained.AutomaticExecution.Actions[0].Result == nil || retained.AutomaticExecution.Actions[0].Result.Outcome != want {
					t.Fatalf("terminal admission=%+v %v", retained, e)
				}
				if _, e = s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, Enabled: true, TargetURL: "https://auto.example.test/late-only", SecretSealed: []byte("sealed"), EventFilter: []string{string(AppWebhookEventRecoveryPolicyPaused)}}); e != nil {
					t.Fatal(e)
				}
				if _, e = s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); e != nil {
					t.Fatal(e)
				}
				rows, e := s.ClaimDueAppWebhookDeliveries(ctx, 32, now.Add(2*time.Minute))
				if e != nil {
					t.Fatal(e)
				}
				for _, row := range rows {
					if row.Event == AppWebhookEventRecoveryPolicyPaused {
						t.Fatal("historical pause replayed to new receiver")
					}
				}
			})
		})
	}
}

func TestWorkflowRecoveryAutomaticDismissalFence(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("PostgreSQL transaction fence; memory store uses the shared mutex")
		}
		r, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		tx, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		raw, _ := json.Marshal(newRecoveryReviewDecision(WithEventRecoveryActor(ctx, "api_key", "reviewer"), api.WorkflowRecoveryReviewDecisionRequest{Decision: "dismissed", Reason: "operator stopped pending recovery"}))
		if _, err = sqlc.New().WorkflowRecoveryPolicyReviewDecide(ctx, tx, sqlc.WorkflowRecoveryPolicyReviewDecideParams{AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), ID: mustPgUUID(r.ID), Decision: raw}); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); done <- err }()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err = pg.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%WorkflowRecoveryAutomaticPending%')").Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("automatic worker bypassed uncommitted review decision: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if _, err = automaticChild(t, s, app, r); !errors.Is(err, ErrNotFound) {
			t.Fatalf("dismissal raced automatic child creation: %v", err)
		}
		retained, err := pg.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, r.ID)
		if err != nil || retained.AutomaticExecution.Actions[0].Reason != "dismissed" {
			t.Fatalf("dismissal receipt=%+v %v", retained, err)
		}
	})
}
