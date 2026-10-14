package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func policyChangeEvidence(t *testing.T, s Store, app string) []byte {
	t.Helper()
	return policyChangeEvidenceSnapshot(t, s, app, true)
}

func policyChangeEvidenceSnapshot(t *testing.T, s Store, app string, includeEligibility bool) []byte {
	t.Helper()
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		defer m.mu.Unlock()
		failures := m.workflowFailureEvidence
		if !includeEligibility {
			failures = make(map[string][]workflowFailureEvidence, len(m.workflowFailureEvidence))
			for id, rows := range m.workflowFailureEvidence {
				cloned := append([]workflowFailureEvidence(nil), rows...)
				for i := range cloned {
					cloned[i].Eligibility = nil
				}
				failures[id] = cloned
			}
		}
		raw, err := json.Marshal([]any{m.recoveryPolicies[canonicalMemUUID(app)], m.eventRecoveryJobs, m.workflowRuns, m.workflowSteps, m.appWebhookEventOutbox, m.initialRecoveryReviews, failures, m.workflowResumes})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var raw []byte
	err := s.(*PgStore).pool.QueryRow(context.Background(), `SELECT jsonb_build_object(
 'policy',(SELECT to_jsonb(p) FROM workflow_recovery_policies p WHERE app_id=$1::uuid),
 'executions',(SELECT jsonb_agg(to_jsonb(e) ORDER BY action_id) FROM workflow_recovery_policy_executions e WHERE app_id=$1::uuid),
 'reviews',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM workflow_recovery_policy_reviews r WHERE app_id=$1::uuid),
 'targets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY review_id,run_id) FROM workflow_recovery_policy_targets t WHERE app_id=$1::uuid),
 'charges',(SELECT jsonb_agg(to_jsonb(c) ORDER BY action_id) FROM workflow_recovery_policy_execution_charges c WHERE app_id=$1::uuid),
 'jobs',(SELECT jsonb_agg(to_jsonb(j) ORDER BY id) FROM event_recovery_jobs j WHERE app_id=$1::uuid),
 'initial_reviews',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM workflow_initial_recovery_reviews r WHERE app_id=$1::uuid),
 'failures',(SELECT jsonb_agg(to_jsonb(f) - CASE WHEN $2::boolean THEN ARRAY[]::text[] ELSE ARRAY['eligibility','eligibility_observed_at'] END ORDER BY id) FROM workflow_recovery_failure_evidence f WHERE app_id=$1::uuid),
 'runs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM workflow_runs r WHERE app_id=$1::uuid),
 'outbox',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM app_webhook_event_outbox o WHERE app_id=$1::uuid))`, app, includeEligibility).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestWorkflowRecoveryPolicyChangePreview(t *testing.T) {
	for _, scenario := range []string{"pending", "applied", "disabled", "filters"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				r, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
				if scenario == "applied" {
					runAutomaticPolicy(t, s, now)
				}
				req := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: r.PolicyVersion, Policy: r.Policy}
				req.Policy.CooldownSeconds = 3600
				req.Policy.HourlyExecutionBudget = 1
				if scenario == "disabled" {
					req.Policy.Enabled = false
				}
				if scenario == "filters" {
					req.Policy.WorkflowNames = []string{"other"}
				}
				before := policyChangeEvidence(t, s, app.ID)
				preview := s.(WorkflowRecoveryPolicyChangeStore)
				out, err := preview.PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, req)
				if err != nil {
					t.Fatal(err)
				}
				if out.Current.Version != r.PolicyVersion || out.NextVersion != r.PolicyVersion+1 || !reflect.DeepEqual(out.Proposed, req.Policy) || len(out.Entries) != 1 {
					t.Fatalf("preview %+v", out)
				}
				if scenario == "applied" {
					if out.PendingSuppressed != 0 || out.OutstandingRetained != 1 || out.HourlyExecutions != 1 || out.RemainingExecutionBudget != 0 || out.ExecutionCooldownUntil == nil || out.Entries[0].Effect != "retain_slot" {
						t.Fatalf("slot or budget %+v", out)
					}
				} else if out.PendingSuppressed != 1 || out.OutstandingRetained != 0 || out.Entries[0].Effect != "would_suppress" {
					t.Fatalf("pending %+v", out)
				}
				if scenario == "filters" && (out.InspectedJobs != 0 || len(out.Candidates) != 0) {
					t.Fatalf("filter mismatch %+v", out)
				}
				if scenario == "pending" && (len(out.Candidates) != 1 || out.Candidates[0].Decision.Outcome != "already_prepared") {
					t.Fatalf("retained deduplication %+v", out)
				}
				if scenario == "disabled" && out.AutomaticExecutionEnabled {
					t.Fatal("disabled policy authorizes execution")
				}
				out.Proposed.WorkflowNames[0] = "mutated"
				if after := policyChangeEvidence(t, s, app.ID); string(before) != string(after) {
					t.Fatal("preview mutated recovery evidence, counters, scheduling or outbox")
				}
				stale := req
				stale.ExpectedVersion++
				if _, err = preview.PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, stale); !errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
					t.Fatalf("stale preview %v", err)
				}
				if _, err = preview.PreviewWorkflowRecoveryPolicyChange(ctx, uuid.NewString(), app.ID, req); !errors.Is(err, ErrNotFound) {
					t.Fatalf("scope %v", err)
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err = preview.PreviewWorkflowRecoveryPolicyChange(cancelled, app.AccountID, app.ID, req); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel %v", err)
				}
				if _, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req); err != nil {
					t.Fatal(err)
				}
				if _, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
					t.Fatalf("stale apply %v", err)
				}
			})
		})
	}
}
func TestWorkflowRecoveryPolicyChangeCandidateBounds(t *testing.T) {
	now := time.Now().UTC()
	queue := api.WorkflowRecoveryAutomaticQueue{Policy: api.WorkflowRecoveryPolicyResponse{AppID: uuid.NewString(), Version: 1}, ObservedAt: now}
	p := testAutomaticRecoveryPolicy("recover")
	b := recoveryPolicyBudget{Existing: map[string]bool{}}
	out := recoveryPolicyChangePreview(queue, p, nil, b)
	candidates := []recoveryPolicyCandidate{}
	for i := 0; i < api.AutomationRecoveryBatchMaxRuns+1; i++ {
		candidates = append(candidates, recoveryPolicyCandidate{Decision: api.WorkflowRecoveryPolicyDecision{Position: int64(i + 1), RunID: uuid.NewString(), Outcome: "unknown"}})
	}
	addRecoveryPolicyChangeCandidates(&out, api.EventRecoveryJob{ID: uuid.NewString()}, candidates, &b)
	if !out.CandidatesTruncated || len(out.Candidates) != api.AutomationRecoveryBatchMaxRuns {
		t.Fatalf("bounds %+v", out)
	}
}
