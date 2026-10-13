package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestWorkflowRecoveryPolicyEvaluationEligibility(t *testing.T) {
	base := testAutomaticRecoveryPolicy("orders")
	review := recoveryEvaluationReview{workflow: "orders", review: api.WorkflowRecoveryPolicyReview{Policy: base}}
	decision := api.WorkflowRecoveryPolicyDecision{RunID: uuid.NewString(), ResumeCount: 1, Trigger: "saved_failure", Action: "retry", Outcome: "cooldown"}
	for _, tc := range []struct {
		name, eligibility, reason string
		change                    func(*api.WorkflowRecoveryPolicy, *recoveryEvaluationReview, *api.WorkflowRecoveryPolicyDecision)
	}{
		{"same filters", "qualifies", "retained_eligibility_evidence", func(*api.WorkflowRecoveryPolicy, *recoveryEvaluationReview, *api.WorkflowRecoveryPolicyDecision) {}},
		{"disabled", "excluded", "policy_disabled", func(p *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			p.Enabled = false
		}},
		{"workflow", "excluded", "workflow_filter", func(p *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			p.WorkflowNames = []string{"other"}
		}},
		{"generation", "excluded", "generation_limit", func(p *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			p.MaxResumeCount = 1
		}},
		{"ambiguous status", "unknown", "failure_status_ambiguous", func(p *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			p.FailureStatuses = []string{"failed"}
		}},
		{"disjoint status", "excluded", "failure_status_filter", func(p *api.WorkflowRecoveryPolicy, r *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			p.FailureStatuses = []string{"failed"}
			r.review.Policy.FailureStatuses = []string{"dead"}
		}},
		{"missing status", "unknown", "failure_status_not_recorded", func(_ *api.WorkflowRecoveryPolicy, r *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			r.review.Policy.FailureStatuses = nil
		}},
		{"widen status", "qualifies", "retained_eligibility_evidence", func(_ *api.WorkflowRecoveryPolicy, r *recoveryEvaluationReview, _ *api.WorkflowRecoveryPolicyDecision) {
			r.review.Policy.FailureStatuses = []string{"failed"}
		}},
		{"unknown preflight", "unknown", "historical_preflight_unproven", func(_ *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, d *api.WorkflowRecoveryPolicyDecision) {
			d.Outcome = "unknown"
		}},
		{"trigger", "excluded", "trigger_filter", func(_ *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, d *api.WorkflowRecoveryPolicyDecision) {
			d.Trigger = "stalled"
			d.Action = "cancel"
		}},
		{"unsupported trigger", "unknown", "unsupported_historical_trigger", func(p *api.WorkflowRecoveryPolicy, _ *recoveryEvaluationReview, d *api.WorkflowRecoveryPolicyDecision) {
			p.Triggers = []string{"stalled"}
			d.Trigger = "stalled"
			d.Action = "cancel"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := recoveryPolicyClone(base)
			r := recoveryPolicyClone(review.review)
			sample := recoveryEvaluationReview{workflow: review.workflow, review: r}
			d := decision
			tc.change(&p, &sample, &d)
			got := recoveryEvaluationEligibility(p, sample, d)
			if got.Eligibility != tc.eligibility || got.Reason != tc.reason {
				t.Fatalf("assessment %+v", got)
			}
		})
	}
}
func TestWorkflowRecoveryPolicyEvaluationReplay(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	p := testAutomaticRecoveryPolicy("orders")
	p.CooldownSeconds = 60
	p.HourlyActionBudget = 2
	p.HourlyExecutionBudget = 2
	makeReview := func(offset time.Duration, runs ...string) recoveryEvaluationReview {
		r := api.WorkflowRecoveryPolicyReview{ID: uuid.NewString(), ObservedAt: at.Add(offset), Policy: p, PolicyVersion: 3}
		for i, id := range runs {
			r.Decisions = append(r.Decisions, api.WorkflowRecoveryPolicyDecision{RunID: id, ResumeCount: 1, Action: "retry", Trigger: "saved_failure", Outcome: "prepared", Position: int64(i + 1)})
		}
		return recoveryEvaluationReview{workflow: "orders", review: r}
	}
	a, b, c, d := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	reviews := []recoveryEvaluationReview{makeReview(time.Hour, a, d), makeReview(0, a, b, c), makeReview(30*time.Second, c), makeReview(60*time.Second, c)}
	executions := []api.WorkflowRecoveryHistoryEntry{{ID: "audit:1", ActionID: uuid.NewString(), OccurredAt: at.Add(-time.Second)}, {ID: "audit:2", ActionID: uuid.NewString(), OccurredAt: at.Add(-2 * time.Second)}}
	// Duplicate application proof must not manufacture additional pressure.
	executions = append(executions, executions[0])
	current := api.WorkflowRecoveryPolicyResponse{Policy: p, Version: 3}
	proposed := recoveryPolicyClone(p)
	proposed.MaxResumeCount = 1
	got, err := evaluateRecoveryPolicy(context.Background(), recoveryHistoryCursor{ObservedAt: time.Now(), Start: at, End: at.Add(time.Hour)}, current, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: proposed}, reviews, executions)
	if err != nil || got.CurrentTotals.WouldPrepare != 3 || got.CurrentTotals.PreparationBudget != 2 || got.CurrentTotals.Cooldown != 1 || got.CurrentTotals.Deduplicated != 1 || got.ProposedTotals.Excluded != 7 || got.ObservedExecutions != 2 {
		t.Fatalf("replay %+v %v", got, err)
	}
	if got.Candidates[0].Current.ExecutionPressure != "observed_hourly_budget" || got.Candidates[0].ObservedHourlyExecutions != 2 || len(got.Candidates[0].ExecutionEvidenceIDs) != 2 {
		t.Fatalf("execution proof %+v", got.Candidates[0])
	}
	if got.Candidates[len(got.Candidates)-1].Current.ExecutionPressure != "insufficient_history" {
		t.Fatal("missing execution history treated as admission proof")
	}
	if !reflect.DeepEqual(reviews[0].review.Policy, p) {
		t.Fatal("mutated historical policy")
	}
}
func TestWorkflowRecoveryPolicyEvaluationBounds(t *testing.T) {
	p := testAutomaticRecoveryPolicy("orders")
	reviews := make([]recoveryEvaluationReview, api.WorkflowRecoveryPolicyEvaluationReviewsMax+1)
	for i := range reviews {
		reviews[i] = recoveryEvaluationReview{workflow: "orders", review: api.WorkflowRecoveryPolicyReview{ID: uuid.NewString(), ObservedAt: time.Now(), Policy: p}}
	}
	got, err := evaluateRecoveryPolicy(context.Background(), recoveryHistoryCursor{}, api.WorkflowRecoveryPolicyResponse{Policy: p}, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: p}, reviews, nil)
	if err != nil || !got.ReviewsTruncated || got.InspectedReviews != api.WorkflowRecoveryPolicyEvaluationReviewsMax {
		t.Fatalf("review bound %+v %v", got, err)
	}
	review := reviews[0]
	review.review.Decisions = make([]api.WorkflowRecoveryPolicyDecision, api.WorkflowRecoveryPolicyEvaluationCandidatesMax+1)
	got, err = evaluateRecoveryPolicy(context.Background(), recoveryHistoryCursor{}, api.WorkflowRecoveryPolicyResponse{Policy: p}, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: p}, []recoveryEvaluationReview{review}, nil)
	if err != nil || !got.CandidatesTruncated || len(got.Candidates) != api.WorkflowRecoveryPolicyEvaluationCandidatesMax {
		t.Fatalf("candidate bound %+v %v", got, err)
	}
	executions := make([]api.WorkflowRecoveryHistoryEntry, api.WorkflowRecoveryPolicyEvaluationExecutionsMax+1)
	got, err = evaluateRecoveryPolicy(context.Background(), recoveryHistoryCursor{}, api.WorkflowRecoveryPolicyResponse{Policy: p}, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: p}, nil, executions)
	if err != nil || !got.ExecutionsTruncated {
		t.Fatalf("execution bound %+v %v", got, err)
	}
}
func TestWorkflowRecoveryPolicyEvaluationStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		review, app, run, _ := seedAutomaticRecoveryReview(t, s, nil)
		clearEvaluationFailureInventory(t, s, app.ID)
		review.ObservedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		for i := range review.Decisions {
			review.Decisions[i].FailureStatus = ""
			review.Decisions[i].PreflightOutcome = ""
			review.Decisions[i].PreflightBlockers = nil
		}
		raw, _ := json.Marshal(review)
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			m.recoveryPolicies[canonicalMemUUID(app.ID)].Reviews[review.ID] = raw
			m.mu.Unlock()
		} else {
			if _, err := s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_policy_reviews SET review=$1,observed_at=$2 WHERE id=$3", raw, review.ObservedAt, review.ID); err != nil {
				t.Fatal(err)
			}
		}
		store := s.(WorkflowRecoveryPolicyEvaluationStore)
		req := api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: review.PolicyVersion, Policy: review.Policy}
		req.Policy.FailureStatuses = []string{"failed"}
		before := policyChangeEvidence(t, s, app.ID)
		out, err := store.EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req)
		if err != nil || len(out.Candidates) != 1 || out.CurrentTotals.WouldPrepare != 1 || out.ProposedTotals.Unknown != 1 || out.Candidates[0].Proposed.Reason != "failure_status_ambiguous" {
			t.Fatalf("evaluate %+v %v", out, err)
		}
		if string(before) != string(policyChangeEvidence(t, s, app.ID)) {
			t.Fatal("evaluation mutated source state")
		}
		req.Workflow = "absent"
		out, err = store.EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req)
		if err != nil || len(out.Candidates) != 0 || len(out.CoverageNotes) < 6 {
			t.Fatalf("empty %+v %v", out, err)
		}
		req.Workflow = run.WorkflowName
		req.ExpectedVersion++
		if _, err = store.EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
			t.Fatalf("stale version %v", err)
		}
		req.ExpectedVersion = review.PolicyVersion
		if _, err = store.EvaluateWorkflowRecoveryPolicy(ctx, uuid.NewString(), app.ID, req); !errors.Is(err, ErrNotFound) {
			t.Fatalf("scope %v", err)
		}
		req.CreatedBefore = time.Now().Add(time.Hour)
		if _, err = store.EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrEventRecoveryQuery) {
			t.Fatalf("future window %v", err)
		}
		req.CreatedBefore = time.Time{}
		req.Policy.MaxResumeCount = 0
		if _, err = store.EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrEventRecoveryQuery) {
			t.Fatalf("invalid policy %v", err)
		}
		req.Policy = review.Policy
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = store.EvaluateWorkflowRecoveryPolicy(cancelled, app.AccountID, app.ID, req); err == nil {
			t.Fatal("cancelled evaluation succeeded")
		}
	})
}

func evaluationFixtureReview(t *testing.T, s Store, r api.WorkflowRecoveryPolicyReview) {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		m.recoveryPolicies[canonicalMemUUID(r.AppID)].Reviews[r.ID] = raw
		m.mu.Unlock()
		return
	}
	if _, err = s.(*PgStore).pool.Exec(context.Background(), "UPDATE workflow_recovery_policy_reviews SET review=$1,observed_at=$2 WHERE id=$3", raw, r.ObservedAt, r.ID); err != nil {
		t.Fatal(err)
	}
}
func evaluationFixtureApplication(t *testing.T, s Store, app string, version int64, at time.Time) {
	t.Helper()
	e := api.WorkflowRecoveryHistoryEntry{OccurredAt: at, Kind: "retry_transition", EvaluationReason: "child_created", Reason: "child_created", Outcome: "applied", ActionID: uuid.NewString(), ChildJobID: uuid.NewString(), PolicyVersion: version, Workflow: "another-workflow"}
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		m.recoveryPolicies[canonicalMemUUID(app)].appendHistoryEvent(e, time.Now().UTC())
		m.mu.Unlock()
		return
	}
	tx, err := s.(*PgStore).pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = appendRecoveryHistoryEvent(context.Background(), sqlc.New(), tx, app, e); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestWorkflowRecoveryPolicyEvaluationApplicationEvidence(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		review, app, run, _ := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) { p.CooldownSeconds = 7200 })
		clearEvaluationFailureInventory(t, s, app.ID)
		review.ObservedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		evaluationFixtureReview(t, s, review)
		evaluationFixtureApplication(t, s, app.ID, review.PolicyVersion, review.ObservedAt.Add(-90*time.Minute))
		// Equal timestamps cannot establish that an application preceded preparation.
		evaluationFixtureApplication(t, s, app.ID, review.PolicyVersion, review.ObservedAt)
		req := api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: review.PolicyVersion, Policy: recoveryPolicyClone(review.Policy), Workflow: run.WorkflowName, CreatedAfter: review.ObservedAt.Add(-time.Second)}
		req.Policy.CooldownSeconds = 60
		req.Policy.HourlyExecutionBudget = 1
		store := s.(WorkflowRecoveryPolicyEvaluationStore)
		before := durableRecoveryEvents(t, s, app.ID)
		out, err := store.EvaluateWorkflowRecoveryPolicy(context.Background(), app.AccountID, app.ID, req)
		if err != nil || len(out.Candidates) != 1 {
			t.Fatalf("evidence %+v %v", out, err)
		}
		e := out.Candidates[0]
		if e.ObservedHourlyExecutions != 0 || e.Current.ExecutionPressure != "observed_cooldown" || e.Proposed.ExecutionPressure != "insufficient_history" || len(e.ExecutionEvidenceIDs) != 1 || e.LastObservedExecutionAt == nil || !e.LastObservedExecutionAt.Equal(review.ObservedAt.Add(-90*time.Minute)) {
			t.Fatalf("warmup/equal time %+v", e)
		}
		if !reflect.DeepEqual(before, durableRecoveryEvents(t, s, app.ID)) {
			t.Fatal("evaluation appended or pruned audit events")
		}
		evaluationFixtureApplication(t, s, app.ID, review.PolicyVersion, review.ObservedAt.Add(-time.Second))
		out, err = store.EvaluateWorkflowRecoveryPolicy(context.Background(), app.AccountID, app.ID, req)
		if err != nil || out.Candidates[0].ObservedHourlyExecutions != 1 || out.ProposedTotals.ExecutionBudgetPressure != 1 {
			t.Fatalf("app-wide execution pressure %+v %v", out, err)
		}
	})
}
func TestWorkflowRecoveryPolicyEvaluationReviewSample(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		review, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		clearEvaluationFailureInventory(t, s, app.ID)
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
		for i := 0; i < api.WorkflowRecoveryPolicyEvaluationReviewsMax+1; i++ {
			r := recoveryPolicyClone(review)
			r.ID = uuid.NewString()
			r.ObservedAt = at.Add(time.Duration(i) * time.Second)
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if m, ok := s.(*MemStore); ok {
				m.mu.Lock()
				m.recoveryPolicies[canonicalMemUUID(app.ID)].Reviews[r.ID] = raw
				m.mu.Unlock()
			} else {
				_, err := sqlc.New().WorkflowRecoveryPolicyReviewInsert(context.Background(), s.(*PgStore).pool, sqlc.WorkflowRecoveryPolicyReviewInsertParams{ID: mustPgUUID(r.ID), AppID: mustPgUUID(app.ID), JobID: mustPgUUID(r.JobID), Fingerprint: fmt.Sprintf("%064x", i+1), Review: raw, NowAt: pgtypeFromTime(r.ObservedAt)})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(context.Background(), app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: review.PolicyVersion, Policy: review.Policy})
		if err != nil || !out.ReviewsTruncated || out.CandidatesTruncated || out.InspectedReviews != api.WorkflowRecoveryPolicyEvaluationReviewsMax || len(out.Candidates) != api.WorkflowRecoveryPolicyEvaluationReviewsMax || !out.Candidates[0].ObservedAt.Equal(at) {
			t.Fatalf("historical review sample %+v %v", out, err)
		}
	})
}

func TestWorkflowRecoveryPolicyEvaluationExactEvidence(t *testing.T) {
	p := testAutomaticRecoveryPolicy("orders")
	p.FailureStatuses = []string{"failed"}
	r := recoveryEvaluationReview{workflow: "orders", review: api.WorkflowRecoveryPolicyReview{Policy: testAutomaticRecoveryPolicy("orders")}}
	d := api.WorkflowRecoveryPolicyDecision{RunID: uuid.NewString(), ResumeCount: 1, Trigger: "saved_failure", Action: "retry", Outcome: "cooldown", FailureStatus: "failed", PreflightOutcome: "eligible"}
	if got := recoveryEvaluationEligibility(p, r, d); got.Eligibility != "qualifies" {
		t.Fatalf("exact status: %+v", got)
	}
	d.FailureStatus = "dead"
	if got := recoveryEvaluationEligibility(p, r, d); got.Reason != "failure_status_filter" {
		t.Fatalf("disjoint exact status: %+v", got)
	}
	d.FailureStatus = "failed"
	d.PreflightOutcome = "outstanding_retry"
	if got := recoveryEvaluationEligibility(p, r, d); got.Reason != "historical_preflight_outstanding_retry" {
		t.Fatalf("frozen refusal: %+v", got)
	}
}

func TestWorkflowRecoveryCandidateEvidenceNoPlan(t *testing.T) {
	r := api.WorkflowRecoveryPolicyReview{ID: uuid.NewString(), AppID: uuid.NewString(), JobID: uuid.NewString(), PolicyVersion: 1, Policy: testAutomaticRecoveryPolicy("orders"), ObservedAt: time.Now(), Decisions: []api.WorkflowRecoveryPolicyDecision{{FailureStatus: "failed", PreflightOutcome: "eligible"}}, Plan: &api.WorkflowRecoveryResponsePlan{Key: "secret-plan"}}
	e := recoveryCandidateHistoryEvent(r, "orders")
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-plan") {
		t.Fatal("plan leaked into candidate evidence")
	}
	r.Decisions[0].FailureStatus = "dead"
	if e.CandidateEvidence.Decisions[0].FailureStatus != "failed" {
		t.Fatal("snapshot aliases source")
	}
	decoded := recoveryCandidateEvidenceReview(e)
	if decoded.review.ID != r.ID || decoded.workflow != "orders" || decoded.review.Plan != nil {
		t.Fatalf("association lost: %+v", decoded)
	}
}
