package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func testRecoveryPreviewPolicy(name string) api.WorkflowRecoveryPolicy {
	return api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{name}, Triggers: []string{"saved_failure"}, FailureStatuses: []string{"failed", "dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "review provider incident"}
}
func TestWorkflowRecoveryPolicyPreview(t *testing.T) {
	for _, scenario := range []string{"retry", "retry_stale", "attention", "filter", "generation_limit", "disabled", "retained_old_job", "review_limit"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				ctx := WithEventRecoveryActor(context.Background(), "api_key", "policy-owner")
				run, app := seedResumeRun(t, store, simpleResumeSpec())
				failResumeRun(t, store, run, "send")
				recovery := store.(EventRecoveryStore)
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, ExecutionDeadlineSeconds: 60, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				if did, e := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil || !did {
					t.Fatalf("admit=%v %v", did, e)
				}
				if scenario != "attention" {
					if _, e := store.ClaimNextDueWorkflowRun(ctx); e != nil {
						t.Fatal(e)
					}
					failResumeRun(t, store, run, "send")
				}
				policies := store.(WorkflowRecoveryPolicyStore)
				defaults, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil || defaults.Version != 0 || defaults.Policy.Enabled {
					t.Fatalf("default=%+v %v", defaults, err)
				}
				policy := testRecoveryPreviewPolicy(run.WorkflowName)
				switch scenario {
				case "attention":
					policy.Triggers = []string{"deadline_exceeded"}
				case "filter":
					policy.WorkflowNames = []string{"other"}
				case "generation_limit":
					policy.MaxResumeCount = 1
				case "disabled":
					policy.Enabled = false
				}
				configured, err := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: policy})
				if err != nil || configured.Version != 1 || configured.ActorID != "policy-owner" {
					t.Fatalf("configure=%+v %v", configured, err)
				}
				if _, e := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: policy}); !errors.Is(e, ErrWorkflowRecoveryPolicyConflict) {
					t.Fatalf("stale update=%v", e)
				}
				if _, e := policies.GetWorkflowRecoveryPolicy(ctx, uuid.NewString(), app.ID); !errors.Is(e, ErrNotFound) {
					t.Fatalf("foreign policy=%v", e)
				}
				now := time.Now().UTC().Add(2 * time.Minute)
				if scenario == "retained_old_job" {
					expires := now.Add(-time.Minute)
					if mem, ok := store.(*MemStore); ok {
						mem.mu.Lock()
						mem.eventRecoveryJobs[job.ID].Job.ExpiresAt = expires
						mem.mu.Unlock()
					} else if _, e := store.(*PgStore).pool.Exec(ctx, "UPDATE event_recovery_jobs SET expires_at=$2 WHERE id=$1::uuid", job.ID, expires); e != nil {
						t.Fatal(e)
					}
				}
				if scenario == "review_limit" {
					if mem, ok := store.(*MemStore); ok {
						mem.mu.Lock()
						p := mem.recoveryPolicies[canonicalMemUUID(app.ID)]
						for i := 0; i < api.WorkflowRecoveryPolicyReviewsMax; i++ {
							id := uuid.NewString()
							raw, _ := json.Marshal(api.WorkflowRecoveryPolicyReview{ID: id, JobID: job.ID})
							p.Reviews[id] = raw
						}
						mem.mu.Unlock()
					} else if _, e := store.(*PgStore).pool.Exec(ctx, "INSERT INTO workflow_recovery_policy_reviews(id,app_id,job_id,fingerprint,review,observed_at) SELECT gen_random_uuid(),$1::uuid,$2::uuid,series::text,'{}'::jsonb,$3 FROM generate_series(1,$4) series", app.ID, job.ID, now, api.WorkflowRecoveryPolicyReviewsMax); e != nil {
						t.Fatal(e)
					}
				}

				type result struct {
					did bool
					err error
				}
				results := make(chan result, 2)
				for i := 0; i < 2; i++ {
					go func() { did, e := policies.ProcessNextWorkflowRecoveryPolicy(ctx, now); results <- result{did, e} }()
				}
				worked := 0
				for i := 0; i < 2; i++ {
					r := <-results
					if r.err != nil {
						t.Fatal(r.err)
					}
					if r.did {
						worked++
					}
				}
				expectedWorked := 1
				if scenario == "disabled" {
					expectedWorked = 0
				}
				if worked != expectedWorked {
					t.Fatalf("concurrent workers=%d", worked)
				}

				if scenario == "review_limit" {
					current, e := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
					if e != nil || current.Status != "review_limit" {
						t.Fatalf("quota status=%+v %v", current, e)
					}
					live, e := store.GetWorkflowRun(ctx, run.ID)
					if e != nil || live.ResumeCount != 1 {
						t.Fatalf("quota mutated execution=%+v %v", live, e)
					}
					return
				}
				reviews, err := policies.ListWorkflowRecoveryPolicyReviews(ctx, app.AccountID, app.ID, "", 100)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "filter" || scenario == "disabled" {
					if len(reviews.Reviews) != 0 {
						t.Fatalf("unexpected review=%+v", reviews)
					}
					return
				}
				if len(reviews.Reviews) != 1 {
					t.Fatalf("reviews=%+v", reviews)
				}
				review := reviews.Reviews[0]
				want := "prepared"
				if scenario == "generation_limit" {
					want = "generation_limit"
				}
				if len(review.Decisions) != 1 || review.Decisions[0].Outcome != want || review.PolicyVersion != 1 {
					t.Fatalf("decision=%+v", review)
				}
				live, e := store.GetWorkflowRun(ctx, run.ID)
				if e != nil || live.ResumeCount != 1 || live.CancelledAt != nil {
					t.Fatalf("preview mutated run=%+v %v", live, e)
				}
				if scenario == "generation_limit" {
					if review.Plan != nil {
						t.Fatal("suppression prepared action")
					}
					return
				}
				if review.Plan == nil || review.Plan.JobID != job.ID || len(review.Plan.Actions) != 1 {
					t.Fatalf("plan=%+v", review.Plan)
				}
				action := review.Plan.Actions[0]
				if action.RunID != run.ID {
					t.Fatal("wrong frozen run")
				}
				if action.Cancellation != nil {
					if *action.Cancellation.ExpectedResumeCount != 1 || action.Cancellation.Validate() != nil {
						t.Fatalf("cancel=%+v", action)
					}
				} else if action.Retry == nil || action.Retry.WorkflowRuns.Runs[0].ExpectedResumeCount != 1 || action.Retry.Validate() != nil {
					t.Fatalf("retry=%+v", action)
				}
				if _, e = policies.GetWorkflowRecoveryPolicyReview(ctx, uuid.NewString(), app.ID, review.ID); !errors.Is(e, ErrNotFound) {
					t.Fatalf("foreign review=%v", e)
				}
				// Return values are detached; subsequent reads retain the original plan.
				review.Plan.Actions[0].RunID = uuid.NewString()
				again, e := policies.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if e != nil || again.Plan.Actions[0].RunID != run.ID {
					t.Fatalf("receipt mutated=%+v %v", again, e)
				}
				for i := 1; i <= 3; i++ {
					if _, e = policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(time.Duration(i)*time.Minute)); e != nil {
						t.Fatal(e)
					}
				}
				reviews, e = policies.ListWorkflowRecoveryPolicyReviews(ctx, app.AccountID, app.ID, "", 100)
				if e != nil {
					t.Fatal(e)
				}
				plans := 0
				for _, r := range reviews.Reviews {
					if r.Plan != nil {
						plans++
					}
				}
				if plans != 1 || len(reviews.Reviews) != 2 {
					t.Fatalf("duplicate plans=%d reviews=%+v", plans, reviews)
				}
				if scenario == "retry" || scenario == "attention" {
					assertRecoveryReviewDecision(t, store, ctx, app.AccountID, app.ID, again, now)
				}

				if scenario == "retry_stale" {
					inspector := store.(WorkflowRecoveryReviewInspector)
					before, e := InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, again.ID, now)
					if e != nil || before.Status != "ready" {
						t.Fatalf("before generation change=%+v %v", before, e)
					}
					if _, _, _, e = store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 1)); e != nil {
						t.Fatal(e)
					}
					after, e := InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, again.ID, now)
					if e != nil || after.Status != "stale" || after.Actions[0].Reason != "changed" {
						t.Fatalf("generation change=%+v %v", after, e)
					}
				}
				// Exported retry requests remain valid child recoveries, with no automatic apply.
				if scenario == "retry" {
					child, e := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, *again.Plan.Actions[0].Retry)
					if e != nil || child.Selection.ParentJobID != job.ID {
						t.Fatalf("apply prepared retry=%+v %v", child, e)
					}
					status, e := InspectWorkflowRecoveryReview(ctx, store.(WorkflowRecoveryReviewInspector), app.AccountID, app.ID, again.ID, now)
					if e != nil || status.Status != "applied" {
						t.Fatalf("retry receipt status=%+v %v", status, e)
					}
					if _, e = recovery.ProcessNextEventRecovery(ctx, now.Add(5*time.Minute)); e != nil {
						t.Fatal(e)
					}
					status, e = InspectWorkflowRecoveryReview(ctx, store.(WorkflowRecoveryReviewInspector), app.AccountID, app.ID, again.ID, now)
					if e != nil || status.Status != "applied" {
						t.Fatalf("changed generation lost receipt=%+v %v", status, e)
					}
				}
			})
		})
	}
}
func TestWorkflowRecoveryPolicyBudgets(t *testing.T) {
	p := api.WorkflowRecoveryPolicyResponse{AppID: uuid.NewString(), Version: 1, Policy: testRecoveryPreviewPolicy("recover")}
	p.Policy.HourlyActionBudget = 1
	job := api.EventRecoveryJob{ID: uuid.NewString()}
	now := time.Now().UTC()
	candidates := []recoveryPolicyCandidate{{Decision: api.WorkflowRecoveryPolicyDecision{Position: 1, RunID: uuid.NewString(), ResumeCount: 1, Action: "retry", Outcome: "eligible"}, Action: api.WorkflowRecoveryResponseAction{Kind: "retry", Retry: &api.EventRecoveryRequest{}}}, {Decision: api.WorkflowRecoveryPolicyDecision{Position: 2, RunID: uuid.NewString(), ResumeCount: 1, Action: "retry", Outcome: "eligible"}, Action: api.WorkflowRecoveryResponseAction{Kind: "retry", Retry: &api.EventRecoveryRequest{}}}}
	for _, scenario := range []string{"budget", "cooldown", "dedupe", "resume_limit"} {
		t.Run(scenario, func(t *testing.T) {
			budget := recoveryPolicyBudget{Existing: map[string]bool{}}
			want := "prepared"
			switch scenario {
			case "cooldown":
				budget.Last = now
				want = "cooldown"
			case "dedupe":
				budget.Existing[recoveryPolicyTargetKey(candidates[0].Decision.RunID, 1, "retry")] = true
				want = "already_prepared"
			case "resume_limit":
				p.Policy.MaxResumeCount = 1
				want = "generation_limit"
			}
			review, fingerprint := buildRecoveryPolicyReview(p, "app", job, candidates, budget, now)
			if review.Decisions[0].Outcome != want {
				t.Fatalf("review=%+v", review)
			}
			if scenario == "budget" && (len(review.Plan.Actions) != 1 || review.Decisions[1].Outcome != "hourly_budget") {
				t.Fatalf("unbounded plan=%+v", review)
			}
			_, same := buildRecoveryPolicyReview(p, "app", job, candidates, budget, now.Add(time.Second))
			if same != fingerprint {
				t.Fatal("unchanged decision fingerprint depends on clock")
			}
		})
	}
}

func TestWorkflowRecoveryPolicySlidingBudget(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		first, app := seedResumeRun(t, store, simpleResumeSpec())
		failResumeRun(t, store, first, "send")
		second := &WorkflowRun{AppID: app.ID, WorkflowName: first.WorkflowName, DefinitionSnapshot: first.DefinitionSnapshot, Input: first.Input}
		if err := store.CreateWorkflowRun(ctx, second); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, second.ID, []*WorkflowStep{{StepName: "send", Status: WorkflowStepStatusPending}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, second, "send")
		recovery := store.(EventRecoveryStore)
		job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: first.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: first.ID}, {RunID: second.ID}}}})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < api.AutomationRecoveryBatchMaxRuns; i++ {
			current, e := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if current.PendingCount == 0 {
				if current.QueuedCount != 2 {
					t.Fatalf("admission=%+v", current)
				}
				break
			}
			if did, e := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC().Add(time.Duration(i)*time.Second)); e != nil || !did {
				t.Fatalf("admit=%v %v", did, e)
			}
		}
		for _, run := range []*WorkflowRun{first, second} {
			if _, e := store.ClaimNextDueWorkflowRun(ctx); e != nil {
				t.Fatal(e)
			}
			failResumeRun(t, store, run, "send")
		}
		policies := store.(WorkflowRecoveryPolicyStore)
		policy := testRecoveryPreviewPolicy(first.WorkflowName)
		policy.HourlyActionBudget = 1
		configured, err := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Add(time.Second)
		countPlans := func() int {
			t.Helper()
			reviews, e := policies.ListWorkflowRecoveryPolicyReviews(ctx, app.AccountID, app.ID, "", 100)
			if e != nil {
				t.Fatal(e)
			}
			actions := 0
			for _, r := range reviews.Reviews {
				if r.JobID != job.ID {
					t.Fatal("review selected another parent")
				}
				if r.Plan != nil {
					actions += len(r.Plan.Actions)
				}
			}
			return actions
		}
		for _, offset := range []time.Duration{0, 2 * time.Minute} {
			if _, err = policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(offset)); err != nil {
				t.Fatal(err)
			}
			if countPlans() != 1 {
				t.Fatal("hourly preparation budget exceeded")
			}
		}
		if _, err = policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(time.Hour+time.Minute)); err != nil {
			t.Fatal(err)
		}
		if countPlans() != 2 {
			t.Fatal("sliding budget failed to free capacity")
		}
		// Editing the policy never changes retained snapshots or repeats old generations.
		policy.Reason = "edited policy"
		if _, err = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: configured.Version, Policy: policy}); err != nil {
			t.Fatal(err)
		}
		if _, err = policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if countPlans() != 2 {
			t.Fatal("policy update duplicated prepared targets")
		}
	})
}

func assertRecoveryReviewDecision(t *testing.T, store Store, ctx context.Context, account, app string, review api.WorkflowRecoveryPolicyReview, now time.Time) {
	t.Helper()
	inspector := store.(WorkflowRecoveryReviewInspector)
	status, e := InspectWorkflowRecoveryReview(ctx, inspector, account, app, review.ID, now)
	if e != nil || status.Status != "ready" {
		t.Fatalf("fresh status=%+v %v", status, e)
	}
	req := api.WorkflowRecoveryReviewDecisionRequest{Decision: "approved", Reason: "provider recovered"}
	if review.Plan.Actions[0].Kind == "cancel" {
		req.Decision = "dismissed"
	}
	type result struct {
		r api.WorkflowRecoveryPolicyReview
		e error
	}
	ch := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r, e := inspector.DecideWorkflowRecoveryPolicyReview(ctx, account, app, review.ID, req)
			ch <- result{r, e}
		}()
	}
	var first *api.WorkflowRecoveryReviewDecision
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.e != nil || r.r.ReviewDecision == nil || r.r.ReviewDecision.ActorID != "policy-owner" {
			t.Fatalf("decision=%+v %v", r.r, r.e)
		}
		if first != nil && !first.DecidedAt.Equal(r.r.ReviewDecision.DecidedAt) {
			t.Fatal("retry replaced decision")
		}
		first = r.r.ReviewDecision
	}
	changed := req
	changed.Reason = "different reason"
	if _, e := inspector.DecideWorkflowRecoveryPolicyReview(ctx, account, app, review.ID, changed); !errors.Is(e, ErrWorkflowRecoveryReviewConflict) {
		t.Fatalf("changed decision=%v", e)
	}
	if _, e := inspector.DecideWorkflowRecoveryPolicyReview(ctx, uuid.NewString(), app, review.ID, req); !errors.Is(e, ErrNotFound) {
		t.Fatalf("foreign decision=%v", e)
	}
	saved, e := inspector.GetWorkflowRecoveryPolicyReview(ctx, account, app, review.ID)
	if e != nil || saved.ReviewDecision == nil || saved.ReviewDecision.Reason != req.Reason {
		t.Fatalf("retained decision=%+v %v", saved, e)
	}
	page, e := inspector.ListWorkflowRecoveryPolicyReviews(ctx, account, app, "", 100)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range page.Reviews {
		if r.ID == review.ID && r.ReviewDecision != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("listed decision=%+v", page)
	}
	a := review.Plan.Actions[0]
	live, e := store.GetWorkflowRun(ctx, a.RunID)
	if e != nil || live.ResumeCount != 1 || live.CancelledAt != nil {
		t.Fatalf("decision mutated run=%+v %v", live, e)
	}
	if a.Cancellation != nil {
		if _, e := store.CancelWorkflowRun(ctx, a.RunID, "external cancellation"); e != nil {
			t.Fatal(e)
		}
		status, e = InspectWorkflowRecoveryReview(ctx, inspector, account, app, review.ID, now)
		if e != nil || status.Status != "stale" {
			t.Fatalf("external finish status=%+v %v", status, e)
		}
		if _, e := inspector.WorkflowRecoveryCancellation(ctx, account, review.JobID, *a.Cancellation, false); e != nil {
			t.Fatal(e)
		}
		status, e = InspectWorkflowRecoveryReview(ctx, inspector, account, app, review.ID, now)
		if e != nil || status.Status != "applied" {
			t.Fatalf("retained cancellation status=%+v %v", status, e)
		}
	}
}
