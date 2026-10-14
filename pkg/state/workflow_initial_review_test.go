package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func seedInitialReview(t *testing.T, s Store) (*WorkflowRun, App, string, api.WorkflowRecoveryPolicyResponse) {
	t.Helper()
	return seedInitialReviewWithContext(t, context.Background(), s)
}

func seedInitialReviewWithContext(t *testing.T, ctx context.Context, s Store) (*WorkflowRun, App, string, api.WorkflowRecoveryPolicyResponse) {
	t.Helper()
	run, app := seedResumeRunWithContext(t, ctx, s, simpleResumeSpec())
	failResumeRunWithContext(t, ctx, s, run, "send")
	page, err := s.(WorkflowInitialRecoveryStore).ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, api.WorkflowInitialFailureOptions{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("failures %+v %v", page, err)
	}
	policy := api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{run.WorkflowName}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"failed", "dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "review incident"}
	p, err := s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	return run, app, page.Items[0].ID, p
}
func mutateInitialReview(t *testing.T, s Store, id string, mutate func(*api.WorkflowInitialRecoveryReview)) {
	t.Helper()
	mutateInitialReviewWithContext(t, context.Background(), s, id, mutate)
}

func mutateInitialReviewWithContext(t *testing.T, ctx context.Context, s Store, id string, mutate func(*api.WorkflowInitialRecoveryReview)) {
	t.Helper()
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		defer m.mu.Unlock()
		r := m.initialRecoveryReviews[id]
		mutate(&r)
		m.initialRecoveryReviews[id] = r
		return
	}
	pg := s.(*PgStore)
	var raw []byte
	if err := pg.pool.QueryRow(ctx, "SELECT review FROM workflow_initial_recovery_reviews WHERE id=$1::uuid", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	r, err := decodeInitialReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&r)
	raw, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pg.pool.Exec(ctx, "UPDATE workflow_initial_recovery_reviews SET review=$2::jsonb,prepared_at=$3,expires_at=$4,submitted_at=$5 WHERE id=$1::uuid", id, raw, r.PreparedAt, r.ExpiresAt, r.SubmittedAt); err != nil {
		t.Fatal(err)
	}
}
func TestWorkflowInitialReviewLifecycle(t *testing.T) {
	for _, scenario := range []string{"admitted", "cancelled", "policy_changed", "expired", "evidence_removed", "manual_pending", "paused_manual_pending", "policy_changed_after_submit", "expired_after_submit", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := WithEventRecoveryActor(context.Background(), "api_key", "reviewer")
				run, app, failure, p := seedInitialReview(t, s)
				hook, err := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://review.example.test/ready", SecretSealed: []byte("test-sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryReviewReady)}})
				if err != nil {
					t.Fatal(err)
				}
				reviews := s.(WorkflowInitialRecoveryReviewStore)
				jobs := s.(EventRecoveryStore)
				request := api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version}
				review, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, request)
				if err != nil || review.Status != "prepared" || len(review.Events) != 1 || review.Events[0].ActorID != "reviewer" || review.Notification == nil || review.Notification.Status != "queued" {
					t.Fatalf("prepare %+v %v", review, err)
				}
				again, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, request)
				if err != nil || again.ID != review.ID || len(again.Events) != 1 {
					t.Fatalf("idempotency %+v %v", again, err)
				}
				page, err := reviews.ListWorkflowInitialRecoveryReviews(ctx, app.AccountID, app.ID, "", 1)
				if err != nil || len(page.Reviews) != 1 || page.Reviews[0].ID != review.ID {
					t.Fatalf("list %+v %v", page, err)
				}
				payload, _ := json.Marshal(initialReviewReadyPayload(review, review.Notification.CapturedAt))
				delivery := AppWebhookDelivery{AccountID: app.AccountID, AppID: app.ID, WebhookID: hook.ID, Event: AppWebhookEventRecoveryReviewReady, Payload: payload}
				allowed, reason, err := s.(WorkflowRecoveryReviewNotificationStore).WorkflowRecoveryReviewNotificationAllowed(ctx, delivery, time.Now().UTC())
				if err != nil || !allowed {
					t.Fatalf("notification %v %s %v", allowed, reason, err)
				}
				if _, err = reviews.GetWorkflowInitialRecoveryReview(ctx, uuid.NewString(), app.ID, review.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("cross-account %v", err)
				}
				if scenario == "cancelled" {
					out, err := reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "cancel", Reason: "operator cancelled"})
					if err != nil || out.Status != "cancelled" || out.JobID != "" || len(out.Events) != 2 {
						t.Fatalf("cancel %+v %v", out, err)
					}
					if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "too late"}); !errors.Is(err, ErrWorkflowInitialReviewBlocked) {
						t.Fatalf("submit cancelled %v", err)
					}
					allowed, _, err = s.(WorkflowRecoveryReviewNotificationStore).WorkflowRecoveryReviewNotificationAllowed(ctx, delivery, time.Now().UTC())
					if err != nil || allowed {
						t.Fatalf("cancelled notification %v %v", allowed, err)
					}
					return
				}
				if scenario == "policy_changed" {
					p.Policy.Enabled = false
					if _, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "expired" {
					mutateInitialReview(t, s, review.ID, func(r *api.WorkflowInitialRecoveryReview) {
						r.PreparedAt = time.Now().UTC().Add(-2 * api.WorkflowInitialRecoveryReviewLifetime).Truncate(time.Microsecond)
						r.ExpiresAt = r.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
					})
				}
				if scenario == "evidence_removed" {
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.workflowFailureEvidence, canonicalMemUUID(app.ID))
						m.mu.Unlock()
					} else if _, err = s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_recovery_failure_evidence WHERE id=$1::uuid", failure); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "manual_pending" || scenario == "paused_manual_pending" {
					manual, err := jobs.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, FailureEvidenceID: failure}}}})
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "paused_manual_pending" {
						if _, err = jobs.PauseEventRecovery(ctx, app.AccountID, manual.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				submit := api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "reviewed initial failure"}
				if scenario == "concurrent" {
					var wg sync.WaitGroup
					outs := make(chan api.WorkflowInitialRecoveryReview, 2)
					errs := make(chan error, 2)
					for i := 0; i < 2; i++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							r, e := reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, submit)
							outs <- r
							errs <- e
						}()
					}
					wg.Wait()
					close(outs)
					close(errs)
					for e := range errs {
						if e != nil {
							t.Fatal(e)
						}
					}
					var job string
					for r := range outs {
						if job != "" && r.JobID != job {
							t.Fatal("duplicate jobs")
						}
						job = r.JobID
					}
					if job == "" {
						t.Fatal("missing job")
					}
					return
				}
				out, err := reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, submit)
				if scenario == "manual_pending" || scenario == "paused_manual_pending" {
					if !errors.Is(err, ErrWorkflowInitialReviewBlocked) {
						t.Fatalf("manual race %v", err)
					}
					return
				}
				if scenario == "policy_changed" || scenario == "expired" || scenario == "evidence_removed" {
					if err != nil || out.JobID != "" || len(out.Events) != 2 || out.Status != "stale" && out.Status != "expired" {
						t.Fatalf("stale submit %+v %v", out, err)
					}
					current, err := s.GetWorkflowRun(ctx, run.ID)
					if err != nil || current.ResumeCount != 0 {
						t.Fatalf("stale admitted %+v %v", current, err)
					}
					allowed, _, err = s.(WorkflowRecoveryReviewNotificationStore).WorkflowRecoveryReviewNotificationAllowed(ctx, delivery, time.Now().UTC())
					if err != nil || allowed {
						t.Fatalf("stale notification %v %v", allowed, err)
					}
					return
				}
				if err != nil || out.Status != "submitted" || out.JobID == "" || len(out.Events) != 2 {
					t.Fatalf("submit %+v %v", out, err)
				}
				again, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, submit)
				if err != nil || again.JobID != out.JobID || len(again.Events) != 2 {
					t.Fatalf("repeat %+v %v", again, err)
				}
				if _, err = jobs.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); !errors.Is(err, ErrWorkflowInitialReviewBlocked) {
					t.Fatalf("duplicate manual job %v", err)
				}
				if scenario == "policy_changed_after_submit" {
					p.Policy.Enabled = false
					if _, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "expired_after_submit" {
					mutateInitialReview(t, s, review.ID, func(r *api.WorkflowInitialRecoveryReview) {
						r.PreparedAt = time.Now().UTC().Add(-2 * api.WorkflowInitialRecoveryReviewLifetime).Truncate(time.Microsecond)
						r.ExpiresAt = r.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
					})
				}
				if scenario != "admitted" {
					preflight, err := s.(EventRecoveryPreflightStore).GetEventRecoveryPreflight(ctx, app.AccountID, out.JobID, time.Now().UTC())
					if err != nil || preflight.LikelySkippedCount != 1 {
						t.Fatalf("authority preflight %+v %v", preflight, err)
					}
				}
				did, err := jobs.ProcessNextEventRecovery(ctx, time.Now().UTC())
				if err != nil || !did {
					t.Fatalf("process %v %v", did, err)
				}
				items, err := jobs.ListEventRecoveryItems(ctx, app.AccountID, out.JobID, 0, 25)
				if err != nil || len(items.Items) != 1 {
					t.Fatalf("items %+v %v", items, err)
				}
				want := "queued"
				if scenario != "admitted" {
					want = "skipped"
				}
				if items.Items[0].State != want {
					t.Fatalf("admission %+v want %s", items.Items[0], want)
				}
			})
		})
	}
}
func TestWorkflowInitialReviewBounds(t *testing.T) {
	now := time.Now().UTC()
	p := api.WorkflowRecoveryPolicy{HourlyActionBudget: 1, CooldownSeconds: 60}
	for _, tc := range []struct {
		count int
		last  time.Time
		want  string
	}{{0, time.Time{}, ""}, {1, time.Time{}, "hourly_budget"}, {0, now, "cooldown"}, {0, now.Add(-time.Minute), ""}} {
		if got := initialReviewBudgetReason(tc.count, tc.last, p, now); got != tc.want {
			t.Fatalf("budget got %s want %s", got, tc.want)
		}
	}
}
func seedSecondInitialFailure(t *testing.T, s Store, app App, first *WorkflowRun) string {
	t.Helper()
	ctx := context.Background()
	run := &WorkflowRun{AppID: app.ID, WorkflowName: first.WorkflowName, DefinitionSnapshot: first.DefinitionSnapshot, Input: first.Input}
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
	page, err := s.(WorkflowInitialRecoveryStore).ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, api.WorkflowInitialFailureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Items {
		if e.RunID == run.ID {
			return e.ID
		}
	}
	t.Fatal("second failure missing")
	return ""
}
func TestWorkflowInitialReviewBudgetLifecycle(t *testing.T) {
	for _, scenario := range []string{"prepare_cooldown", "prepare_budget", "submit_cooldown", "submit_budget"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				run, app, failure, p := seedInitialReview(t, s)
				if scenario == "prepare_budget" || scenario == "submit_budget" {
					p.Policy.HourlyActionBudget = 1
					var err error
					p, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
					if err != nil {
						t.Fatal(err)
					}
				}
				secondFailure := seedSecondInitialFailure(t, s, app, run)
				reviews := s.(WorkflowInitialRecoveryReviewStore)
				req := api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version}
				first, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, req)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "prepare_budget" || scenario == "prepare_cooldown" {
					if _, err = reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, secondFailure, req); !errors.Is(err, ErrWorkflowInitialReviewBlocked) {
						t.Fatalf("preparation budget bypassed %v", err)
					}
					return
				}
				mutateInitialReview(t, s, first.ID, func(r *api.WorkflowInitialRecoveryReview) {
					r.PreparedAt = time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
					r.ExpiresAt = r.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
				})
				second, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, secondFailure, req)
				if err != nil {
					t.Fatal(err)
				}
				decision := api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "reviewed"}
				if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, first.ID, decision); err != nil {
					t.Fatal(err)
				}
				if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, second.ID, decision); !errors.Is(err, ErrWorkflowInitialReviewBlocked) {
					t.Fatalf("submission budget bypassed %v", err)
				}
				observed, err := reviews.GetWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, second.ID)
				if err != nil || observed.JobID != "" || len(observed.Events) != 1 {
					t.Fatalf("blocked submission mutated %+v %v", observed, err)
				}
			})
		})
	}
}
func TestWorkflowInitialReviewManualRace(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app, failure, p := seedInitialReview(t, s)
		reviews := s.(WorkflowInitialRecoveryReviewStore)
		r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "reviewed"})
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
			results <- err
		}()
		close(start)
		wg.Wait()
		close(results)
		success, blocked := 0, 0
		for err := range results {
			if err == nil {
				success++
			} else if errors.Is(err, ErrWorkflowInitialReviewBlocked) {
				blocked++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || blocked != 1 {
			t.Fatalf("race success %d blocked %d", success, blocked)
		}
	})
}

// A parent-review worker may hold policy authority while its FK check acquires
// KEY SHARE on this job. Native claims must allow that FK check to finish before
// waiting for policy authority, while still excluding another native claim.
func TestWorkflowInitialReviewAdmissionLockOrder(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			return
		}
		base := context.Background()
		_, app, failure, p := seedInitialReview(t, s)
		reviews := s.(WorkflowInitialRecoveryReviewStore)
		r, err := reviews.PrepareWorkflowInitialRecoveryReview(base, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		r, err = reviews.DecideWorkflowInitialRecoveryReview(base, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "reviewed"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(base, 5*time.Second)
		defer cancel()
		jobTx, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = jobTx.Rollback(base) }()
		q := sqlc.New()
		job, err := q.EventRecoveryNextJob(ctx, jobTx, pgtypeFromTime(time.Now().UTC()))
		if err != nil || uuidString(job.ID) != r.JobID {
			t.Fatalf("claim %s %v", uuidString(job.ID), err)
		}
		policyTx, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = policyTx.Rollback(base) }()
		if _, err = q.EventRecoveryNextJob(ctx, policyTx, pgtypeFromTime(time.Now().UTC())); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second native claim %v", err)
		}
		if _, err = policyTx.Exec(ctx, "SELECT app_id FROM workflow_recovery_policies WHERE app_id=$1::uuid FOR UPDATE", app.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = policyTx.Exec(ctx, "INSERT INTO workflow_recovery_policy_reviews(id,app_id,job_id,fingerprint,review,observed_at) VALUES($1::uuid,$2::uuid,$3::uuid,'lock-order','{}'::jsonb,now())", uuid.NewString(), app.ID, r.JobID); err != nil {
			t.Fatalf("parent review FK capture blocked behind native claim: %v", err)
		}
		if err = policyTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if reason, err := initialReviewJobAdmissionTx(ctx, q, jobTx, r.JobID, time.Now().UTC()); err != nil || reason != "" {
			t.Fatalf("admission authority %s %v", reason, err)
		}
	})
}
