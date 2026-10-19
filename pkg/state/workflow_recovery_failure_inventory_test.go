package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func failureInventoryFacts(t *testing.T, s Store, app string) []workflowFailureEvidence {
	t.Helper()
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		defer m.mu.Unlock()
		return recoveryPolicyClone(m.workflowFailureEvidence[canonicalMemUUID(app)])
	}
	c := recoveryHistoryCursor{Start: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Now().Add(time.Hour), ObservedAt: time.Now().Add(time.Hour)}
	snapshot, err := recoveryFailureSnapshotRead(context.Background(), sqlc.New(), s.(*PgStore).pool, app, api.WorkflowRecoveryPolicyEvaluationRequest{}, c)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Failures
}
func clearEvaluationFailureInventory(t *testing.T, s Store, app string) {
	t.Helper()
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		delete(m.workflowFailureEvidence, canonicalMemUUID(app))
		m.mu.Unlock()
		return
	}
	if _, err := s.(*PgStore).pool.Exec(context.Background(), "DELETE FROM workflow_recovery_failure_evidence WHERE app_id=$1", app); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRecoveryFailureInventoryStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		failResumeRun(t, s, run, "send")
		first := failureInventoryFacts(t, s, app.ID)
		if len(first) != 1 || first[0].ResumeCount != 0 || first[0].FailureStatus != "dead" || first[0].Workflow != run.WorkflowName {
			t.Fatalf("initial failure not captured: %+v", first)
		}
		if err := s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusDead, nil, nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, failureInventoryFacts(t, s, app.ID)) {
			t.Fatal("repeated terminal update changed evidence")
		}
		policies := s.(WorkflowRecoveryPolicyStore)
		baseline, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil || baseline.Version != 0 {
			t.Fatalf("capture created a policy: %+v %v", baseline, err)
		}
		proposed := testAutomaticRecoveryPolicy(run.WorkflowName)
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: proposed})
		if err != nil || len(out.Candidates) != 1 || out.FailureCoverage.CapturedFailures != 1 || out.FailureCoverage.UnreviewedFailures != 1 || out.FailureCoverage.MissingEligibilityEvidence != 1 || out.InspectedReviews != 0 || out.CurrentTotals.Excluded != 1 || out.ProposedTotals.Unknown != 1 || out.Candidates[0].EvidenceSource != "failure_inventory" || out.Candidates[0].Proposed.Reason != "historical_preflight_not_captured" || out.Candidates[0].ReviewID != "" || out.Candidates[0].FailureEvidenceID != "failure:"+first[0].ID {
			t.Fatalf("no-policy evaluation: %+v %v", out, err)
		}
		if out.FailureCoverage.CaptureStartedAt.IsZero() || out.FailureCoverage.Coverage != "partial" {
			t.Fatalf("missing coverage: %+v", out.FailureCoverage)
		}
		outside := testAutomaticRecoveryPolicy("other-workflow")
		outside.FailureStatuses = []string{"failed"}
		accepted, err := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: outside})
		if err != nil {
			t.Fatal(err)
		}
		req := api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: accepted.Version, Policy: proposed}
		before := policyChangeEvidence(t, s, app.ID)
		out, err = s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req)
		if err != nil || out.CurrentTotals.Excluded != 1 || out.ProposedTotals.Unknown != 1 || out.Candidates[0].Current.Reason != "workflow_filter" {
			t.Fatalf("widened filters: %+v %v", out, err)
		}
		if string(before) != string(policyChangeEvidence(t, s, app.ID)) || !reflect.DeepEqual(first, failureInventoryFacts(t, s, app.ID)) {
			t.Fatal("evaluation mutated state")
		}
		req.Workflow = "absent"
		out, err = s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req)
		if err != nil || out.FailureCoverage.CapturedFailures != 0 || len(out.Candidates) != 0 {
			t.Fatalf("scope filter: %+v %v", out, err)
		}
		req.Workflow = ""
		if _, err = s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, uuid.NewString(), app.ID, req); !errors.Is(err, ErrNotFound) {
			t.Fatalf("account scope: %v", err)
		}
		if _, _, _, err = s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, s, run, "send")
		facts := failureInventoryFacts(t, s, app.ID)
		if len(facts) != 2 || facts[0].ResumeCount != 0 || facts[1].ResumeCount != 1 || facts[0].ID == facts[1].ID {
			t.Fatalf("generation dedup: %+v", facts)
		}
		if _, err = s.SweepExpiredWorkflowRuns(ctx, 0); err != nil {
			t.Fatal(err)
		}
		if pg, ok := s.(*PgStore); ok {
			s = NewPgStore(pg.pool.Pool)
		}
		out, err = s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req)
		if err != nil || out.FailureCoverage.CapturedFailures != 2 || out.ProposedTotals.Unknown != 2 {
			t.Fatalf("run pruning/restart: %+v %v", out, err)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "enabled\":true}") || strings.Contains(string(raw), "provider unavailable") || strings.Contains(string(raw), "keep_literal") {
			t.Fatal("execution data leaked")
		}
	})
}

func TestWorkflowRecoveryFailureInventoryExcludesCancellationAndSuccess(t *testing.T) {
	for _, status := range []string{"cancelled", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				run, app := seedResumeRun(t, s, simpleResumeSpec())
				ctx := context.Background()
				if status == "cancelled" {
					if _, err := s.CancelWorkflowRun(ctx, run.ID, "operator request"); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if got := failureInventoryFacts(t, s, app.ID); len(got) != 0 {
					t.Fatalf("captured %s: %+v", status, got)
				}
			})
		})
	}
}

func TestWorkflowRecoveryFailureInventoryCoverageAndBounds(t *testing.T) {
	p := testAutomaticRecoveryPolicy("orders")
	run := uuid.NewString()
	r := recoveryEvaluationReview{review: api.WorkflowRecoveryPolicyReview{ID: uuid.NewString(), ObservedAt: time.Now(), Policy: p, Decisions: []api.WorkflowRecoveryPolicyDecision{{RunID: run, ResumeCount: 1, Trigger: "saved_failure", Action: "retry", Outcome: "prepared"}}}, workflow: "orders"}
	snapshot := recoveryFailureSnapshot{StartedAt: time.Now(), Failures: []workflowFailureEvidence{{ID: uuid.NewString(), RunID: run, Workflow: "orders", ResumeCount: 1, FailureStatus: "dead", ObservedAt: time.Now()}, {ID: uuid.NewString(), RunID: run, Workflow: "orders", ResumeCount: 2, FailureStatus: "dead", ObservedAt: time.Now()}}}
	reviews, coverage := recoveryEvaluationWithFailures([]recoveryEvaluationReview{r}, snapshot)
	if len(reviews) != 2 || coverage.CapturedFailures != 2 || coverage.ReviewedFailures != 1 || coverage.UnreviewedFailures != 1 || coverage.MissingEligibilityEvidence != 1 {
		t.Fatalf("correlation: %+v %+v", reviews, coverage)
	}
	snapshot.Failures = make([]workflowFailureEvidence, api.WorkflowRecoveryPolicyEvaluationFailuresMax+1)
	for i := range snapshot.Failures {
		snapshot.Failures[i] = workflowFailureEvidence{ID: uuid.NewString(), RunID: uuid.NewString(), Workflow: "orders", ObservedAt: time.Now(), FailureStatus: "dead"}
	}
	reviews, coverage = recoveryEvaluationWithFailures(nil, snapshot)
	if !coverage.Truncated || coverage.CapturedFailures != api.WorkflowRecoveryPolicyEvaluationFailuresMax || len(reviews) != api.WorkflowRecoveryPolicyEvaluationFailuresMax {
		t.Fatalf("failure cap: %+v", coverage)
	}
}

func TestWorkflowRecoveryFailureInventoryRetention(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		failResumeRun(t, s, run, "send")
		aged := time.Now().UTC().Add(-api.WorkflowRecoveryFailureRetention - time.Hour)
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			m.workflowFailureEvidence[canonicalMemUUID(app.ID)][0].ObservedAt = aged
			m.workflowFailureEvidence[canonicalMemUUID(app.ID)][0].CompletedAt = nil
			m.mu.Unlock()
		} else {
			if _, err := s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_failure_evidence SET observed_at=$1,completed_at=NULL WHERE app_id=$2", aged, app.ID); err != nil {
				t.Fatal(err)
			}
		}
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{Policy: testAutomaticRecoveryPolicy(run.WorkflowName)})
		if err != nil || out.FailureCoverage.CapturedFailures != 0 || len(out.Candidates) != 0 {
			t.Fatalf("expired evidence visible: %+v %v", out, err)
		}
		n, err := s.(WorkflowRecoveryHistoryRetentionStore).PruneWorkflowRecoveryHistory(ctx, time.Now().UTC())
		if err != nil || n != 1 {
			t.Fatalf("inactive retention: %d %v", n, err)
		}
		if n, err = s.(WorkflowRecoveryHistoryRetentionStore).PruneWorkflowRecoveryHistory(ctx, time.Now().UTC()); err != nil || n != 0 {
			t.Fatalf("repeat retention: %d %v", n, err)
		}
	})
}

func TestWorkflowRecoveryFailureInventoryAtomicPostgres(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("Postgres failure injection")
		}
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		before, err := s.GetWorkflowRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pg.pool.Exec(ctx, "ALTER TABLE workflow_recovery_failure_evidence ADD CONSTRAINT failure_capture_reject CHECK(failure_status<>'dead')"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = pg.pool.Exec(ctx, "ALTER TABLE workflow_recovery_failure_evidence DROP CONSTRAINT IF EXISTS failure_capture_reject")
		}()
		if err = s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusDead, nil, nil); err == nil {
			t.Fatal("capture failure accepted")
		}
		after, err := s.GetWorkflowRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) || len(failureInventoryFacts(t, s, app.ID)) != 0 {
			t.Fatal("terminal mutation partially committed")
		}
		if _, err = pg.pool.Exec(ctx, "ALTER TABLE workflow_recovery_failure_evidence DROP CONSTRAINT failure_capture_reject"); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusDead, nil, nil); err != nil {
			t.Fatal(err)
		}
		if len(failureInventoryFacts(t, s, app.ID)) != 1 {
			t.Fatal("retry lost capture")
		}
	})
}

func TestWorkflowRecoveryFailureInventoryMigrationAndCapacity(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("Postgres schema bounds")
		}
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		var cap, seconds int64
		if err := pg.pool.QueryRow(ctx, "SELECT max_entries,retention_seconds FROM workflow_recovery_failure_capture").Scan(&cap, &seconds); err != nil {
			t.Fatal(err)
		}
		if cap != int64(api.WorkflowRecoveryFailureEvidenceMax) || seconds != int64(api.WorkflowRecoveryFailureRetention/time.Second) {
			t.Fatalf("central limit drift: %d %d", cap, seconds)
		}
		tx, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, "UPDATE workflow_recovery_failure_capture SET max_entries=3"); err != nil {
			t.Fatal(err)
		}
		for count := 0; count < 5; count++ {
			if _, err = tx.Exec(ctx, "SELECT capture_workflow_recovery_failure($1,$2,$3,'dead',$4,clock_timestamp(),false)", app.ID, run.ID, run.WorkflowName, count); err != nil {
				t.Fatal(err)
			}
		}
		var retained int
		var minimum int
		if err = tx.QueryRow(ctx, "SELECT count(*),min(resume_count) FROM workflow_recovery_failure_evidence WHERE app_id=$1", app.ID).Scan(&retained, &minimum); err != nil {
			t.Fatal(err)
		}
		if retained != 3 || minimum != 2 {
			t.Fatalf("capacity: %d %d", retained, minimum)
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, s, run, "send")
		migration, err := os.ReadFile("../../migrations/20261011190000001_workflow_recovery_failure_inventory.sql")
		if err != nil {
			t.Fatal(err)
		}
		down := strings.Split(string(migration), "-- +goose Down")[1]
		tx, err = pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
			_ = tx.Rollback(ctx)
			t.Fatalf("unguarded downgrade: %v", err)
		}
		_ = tx.Rollback(ctx)
		if len(failureInventoryFacts(t, s, app.ID)) != 1 {
			t.Fatal("downgrade guard lost capture")
		}
	})
}

func TestWorkflowRecoveryFailureInventoryConcurrentCapture(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		first, app := seedResumeRun(t, s, simpleResumeSpec())
		runs := []*WorkflowRun{first}
		for i := 0; i < 3; i++ {
			r := &WorkflowRun{AppID: app.ID, WorkflowName: first.WorkflowName, Input: first.Input, DefinitionSnapshot: first.DefinitionSnapshot}
			if err := s.CreateWorkflowRun(ctx, r); err != nil {
				t.Fatal(err)
			}
			runs = append(runs, r)
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(runs))
		for _, r := range runs {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				errs <- s.MarkWorkflowRunStatus(ctx, id, WorkflowRunStatusDead, nil, nil)
			}(r.ID)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(failureInventoryFacts(t, s, app.ID)) != len(runs) {
			t.Fatal("concurrent capture lost failures")
		}
	})
}

func TestWorkflowRecoveryFailureInventoryDisabledAndTerminalInsert(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		disabled := testAutomaticRecoveryPolicy("other")
		disabled.Enabled = false
		if _, err := s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: disabled}); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, s, run, "send")
		r := &WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, Status: WorkflowRunStatusFailed, Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot}
		if err := s.CreateWorkflowRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		facts := failureInventoryFacts(t, s, app.ID)
		if len(facts) != 2 || facts[1].RunID != canonicalMemUUID(r.ID) || facts[1].FailureStatus != "failed" {
			t.Fatalf("disabled/terminal insert: %+v", facts)
		}
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: 1, Policy: testAutomaticRecoveryPolicy(run.WorkflowName)})
		if err != nil || out.CurrentTotals.Excluded != 2 || out.ProposedTotals.Unknown != 2 || out.FailureCoverage.CapturedFailures != 2 {
			t.Fatalf("disabled evidence: %+v %v", out, err)
		}
	})
}

func TestWorkflowRecoveryFailureInventoryPrefersReviewStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		r, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		r.ObservedAt = time.Now().UTC().Truncate(time.Microsecond)
		evaluationFixtureReview(t, s, r)
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(context.Background(), app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: r.PolicyVersion, Policy: r.Policy})
		if err != nil || len(out.Candidates) != 2 || out.InspectedReviews != 1 || out.FailureCoverage.CapturedFailures != 2 || out.FailureCoverage.ReviewedFailures != 1 || out.FailureCoverage.UnreviewedFailures != 1 || out.FailureCoverage.MissingEligibilityEvidence != 1 || out.ProposedTotals.WouldPrepare != 1 || out.ProposedTotals.Unknown != 1 {
			t.Fatalf("review/inventory correlation: %+v %v", out, err)
		}
		for _, c := range out.Candidates {
			if c.ResumeCount == 1 && (c.EvidenceSource != "policy_review" || c.FailureEvidenceID != "" || c.ReviewID != r.ID) {
				t.Fatalf("review proof lost: %+v", c)
			}
		}
	})
}
