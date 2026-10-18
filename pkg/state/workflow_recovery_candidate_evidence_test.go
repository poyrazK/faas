package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryCandidateEvidenceSurvivesPruning(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		r, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		var evidence api.WorkflowRecoveryHistoryEntry
		for _, e := range durableRecoveryEvents(t, s, app.ID) {
			if e.Kind == "candidates_observed" && e.ReviewID == r.ID {
				evidence = e
			}
		}
		if evidence.CandidateEvidence == nil || evidence.CandidateEvidence.Decisions[0].FailureStatus != r.Decisions[0].FailureStatus || r.Decisions[0].FailureStatus == "" || evidence.CandidateEvidence.Decisions[0].PreflightOutcome != "eligible" {
			t.Fatalf("capture missing: %+v", evidence)
		}
		policies := s.(WorkflowRecoveryPolicyStore)
		for i := 1; i <= 2; i++ {
			if _, err := policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(time.Duration(i)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		seen := map[string]bool{}
		for _, e := range durableRecoveryEvents(t, s, app.ID) {
			if e.Kind != "candidates_observed" {
				continue
			}
			if seen[e.ReviewID] {
				t.Fatal("duplicate candidate snapshot")
			}
			seen[e.ReviewID] = true
		}
		// The production fixture advances its scheduler clock; move the frozen snapshot
		// into the read window before deleting its source associations.
		at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		evidence.OccurredAt = at
		evidence.CandidateEvidence.ObservedAt = at
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			p := m.recoveryPolicies[canonicalMemUUID(app.ID)]
			for i := range p.History {
				if p.History[i].Entry.ID == evidence.ID {
					p.History[i].Entry = evidence
				}
			}
			for id := range p.Reviews {
				forgetRecoveryPolicyReview(p, id)
			}
			delete(m.eventRecoveryJobs, canonicalMemUUID(r.JobID))
			m.mu.Unlock()
		} else {
			pg := s.(*PgStore)
			raw, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pg.pool.Exec(ctx, "UPDATE workflow_recovery_history_events SET entry=$1,occurred_at=$2 WHERE app_id=$3 AND entry->>'id'=$4", raw, at, app.ID, evidence.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = pg.pool.Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1", r.JobID); err != nil {
				t.Fatal(err)
			}
			s = NewPgStore(pg.pool.Pool)
		}
		proposed := recoveryPolicyClone(r.Policy)
		proposed.FailureStatuses = []string{r.Decisions[0].FailureStatus}
		out, err := s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: r.PolicyVersion, Policy: proposed, CreatedBefore: at.Add(time.Second)})
		if err != nil || len(out.Candidates) != 1 || out.ProposedTotals.WouldPrepare != 1 || out.Candidates[0].CandidateEvidenceID != evidence.ID || len(out.Candidates[0].HistoricalFailureStatuses) != 1 || out.Candidates[0].HistoricalFailureStatuses[0] != r.Decisions[0].FailureStatus {
			t.Fatalf("pruning/restart evaluation: %+v %v", out, err)
		}
		// Evidence remains subject to recording-age retention; reads do not prune it.
		aged := time.Now().Add(-api.WorkflowRecoveryHistoryRetention - time.Hour)
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			for i := range m.recoveryPolicies[canonicalMemUUID(app.ID)].History {
				m.recoveryPolicies[canonicalMemUUID(app.ID)].History[i].RecordedAt = aged
			}
			m.mu.Unlock()
		} else {
			if _, err = s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_history_events SET recorded_at=$1 WHERE app_id=$2", aged, app.ID); err != nil {
				t.Fatal(err)
			}
		}
		out, err = s.(WorkflowRecoveryPolicyEvaluationStore).EvaluateWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.WorkflowRecoveryPolicyEvaluationRequest{ExpectedVersion: r.PolicyVersion, Policy: proposed, CreatedBefore: at.Add(time.Second)})
		if err != nil || len(out.Candidates) != 0 {
			t.Fatalf("expired candidate facts: %+v %v", out, err)
		}
	})
}
