package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1270
func TestWorkflowRecoveryEvidenceHistory(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "history-operator")
		review, app, run, _ := state.SeedAutomaticRecoveryReview(t, s, nil)
		action := review.AutomaticExecution.Actions[0].ActionID
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			delete((*m.EventRecoveryJobs())[review.JobID].WorkflowResults, 1)
			(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Executions[action].Reason = "evidence_unknown"
			(*m.Mu()).Unlock()
		} else {
			pg := s.(*state.PgStore)
			if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", review.JobID); err != nil {
				t.Fatal(err)
			}
			if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_recovery_policy_executions SET reason='evidence_unknown' WHERE action_id=$1::uuid", action); err != nil {
				t.Fatal(err)
			}
		}
		poll := func() {
			t.Helper()
			if _, err := s.(state.WorkflowRecoveryAutomaticBlockedStore).ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
		poll()
		req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "restore history proof"}
		repair := func() {
			t.Helper()
			if _, err := s.(state.WorkflowRetryParentEvidenceStore).RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action, req); err != nil {
				t.Fatal(err)
			}
		}
		repair()
		repair()
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].BlockedNext[action] = time.Time{}
			(*m.Mu()).Unlock()
		} else {
			if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_recovery_policy_executions SET next_blocked_at=$2 WHERE action_id=$1::uuid", action, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		poll()
		if pg, ok := s.(*state.PgStore); ok {
			s = state.NewPgStore((*pg.Pool()).Pool)
		}
		options := api.WorkflowRecoveryHistoryOptions{RunID: run.ID, Workflow: run.WorkflowName, PolicyVersion: review.PolicyVersion, Limit: 100}
		history := s.(state.WorkflowRecoveryHistoryStore)
		read := func() api.WorkflowRecoveryHistory {
			t.Helper()
			out, err := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, options)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		out := read()
		counts := map[string]int{}
		for _, e := range out.Entries {
			if e.EvidenceKind == "" {
				continue
			}
			counts[e.Kind]++
			if !e.Durable || e.RunID != run.ID || e.ResumeCount == nil || *e.ResumeCount != 1 || e.ActionID != action || e.ReviewURL == "" || e.EvidenceURL == "" || e.JobURL == "" {
				t.Fatalf("associations %+v", e)
			}
			if e.Kind == "retry_parent_evidence_repaired" && (e.RequestID != req.RequestID || e.ActorID != "history-operator" || e.Reason != req.Reason || e.Outcome != "failed") {
				t.Fatalf("repair audit %+v", e)
			}
		}
		for _, kind := range []string{"retry_parent_evidence_repaired", "retry_evidence_blocked", "retry_evidence_cleared"} {
			if counts[kind] != 1 {
				t.Fatalf("counts %+v", counts)
			}
		}
		// Page through the same fenced view without losing or repeating events.
		options.Limit = 1
		options.After = ""
		seen := map[string]bool{}
		for {
			page := read()
			for _, e := range page.Entries {
				if seen[e.ID] {
					t.Fatal("duplicate page event")
				}
				seen[e.ID] = true
			}
			if page.NextAfter == "" {
				break
			}
			options.After = page.NextAfter
		}
		if len(seen) != len(out.Entries) {
			t.Fatal("pagination lost events")
		}
		if _, err := history.GetWorkflowRecoveryHistory(ctx, uuid.NewString(), app.ID, api.WorkflowRecoveryHistoryOptions{}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign scope %v", err)
		}
		// Parent and child repairs keep independent audit identities on one action.
		state.RunAutomaticPolicy(t, s, time.Now().Add(5*time.Minute))
		child, err := state.AutomaticChild(t, s, app, review)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
			t.Fatal(err)
		}
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			delete((*m.EventRecoveryJobs())[child.ID].WorkflowResults, 1)
			(*m.Mu()).Unlock()
		} else {
			if _, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", child.ID); err != nil {
				t.Fatal(err)
			}
		}
		childReq := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "restore child proof"}
		for range 2 {
			if _, err = s.(state.WorkflowRetryEvidenceStore).RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action, childReq); err != nil {
				t.Fatal(err)
			}
		}
		options.Limit = 100
		options.After = ""
		childCount := 0
		for _, e := range read().Entries {
			if e.RequestID == childReq.RequestID {
				childCount++
				if !e.Durable || e.EvidenceKind != "child" || e.ResumeCount == nil || *e.ResumeCount != 2 || e.ChildJobID != child.ID {
					t.Fatalf("child audit %+v", e)
				}
			}
		}
		if childCount != 1 {
			t.Fatal("child replay duplicated history")
		}
		// A pre-history receipt remains a retained observation while its review exists.
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			p := (*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)]
			kept := p.History[:0]
			for _, event := range p.History {
				if event.Entry.RequestID != req.RequestID {
					kept = append(kept, event)
				}
			}
			p.History = kept
			(*m.Mu()).Unlock()
		} else {
			pg := s.(*state.PgStore)
			if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM workflow_recovery_history_events WHERE app_id=$1::uuid AND entry->>'request_id'=$2", app.ID, req.RequestID); err != nil {
				t.Fatal(err)
			}
		}
		options.Limit = 100
		options.After = ""
		count := 0
		for _, e := range read().Entries {
			if e.RequestID == req.RequestID {
				count++
				if e.Durable || e.ActorID != "history-operator" {
					t.Fatal("legacy receipt mislabeled")
				}
			}
		}
		if count != 1 {
			t.Fatalf("legacy receipt count %d", count)
		}
		// Durable child and alert events survive source job/review pruning.
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			delete((*m.EventRecoveryJobs()), review.JobID)
			delete((*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Reviews, review.ID)
			(*m.Mu()).Unlock()
		} else {
			if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", review.JobID); err != nil {
				t.Fatal(err)
			}
		}
		survived := 0
		for _, e := range read().Entries {
			if e.Durable && e.EvidenceKind != "" {
				survived++
			}
		}
		if survived != 3 {
			t.Fatalf("durable events after pruning %d", survived)
		}

	})
}
