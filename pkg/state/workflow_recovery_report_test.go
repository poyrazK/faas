package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryReportAggregation(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	entry := func(id, action, kind, reason, outcome string, seconds int, durable bool) api.WorkflowRecoveryHistoryEntry {
		return api.WorkflowRecoveryHistoryEntry{ID: id, ActionID: action, ChildJobID: "child-" + action, Kind: kind, EvaluationReason: reason, Reason: reason, Outcome: outcome, OccurredAt: at.Add(time.Duration(seconds) * time.Second), Workflow: "orders", PolicyVersion: 2, Durable: durable}
	}
	h := api.WorkflowRecoveryHistory{Entries: []api.WorkflowRecoveryHistoryEntry{
		entry("apply-old", "a", "retry_applied", "", "applied", 0, false),
		entry("apply", "a", "retry_transition", "child_created", "applied", 0, true),
		entry("result-old", "a", "retry_result", "", "succeeded", 12, false),
		entry("result", "a", "retry_transition", "result_recorded", "succeeded", 10, true),
		entry("b-apply", "b", "retry_transition", "child_created", "applied", 1, true),
		entry("c-result", "c", "retry_transition", "result_recorded", "failed", 2, true),
		entry("wait-1", "a", "retry_transition", "hourly_budget", "pending", 1, true),
		entry("wait-2", "a", "retry_transition", "hourly_budget", "pending", 2, true),
		entry("wait-3", "b", "retry_evaluation", "hourly_budget", "pending", 3, false),
		entry("pause", "", "policy_paused", "consecutive_failures", "paused", 4, true),
	}}
	out := recoveryReport(h)
	if out.Totals.Attempts != 2 || out.Totals.Succeeded != 1 || out.Totals.Failed != 1 || out.Totals.WithoutObservedResult != 1 || out.Totals.Pauses != 1 || out.Totals.TimedResults != 1 || *out.Totals.MeanRetryResultSeconds != 10 {
		t.Fatalf("totals %+v", out.Totals)
	}
	if len(out.Blockers) != 1 || out.Blockers[0].Actions != 2 || len(out.Blockers[0].Evidence) != 2 || len(out.Groups) != 1 || !reflect.DeepEqual(out.Groups[0].Totals, out.Totals) {
		t.Fatalf("groups/blockers %+v", out)
	}
	// Completed evidence with a different child or backwards clock must not create a duration sample.
	for _, kind := range []string{"child", "clock"} {
		result := entry("result", "a", "retry_result", "", "cancelled", 1, false)
		if kind == "child" {
			result.ChildJobID = "another"
		} else {
			result.OccurredAt = at.Add(-time.Second)
		}
		got := recoveryReport(api.WorkflowRecoveryHistory{Entries: []api.WorkflowRecoveryHistoryEntry{h.Entries[0], result}})
		if got.Totals.TimedResults != 0 || got.Totals.MeanRetryResultSeconds != nil || got.Totals.Cancelled != 1 {
			t.Fatalf("invalid timing %s %+v", kind, got)
		}
	}
	empty := recoveryReport(api.WorkflowRecoveryHistory{})
	if empty.Groups == nil || empty.Blockers == nil || empty.Evidence == nil || empty.Totals.MeanRetryResultSeconds != nil {
		t.Fatalf("empty %+v", empty)
	}
}
func TestWorkflowRecoveryReportBound(t *testing.T) {
	entries := make([]api.WorkflowRecoveryHistoryEntry, api.WorkflowRecoveryReportEntriesMax+1)
	got := recoveryReport(api.WorkflowRecoveryHistory{Entries: entries})
	if !got.Truncated || got.ScannedEntries != api.WorkflowRecoveryReportEntriesMax {
		t.Fatalf("bound %+v", got)
	}
}
func TestWorkflowRecoveryReportStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		_, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		before := policyChangeEvidence(t, s, app.ID)
		reports := s.(WorkflowRecoveryReportStore)
		out, err := reports.GetWorkflowRecoveryReport(ctx, app.AccountID, app.ID, api.WorkflowRecoveryReportOptions{})
		if err != nil || out.ScannedEntries == 0 || out.DurableEntries == 0 || out.Truncated || out.Groups == nil {
			t.Fatalf("report %+v %v", out, err)
		}
		if string(before) != string(policyChangeEvidence(t, s, app.ID)) {
			t.Fatal("report mutated state")
		}
		out, err = reports.GetWorkflowRecoveryReport(ctx, app.AccountID, app.ID, api.WorkflowRecoveryReportOptions{Workflow: "absent"})
		if err != nil || out.ScannedEntries != 0 {
			t.Fatalf("filter %+v %v", out, err)
		}
		if _, err = reports.GetWorkflowRecoveryReport(ctx, uuid.NewString(), app.ID, api.WorkflowRecoveryReportOptions{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("scope %v", err)
		}
		if _, err = reports.GetWorkflowRecoveryReport(ctx, app.AccountID, app.ID, api.WorkflowRecoveryReportOptions{PolicyVersion: -1}); !errors.Is(err, ErrEventRecoveryQuery) {
			t.Fatalf("validation %v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = reports.GetWorkflowRecoveryReport(cancelled, app.AccountID, app.ID, api.WorkflowRecoveryReportOptions{}); err == nil {
			t.Fatal("cancelled report succeeded")
		}
	})
}

func TestWorkflowRecoveryReportRetainsPrunedResults(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		review, app, run, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) { p.PauseAfterFailures = 1 })
		runAutomaticPolicy(t, s, now)
		failAutomaticChild(t, s, run)
		runAutomaticPolicy(t, s, now.Add(time.Minute))
		// Fixture worker clocks are ahead of wall time. Move recorded proof into the
		// report window while preserving its relative durations; remove source rows.
		events := durableRecoveryEvents(t, s, app.ID)
		for _, e := range events {
			if e.Kind == "policy_changed" {
				continue
			}
			e.OccurredAt = e.OccurredAt.Add(-time.Hour)
			if e.CandidateEvidence != nil {
				e.CandidateEvidence.ObservedAt = e.OccurredAt
			}
			if m, ok := s.(*MemStore); ok {
				m.mu.Lock()
				for i := range m.recoveryPolicies[canonicalMemUUID(app.ID)].History {
					if m.recoveryPolicies[canonicalMemUUID(app.ID)].History[i].Entry.ID == e.ID {
						m.recoveryPolicies[canonicalMemUUID(app.ID)].History[i].Entry = e
					}
				}
				m.mu.Unlock()
			} else {
				raw, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_history_events SET occurred_at=$1, entry=$2 WHERE id=$3", e.OccurredAt, raw, strings.TrimPrefix(e.ID, "audit:")); err != nil {
					t.Fatal(err)
				}
			}
		}
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			forgetRecoveryPolicyReview(m.recoveryPolicies[canonicalMemUUID(app.ID)], review.ID)
			delete(m.eventRecoveryJobs, canonicalMemUUID(review.JobID))
			m.mu.Unlock()
		} else {
			if _, err := s.(*PgStore).pool.Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1", review.JobID); err != nil {
				t.Fatal(err)
			}
		}
		out, err := s.(WorkflowRecoveryReportStore).GetWorkflowRecoveryReport(ctx, app.AccountID, app.ID, api.WorkflowRecoveryReportOptions{})
		if err != nil || out.Totals.Attempts != 1 || out.Totals.Failed != 1 || out.Totals.Pauses != 1 || out.Totals.WithoutObservedResult != 0 || out.Totals.TimedResults != 1 || *out.Totals.MeanRetryResultSeconds != 60 {
			t.Fatalf("pruned report %+v %v", out, err)
		}
		filtered, err := s.(WorkflowRecoveryReportStore).GetWorkflowRecoveryReport(ctx, app.AccountID, app.ID, api.WorkflowRecoveryReportOptions{Workflow: run.WorkflowName, PolicyVersion: review.PolicyVersion})
		if err != nil || filtered.Totals.Attempts != 1 || filtered.Totals.Failed != 1 || filtered.Totals.Pauses != 0 {
			t.Fatalf("filtered %+v %v", filtered, err)
		}
	})
}
