package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryHistory(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		review, app, run, _ := state.SeedAutomaticRecoveryReview(t, s, nil)
		// Place immutable fixture observations before the API's wall-clock fence.
		at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		review.ObservedAt = at
		raw, _ := json.Marshal(review)
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Reviews[review.ID] = raw
			(*m.Mu()).Unlock()
		} else {
			if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_recovery_policy_reviews SET observed_at=$1,review=$2 WHERE id=$3", at, raw, review.ID); err != nil {
				t.Fatal(err)
			}
		}
		operator := s.(state.WorkflowRecoveryPolicyOperatorStore)
		p, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Operation: "pause", Reason: "audit provider"}); err != nil {
			t.Fatal(err)
		}
		before := state.PolicyChangeEvidence(t, s, app.ID)
		store := s.(state.WorkflowRecoveryHistoryStore)
		o := api.WorkflowRecoveryHistoryOptions{Limit: 1}
		seen := map[string]bool{}
		kinds := map[string]bool{}
		pages := 0
		var observed time.Time
		for {
			out, err := store.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, o)
			if err != nil {
				t.Fatal(err)
			}
			pages++
			if pages > 10 {
				t.Fatal("unbounded pagination")
			}
			if observed.IsZero() {
				observed = out.ObservedAt
			} else if !observed.Equal(out.ObservedAt) {
				t.Fatal("cursor refreshed time fence")
			}
			if len(out.Entries) != 1 {
				t.Fatalf("page %+v", out)
			}
			e := out.Entries[0]
			if seen[e.ID] {
				t.Fatalf("duplicate %s", e.ID)
			}
			seen[e.ID] = true
			kinds[e.Kind] = true
			if out.NextAfter == "" {
				break
			}
			o.After = out.NextAfter
		}
		if !kinds["policy_control"] || !kinds["policy_observation"] || !kinds["review_outcome"] {
			t.Fatalf("missing history %+v", kinds)
		}
		filtered, err := store.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{Workflow: run.WorkflowName, RunID: run.ID, PolicyVersion: review.PolicyVersion})
		if err != nil || len(filtered.Entries) != 1 || filtered.Entries[0].Kind != "review_outcome" {
			t.Fatalf("filters %+v %v", filtered, err)
		}
		if string(before) != string(state.PolicyChangeEvidence(t, s, app.ID)) {
			t.Fatal("history read mutated evidence")
		}
		first, err := store.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{After: first.NextAfter, Workflow: "another"}); !errors.Is(err, state.ErrEventRecoveryQuery) {
			t.Fatalf("cursor accepted changed filters %v", err)
		}
		if _, err = store.GetWorkflowRecoveryHistory(ctx, uuid.NewString(), app.ID, api.WorkflowRecoveryHistoryOptions{}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign history %v", err)
		}
		if _, err = store.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{After: "invalid"}); !errors.Is(err, state.ErrEventRecoveryQuery) {
			t.Fatalf("bad cursor %v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = store.GetWorkflowRecoveryHistory(cancelled, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{}); err == nil {
			t.Fatal("cancelled read succeeded")
		}
	})
}

func TestWorkflowRecoveryHistoryOrderingAndEvidence(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	account, app := uuid.NewString(), uuid.NewString()
	c, limit, err := state.RecoveryHistoryWindow(account, app, api.WorkflowRecoveryHistoryOptions{Limit: 1}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	page := state.RecoveryHistoryPage(c, limit, []api.WorkflowRecoveryHistoryEntry{{ID: "a", OccurredAt: at}, {ID: "b", OccurredAt: at}})
	if page.Entries[0].ID != "b" || page.NextAfter == "" {
		t.Fatalf("tie ordering %+v", page)
	}
	next, _, err := state.RecoveryHistoryWindow(account, app, api.WorkflowRecoveryHistoryOptions{After: page.NextAfter}, at.Add(2*time.Minute))
	if err != nil || state.RecoveryHistoryMatches(page.Entries[0], api.WorkflowRecoveryHistoryOptions{}, next) || !state.RecoveryHistoryMatches(api.WorkflowRecoveryHistoryEntry{ID: "a", OccurredAt: at}, api.WorkflowRecoveryHistoryOptions{}, next) {
		t.Fatalf("exclusive tie cursor %+v %v", next, err)
	}
	for _, o := range []api.WorkflowRecoveryHistoryOptions{{CreatedBefore: at.Add(time.Hour)}, {CreatedAfter: at.Add(-31 * 24 * time.Hour), CreatedBefore: at}, {After: page.NextAfter, CreatedBefore: at}} {
		if _, _, err := state.RecoveryHistoryWindow(account, app, o, at.Add(time.Minute)); !errors.Is(err, state.ErrEventRecoveryQuery) {
			t.Fatalf("accepted invalid window %+v: %v", o, err)
		}
	}
	entries := []api.WorkflowRecoveryHistoryEntry{}
	add := func(e api.WorkflowRecoveryHistoryEntry) { entries = append(entries, e) }
	a := api.WorkflowRecoveryAutomaticAction{ActionID: "action", ChildJobID: "child", Status: "applied", AppliedAt: &at, LastEvaluation: &api.WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: at, Reason: "child_created"}, Result: &api.WorkflowRecoveryReviewActionStatus{Outcome: "succeeded"}}
	state.RecoveryHistoryExecution(add, api.WorkflowRecoveryHistoryEntry{RunID: "run", ReviewID: "review", PolicyVersion: 2}, a)
	if len(entries) != 2 {
		t.Fatalf("fabricated result timestamp %+v", entries)
	}
	a.Result.RecordedAt = &at
	entries = nil
	state.RecoveryHistoryExecution(add, api.WorkflowRecoveryHistoryEntry{RunID: "run", ReviewID: "review", PolicyVersion: 2}, a)
	if len(entries) != 3 || entries[2].Kind != "retry_result" || entries[2].ChildJobID != "child" || entries[2].RunID != "run" || entries[2].PolicyVersion != 2 {
		t.Fatalf("lost evidence associations %+v", entries)
	}
}

func TestWorkflowRecoveryHistoryExecutionAndHealth(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		review, app, run, _ := state.SeedAutomaticRecoveryReview(t, s, nil)
		at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		actionID := api.WorkflowRecoveryResponseActionID(*review.Plan, review.Plan.Actions[0])
		child := uuid.NewString()
		evaluation := api.WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: at, Reason: "result_recorded"}
		result := api.WorkflowRecoveryReviewActionStatus{Complete: true, Outcome: "succeeded", RecordedAt: &at}
		incident := api.WorkflowRecoveryHealthIncident{EpisodeID: uuid.NewString(), OpenedAt: at, CandidateSince: at, ObservedAt: at, Status: "healthy", Reason: "queue_idle", ClosedAt: &at, AlertNotification: &api.WorkflowRecoveryHealthNotification{EventID: uuid.NewString(), Status: "no_receivers", CapturedAt: at}, RecoveryNotification: &api.WorkflowRecoveryHealthNotification{EventID: uuid.NewString(), Status: "no_receivers", CapturedAt: at}}
		if m, ok := s.(*state.MemStore); ok {
			(*m.Mu()).Lock()
			p := (*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)]
			a := p.Executions[actionID]
			if a == nil {
				(*m.Mu()).Unlock()
				t.Fatal("missing execution")
			}
			a.Status = "applied"
			a.ChildJobID = child
			a.AppliedAt = &at
			a.LastEvaluation = &evaluation
			a.Result = &result
			p.HealthIncident = &incident
			(*m.Mu()).Unlock()
		} else {
			pg := s.(*state.PgStore)
			ev, _ := json.Marshal(evaluation)
			res, _ := json.Marshal(result)
			health, _ := json.Marshal(incident)
			if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_recovery_policy_executions SET status='applied',child_job_id=$1,applied_at=$2,last_evaluation=$3,result=$4 WHERE action_id=$5", child, at, ev, res, actionID); err != nil {
				t.Fatal(err)
			}
			if _, err := (*pg.Pool()).Exec(ctx, "INSERT INTO workflow_recovery_health_incidents(app_id,incident) VALUES($1,$2)", app.ID, health); err != nil {
				t.Fatal(err)
			}
		}
		history := s.(state.WorkflowRecoveryHistoryStore)
		out, err := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		kinds := map[string]api.WorkflowRecoveryHistoryEntry{}
		for _, e := range out.Entries {
			kinds[e.Kind] = e
		}
		for _, kind := range []string{"retry_applied", "retry_evaluation", "retry_result", "health_observation", "health_alert", "health_recovered"} {
			e, ok := kinds[kind]
			if !ok || !e.OccurredAt.Equal(at) {
				t.Fatalf("missing timestamped %s: %+v", kind, out)
			}
			if kind == "retry_result" && (e.ChildJobID != child || !state.SameMemUUID(e.RunID, run.ID) || e.PolicyVersion != review.PolicyVersion) {
				t.Fatalf("lost child association %+v", e)
			}
			if kind == "health_alert" && (e.Reason != "" || e.PolicyVersion != 0) {
				t.Fatalf("inferred historical receipt evidence %+v", e)
			}
		}
		filtered, err := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{RunID: run.ID, PolicyVersion: review.PolicyVersion})
		if err != nil || len(filtered.Entries) != 3 {
			t.Fatalf("target filter %+v %v", filtered, err)
		}
	})
}
