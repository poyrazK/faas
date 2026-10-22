package state

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func observeFailureBatch(t *testing.T, s Store, now time.Time) int {
	t.Helper()
	n := 0
	for n < api.WorkflowRecoveryFailureObserverBatch {
		worked, err := s.(WorkflowFailureEligibilityStore).ObserveNextWorkflowFailureEligibility(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
		n++
	}
	return n
}
func TestWorkflowFailureEligibilityStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		failResumeRun(t, s, run, "send")
		before := failureInventoryFacts(t, s, app.ID)
		now := time.Now().UTC().Truncate(time.Microsecond)
		if n := observeFailureBatch(t, s, now); n != 1 {
			t.Fatalf("checked %d", n)
		}
		facts := failureInventoryFacts(t, s, app.ID)
		if len(facts) != 1 || facts[0].Eligibility == nil || facts[0].Eligibility.Reason != "parent_not_retained" || facts[0].Eligibility.ObservedAt.Before(facts[0].ObservedAt) {
			t.Fatalf("no-parent checks: %+v", facts)
		}
		snapshot := facts[0].Eligibility
		facts[0].Eligibility = nil
		if !reflect.DeepEqual(facts, before) {
			t.Fatal("observer modified original terminal facts")
		}
		facts[0].Eligibility = snapshot
		if n := observeFailureBatch(t, s, now.Add(time.Minute)); n != 0 {
			t.Fatalf("refreshed too early %d", n)
		}
		proposed := testAutomaticRecoveryPolicy(run.WorkflowName)
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: proposed})
		if err != nil || len(out.Candidates) != 1 || out.Candidates[0].LaterEligibility == nil || out.Candidates[0].Proposed.Eligibility != "unknown" || out.ProposedTotals.Unknown != 1 || out.Candidates[0].LaterEligibility.Proposed.Reason != "later_preflight_parent_not_retained" {
			t.Fatalf("separate later checks: %+v %v", out, err)
		}
		policy, err := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil || policy.Version != 0 {
			t.Fatalf("observer created policy %+v %v", policy, err)
		}
		if _, _, _, err = s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
			t.Fatal(err)
		}
		if n := observeFailureBatch(t, s, now.Add(api.WorkflowRecoveryFailureObserverRefresh)); n != 1 {
			t.Fatalf("refresh %d", n)
		}
		facts = failureInventoryFacts(t, s, app.ID)
		if facts[0].Eligibility.Reason != "changed" {
			t.Fatalf("generation fence %+v", facts[0].Eligibility)
		}
		if err = s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SweepExpiredWorkflowRuns(ctx, 0); err != nil {
			t.Fatal(err)
		}
		if n := observeFailureBatch(t, s, now.Add(2*api.WorkflowRecoveryFailureObserverRefresh)); n != 1 {
			t.Fatalf("pruned refresh %d", n)
		}
		if got := failureInventoryFacts(t, s, app.ID)[0].Eligibility.Reason; got != "run_not_retained" {
			t.Fatalf("pruned reason %s", got)
		}
	})
}
func TestWorkflowFailureEligibilitySavedParent(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		review, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		// Eligibility is the observer's output; all other durable evidence must stay unchanged.
		before := policyChangeEvidenceSnapshot(t, s, app.ID, false)
		now := time.Now().UTC().Truncate(time.Microsecond)
		if n := observeFailureBatch(t, s, now); n != 2 {
			t.Fatalf("checked %d", n)
		}
		facts := failureInventoryFacts(t, s, app.ID)
		if len(facts) != 2 || facts[1].Eligibility == nil || facts[1].Eligibility.Reason != "eligible" || facts[1].Eligibility.ParentJobID != review.JobID || facts[1].Eligibility.ParentPosition != review.Decisions[0].Position {
			t.Fatalf("saved parent %+v", facts)
		}
		if string(before) != string(policyChangeEvidenceSnapshot(t, s, app.ID, false)) {
			t.Fatal("observer mutated policy/review/job/outbox")
		}
		// Pure assessment uses an observation distinct from original failure, never replay totals.
		r := recoveryEvaluationReview{source: "failure_inventory", workflow: facts[1].Workflow, eligibility: facts[1].Eligibility}
		d := api.WorkflowRecoveryPolicyDecision{RunID: facts[1].RunID, ResumeCount: facts[1].ResumeCount, Trigger: "saved_failure", Action: "retry", FailureStatus: facts[1].FailureStatus}
		proposed := testAutomaticRecoveryPolicy(facts[1].Workflow)
		got := failureEligibilityAssessment(r, d, proposed, proposed, now)
		if got.Proposed.Eligibility != "qualifies" || got.Proposed.Preparation != "unknown" || got.Proposed.ExecutionPressure != "not_evaluated" {
			t.Fatalf("later qualification %+v", got)
		}
		got = failureEligibilityAssessment(r, d, proposed, proposed, now.Add(api.WorkflowRecoveryFailureObservationFreshness))
		if got.Freshness != "stale" || got.Proposed.Eligibility != "unknown" {
			t.Fatalf("stale qualification %+v", got)
		}
		facts[1].Eligibility.Blockers = append(facts[1].Eligibility.Blockers, "detached-test")
		if reflect.DeepEqual(facts, failureInventoryFacts(t, s, app.ID)) {
			t.Fatal("snapshot aliases stored blockers")
		}
	})
}
func TestWorkflowFailureEligibilityConcurrentStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		for i := 0; i < 4; i++ {
			run, _ := seedResumeRun(t, s, simpleResumeSpec())
			failResumeRun(t, s, run, "send")
		}
		now := time.Now().UTC()
		var wg sync.WaitGroup
		results := make(chan bool, 4)
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				did, err := s.(WorkflowFailureEligibilityStore).ObserveNextWorkflowFailureEligibility(context.Background(), now)
				results <- did
				errs <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		n := 0
		for did := range results {
			if did {
				n++
			}
		}
		if n != 4 || observeFailureBatch(t, s, now) != 0 {
			t.Fatalf("concurrent claims %d", n)
		}
	})
}
func TestWorkflowFailureEligibilityAssessment(t *testing.T) {
	now := time.Now().UTC()
	p := testAutomaticRecoveryPolicy("orders")
	r := recoveryEvaluationReview{source: "failure_inventory", workflow: "orders", eligibility: &api.WorkflowRecoveryFailureEligibility{ObservedAt: now, Reason: "eligible", Blockers: []string{}}}
	d := api.WorkflowRecoveryPolicyDecision{RunID: "run", ResumeCount: 1, Trigger: "saved_failure", Action: "retry", FailureStatus: "dead"}
	for _, tt := range []struct{ reason, want string }{{"eligible", "qualifies"}, {"capacity", "qualifies"}, {"resume_ineligible", "excluded"}, {"changed", "excluded"}, {"inspection_error", "unknown"}, {"resume_limit_reached", "excluded"}} {
		t.Run(tt.reason, func(t *testing.T) {
			r.eligibility.Reason = tt.reason
			got := failureEligibilityAssessment(r, d, p, p, now)
			if got.Proposed.Eligibility != tt.want || got.Proposed.Preparation != "unknown" {
				t.Fatalf("%+v", got)
			}
		})
	}
	r.eligibility.Reason = "eligible"
	p.MaxResumeCount = 1
	if got := failureEligibilityAssessment(r, d, p, p, now); got.Proposed.Reason != "generation_limit" {
		t.Fatalf("%+v", got)
	}
	if got := failureEligibilityAssessment(r, d, p, p, now.Add(-time.Second)); got != nil {
		t.Fatalf("future observation %+v", got)
	}
	if _, err := failureEligibilityDiagnostic(workflowFailureEvidence{}, api.WorkflowRunDiagnosticsResponse{}, errors.New("private detail"), false, now); err == nil {
		t.Fatal("inspection error lost")
	}
	m := NewMemStore()
	if _, err := m.ObserveNextWorkflowFailureEligibility(context.Background(), time.Time{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ObserveNextWorkflowFailureEligibility(ctx, now); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWorkflowFailureEligibilityPostgresInspectionAndMigration(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("Postgres transaction checks")
		}
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		failResumeRun(t, s, run, "send")
		if _, err := pg.pool.Exec(ctx, "ALTER TABLE workflow_steps RENAME TO eligibility_test_steps"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pg.pool.Exec(ctx, "ALTER TABLE IF EXISTS eligibility_test_steps RENAME TO workflow_steps")
		})
		now := time.Now().UTC().Truncate(time.Microsecond)
		if n := observeFailureBatch(t, s, now); n != 1 {
			t.Fatalf("inspection %d", n)
		}
		got := failureInventoryFacts(t, s, app.ID)
		if got[0].Eligibility == nil || got[0].Eligibility.Reason != "inspection_error" {
			t.Fatalf("failed query poisoned transaction %+v", got)
		}
		if _, err := pg.pool.Exec(ctx, "ALTER TABLE eligibility_test_steps RENAME TO workflow_steps"); err != nil {
			t.Fatal(err)
		}
		if n := observeFailureBatch(t, s, now.Add(api.WorkflowRecoveryFailureObserverRefresh)); n != 1 {
			t.Fatalf("refresh %d", n)
		}
		if got = failureInventoryFacts(t, s, app.ID); got[0].Eligibility.Reason != "parent_not_retained" {
			t.Fatalf("recovered inspection %+v", got)
		}
		migration, err := os.ReadFile("../../migrations/20261012090000001_workflow_failure_eligibility.sql")
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(string(migration), "-- +goose Down")
		tx, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "cannot remove retained") {
			_ = tx.Rollback(ctx)
			t.Fatalf("unguarded downgrade %v", err)
		}
		_ = tx.Rollback(ctx)
		tx, err = pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, "UPDATE workflow_recovery_failure_evidence SET eligibility=NULL,eligibility_observed_at=NULL"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[1]); err != nil {
			t.Fatalf("empty down %v", err)
		}
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatalf("up %v", err)
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if n := observeFailureBatch(t, s, now.Add(api.WorkflowRecoveryFailureRetention+time.Minute)); n != 0 {
			t.Fatalf("observed expired evidence %d", n)
		}
	})
}
