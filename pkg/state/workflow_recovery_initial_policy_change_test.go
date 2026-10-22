package state

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func initialChangeFreshRun(t *testing.T, s Store, app App, template *WorkflowRun) *WorkflowRun {
	t.Helper()
	ctx := context.Background()
	run := &WorkflowRun{AppID: app.ID, WorkflowName: template.WorkflowName, Input: template.Input, DefinitionSnapshot: template.DefinitionSnapshot}
	if err := s.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "send", Input: run.Input}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	failResumeRun(t, s, run, "send")
	return run
}
func initialChangeFillAccountQuota(t *testing.T, s Store, app App, template *WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	other, err := s.CreateApp(ctx, App{AccountID: app.AccountID, Slug: "quota-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: other.ID, Kind: DeploymentKindImage, Status: DeployPending, ImageDigest: "sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	for range api.EventRecoveryActiveJobsMax {
		run := initialChangeFreshRun(t, s, other, template)
		if _, err = s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, other.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestWorkflowRecoveryInitialPolicyChangePreview(t *testing.T) {
	for _, scenario := range []string{"automatic", "preview", "disabled", "workflow_filter", "status_filter", "changed", "pending", "cooldown", "preparation_budget", "execution_budget", "execution_cooldown", "unresolved", "account_quota", "retention_no_prune"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				run, app, failure, p := seedInitialReview(t, s)
				if scenario == "execution_budget" || scenario == "execution_cooldown" || scenario == "unresolved" {
					var r api.WorkflowInitialRecoveryReview
					run, app, r, p = seedInitialAutomatic(t, s)
					pollInitialAutomatic(t, s, time.Now().UTC())
					if scenario != "unresolved" {
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
					mutateInitialReview(t, s, r.ID, func(r *api.WorkflowInitialRecoveryReview) { r.PreparedAt = r.PreparedAt.Add(-2 * time.Minute) })
					run = initialChangeFreshRun(t, s, app, run)
				}
				req := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}
				req.Policy.Mode = "automatic_initial"
				req.Policy.HourlyExecutionBudget = 2
				req.Policy.PauseAfterFailures = 2
				expectedPrep, expectedExecution := "would_prepare", "would_submit"
				switch scenario {
				case "preview":
					req.Policy.Mode = "preview"
					req.Policy.HourlyExecutionBudget = 0
					req.Policy.PauseAfterFailures = 0
					expectedExecution = "not_enabled"
				case "disabled":
					req.Policy.Enabled = false
					expectedPrep = "not_eligible"
					expectedExecution = "not_prepared"
				case "workflow_filter":
					req.Policy.WorkflowNames = []string{"other"}
					expectedPrep = "not_eligible"
					expectedExecution = "not_prepared"
				case "status_filter":
					req.Policy.FailureStatuses = []string{"failed"}
					expectedPrep = "not_eligible"
					expectedExecution = "not_prepared"
				case "changed":
					if _, _, _, err := s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
						t.Fatal(err)
					}
					expectedPrep = "not_eligible"
					expectedExecution = "not_prepared"
				case "pending":
					if _, err := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); err != nil {
						t.Fatal(err)
					}
					expectedPrep = "pending_recovery"
					expectedExecution = "not_prepared"
				case "cooldown", "preparation_budget", "retention_no_prune":
					r, err := s.(WorkflowInitialRecoveryReviewStore).PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
					if err != nil {
						t.Fatal(err)
					}
					expectedPrep = "cooldown"
					expectedExecution = "not_prepared"
					if scenario == "preparation_budget" {
						req.Policy.HourlyActionBudget = 1
						expectedPrep = "hourly_budget"
					}
					if scenario == "retention_no_prune" {
						mutateInitialReview(t, s, r.ID, func(r *api.WorkflowInitialRecoveryReview) {
							r.PreparedAt = r.PreparedAt.Add(-31 * 24 * time.Hour)
							r.ExpiresAt = r.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
						})
						expectedPrep = "would_prepare"
						expectedExecution = "would_submit"
					}
				case "execution_budget":
					req.Policy.HourlyExecutionBudget = 1
					expectedExecution = "hourly_execution_budget"
				case "execution_cooldown":
					expectedExecution = "execution_cooldown"
				case "unresolved":
					expectedExecution = "outstanding_recovery"
				case "account_quota":
					initialChangeFillAccountQuota(t, s, app, run)
					expectedExecution = "execution_quota"
				}
				before := policyChangeEvidence(t, s, app.ID)
				out, err := s.(WorkflowRecoveryPolicyChangeStore).PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, req)
				if err != nil {
					t.Fatal(err)
				}
				sample := out.InitialFailures
				if sample == nil || sample.Coverage != "partial" || sample.InspectedFailures < 1 || sample.Eligible+sample.Excluded+sample.SafetyBlocked != sample.InspectedFailures {
					t.Fatalf("initial sample %+v", sample)
				}
				var candidate *api.WorkflowRecoveryInitialPolicyChangeCandidate
				for i := range sample.Candidates {
					if sample.Candidates[i].Failure.RunID == run.ID {
						candidate = &sample.Candidates[i]
					}
				}
				if candidate == nil || candidate.Preparation != expectedPrep || candidate.Execution != expectedExecution {
					t.Fatalf("candidate %+v, want %s/%s", candidate, expectedPrep, expectedExecution)
				}
				if scenario == "disabled" || scenario == "workflow_filter" || scenario == "status_filter" {
					if sample.Excluded != 1 || candidate.Target != nil {
						t.Fatalf("excluded candidate %+v", sample)
					}
				}
				if scenario == "changed" && sample.SafetyBlocked != 1 {
					t.Fatalf("unsafe failure not blocked %+v", sample)
				}
				if scenario == "cooldown" || scenario == "preparation_budget" {
					if out.HourlyPreparedActions != 1 || sample.RetainedReviews != 1 {
						t.Fatalf("initial preparation charges omitted %+v", out)
					}
				}
				if scenario == "retention_no_prune" && sample.RetainedReviews != 0 {
					t.Fatalf("expired history consumes future review quota %+v", sample)
				}
				if scenario == "unresolved" || scenario == "pending" {
					if !sample.UnresolvedRecovery {
						t.Fatalf("missing unresolved slot %+v", sample)
					}
				}
				if scenario == "account_quota" && sample.RemainingRecoveryJobCapacity != 0 {
					t.Fatalf("quota capacity %+v", sample)
				}
				if expectedExecution == "would_submit" && sample.WouldSubmit != 1 {
					t.Fatalf("no predicted submission %+v", sample)
				}
				if candidate.Target != nil {
					candidate.Target.RunID = "mutated"
				}
				out.Proposed.WorkflowNames[0] = "mutated"
				if after := policyChangeEvidence(t, s, app.ID); string(before) != string(after) {
					t.Fatal("preview mutated policy, reviews, failures, runs, jobs or outbox")
				}
				if scenario == "automatic" {
					stale := req
					stale.ExpectedVersion++
					if _, err = s.(WorkflowRecoveryPolicyChangeStore).PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, stale); !errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
						t.Fatalf("version fence %v", err)
					}
					if _, err = s.(WorkflowRecoveryPolicyChangeStore).PreviewWorkflowRecoveryPolicyChange(ctx, uuid.NewString(), app.ID, req); !errors.Is(err, ErrNotFound) {
						t.Fatalf("ownership %v", err)
					}
				}
			})
		})
	}
}
func TestWorkflowRecoveryInitialPolicyChangeBounds(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app, _, p := seedInitialReview(t, s)
		for i := range api.WorkflowRecoveryPolicyJobsPerPoll + 1 {
			e := workflowFailureEvidence{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), AppID: app.ID, RunID: uuid.NewString(), Workflow: "excluded", FailureStatus: "dead", ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
			if m, ok := s.(*MemStore); ok {
				m.mu.Lock()
				m.workflowFailureEvidence[canonicalMemUUID(app.ID)] = append(m.workflowFailureEvidence[canonicalMemUUID(app.ID)], e)
				m.mu.Unlock()
			} else {
				if _, err := s.(*PgStore).pool.Exec(ctx, "INSERT INTO workflow_recovery_failure_evidence(id,app_id,run_id,workflow,resume_count,failure_status,observed_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,0,$5,$6)", e.ID, e.AppID, e.RunID, e.Workflow, e.FailureStatus, e.ObservedAt); err != nil {
					t.Fatal(err)
				}
			}
		}
		p.Policy.Mode = "automatic_initial"
		p.Policy.HourlyExecutionBudget = 1
		p.Policy.PauseAfterFailures = 1
		out, err := s.(WorkflowRecoveryPolicyChangeStore).PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
		if err != nil {
			t.Fatal(err)
		}
		sample := out.InitialFailures
		if sample == nil || !sample.Truncated || sample.InspectedFailures != api.WorkflowRecoveryPolicyJobsPerPoll || sample.Candidates[0].Failure.RunID != run.ID || sample.Eligible != 1 || sample.Excluded != api.WorkflowRecoveryPolicyJobsPerPoll-1 {
			t.Fatalf("bounded matching-first sample %+v", sample)
		}
	})
}
func TestWorkflowRecoveryInitialPolicyChangeSimulation(t *testing.T) {
	p := testAutomaticRecoveryPolicy("recover")
	p.Mode = "automatic_initial"
	p.Triggers = []string{"initial_failure"}
	now := time.Now().UTC()
	out := recoveryPolicyChangePreview(api.WorkflowRecoveryAutomaticQueue{Policy: api.WorkflowRecoveryPolicyResponse{Version: 1}, ObservedAt: now}, p, nil, recoveryPolicyBudget{})
	pressure := initialPolicyChangePressure{}
	out.InitialFailures = newInitialPolicyChangePreview(pressure)
	budget := recoveryPolicyBudget{}
	f := api.WorkflowInitialFailure{ID: uuid.NewString(), RunID: uuid.NewString(), Workflow: "recover", FailureStatus: "dead"}
	preview := api.WorkflowInitialRecoveryPreview{Failure: f, Reason: "eligible", Target: &api.WorkflowRunRecoveryTarget{RunID: f.RunID, FailureEvidenceID: f.ID}, Blockers: []string{}}
	addInitialPolicyChangeCandidate(&out, preview, false, &budget, &pressure)
	addInitialPolicyChangeCandidate(&out, preview, false, &budget, &pressure)
	if out.InitialFailures.WouldSubmit != 1 || out.InitialFailures.WouldPrepare != 1 || out.InitialFailures.Candidates[1].Preparation != "cooldown" {
		t.Fatalf("unbounded simulation %+v", out)
	}
	quota := initialPolicyChangePressure{retained: api.WorkflowRecoveryPolicyReviewsMax}
	out.InitialFailures = newInitialPolicyChangePreview(quota)
	budget = recoveryPolicyBudget{}
	addInitialPolicyChangeCandidate(&out, preview, false, &budget, &quota)
	if out.InitialFailures.Candidates[0].Preparation != "review_limit" || out.InitialFailures.WouldPrepare != 0 {
		t.Fatal("review quota bypass")
	}
	if !reflect.DeepEqual(preview.Blockers, []string{}) {
		t.Fatal("simulation mutated diagnostic blockers")
	}
}
