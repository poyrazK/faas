package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"strings"
	"testing"
	"time"
)

func durableRecoveryEvents(t *testing.T, s Store, app string) []api.WorkflowRecoveryHistoryEntry {
	t.Helper()
	out := []api.WorkflowRecoveryHistoryEntry{}
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, e := range m.recoveryPolicies[canonicalMemUUID(app)].History {
			out = append(out, recoveryPolicyClone(e.Entry))
		}
		return out
	}
	rows, err := s.(*PgStore).pool.Query(context.Background(), "SELECT entry FROM workflow_recovery_history_events WHERE app_id=$1 ORDER BY sequence", app)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var e api.WorkflowRecoveryHistoryEntry
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorkflowRecoveryDurablePolicyHistory(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		review, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		policies := s.(WorkflowRecoveryPolicyStore)
		initial := durableRecoveryEvents(t, s, app.ID)
		if len(initial) != 2 || initial[0].Kind != "policy_changed" || initial[0].Policy == nil || !initial[0].Durable {
			t.Fatalf("initial policy event %+v", initial)
		}
		req := api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: review.PolicyVersion, Policy: review.Policy}
		req.Policy.Reason = "provider incident"
		req.Policy.CooldownSeconds = 3600
		updated, err := policies.SetWorkflowRecoveryPolicy(WithEventRecoveryActor(ctx, "account", "operator-42"), app.AccountID, app.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		req.Policy.Reason = "mutated caller"
		events := durableRecoveryEvents(t, s, app.ID)
		if len(events) != 3 || events[2].Reason != "provider incident" || events[2].ActorID != "operator-42" || events[2].Policy.CooldownSeconds != 3600 || events[0].Policy.CooldownSeconds != review.Policy.CooldownSeconds || events[0].ID == events[2].ID {
			t.Fatalf("snapshots %+v", events)
		}
		if _, err = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
			t.Fatalf("stale config %v", err)
		}
		if len(durableRecoveryEvents(t, s, app.ID)) != 3 {
			t.Fatal("failed mutation appended history")
		}
		// Audit links have no cascading reference to the pruned source review/job.
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			forgetRecoveryPolicyReview(m.recoveryPolicies[canonicalMemUUID(app.ID)], review.ID)
			delete(m.eventRecoveryJobs, canonicalMemUUID(review.JobID))
			m.mu.Unlock()
		} else {
			pg := s.(*PgStore)
			if _, err = pg.pool.Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1", review.JobID); err != nil {
				t.Fatal(err)
			}
			s = NewPgStore(pg.pool)
		}
		out, err := s.(WorkflowRecoveryHistoryStore).GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{PolicyVersion: 1})
		if err != nil || len(out.Entries) != 1 || out.Entries[0].ID != events[0].ID || out.Entries[0].Policy == nil {
			t.Fatalf("pruned source/restart lost event %+v %v", out, err)
		}
		if updated.Version != 2 {
			t.Fatalf("version %d", updated.Version)
		}
		// Reads neither prune nor alter evidence; expired records are invisible.
		aged := time.Now().UTC().Add(-api.WorkflowRecoveryHistoryRetention - time.Hour)
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
		out, err = s.(WorkflowRecoveryHistoryStore).GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{PolicyVersion: 1})
		if err != nil || len(out.Entries) != 0 || len(durableRecoveryEvents(t, s, app.ID)) != 3 {
			t.Fatalf("expired read %+v %v", out, err)
		}
		n, err := s.(WorkflowRecoveryHistoryRetentionStore).PruneWorkflowRecoveryHistory(ctx, time.Now().UTC())
		if err != nil || n != 3 {
			t.Fatalf("retention %d %v", n, err)
		}
		if n, err = s.(WorkflowRecoveryHistoryRetentionStore).PruneWorkflowRecoveryHistory(ctx, time.Now().UTC()); err != nil || n != 0 {
			t.Fatalf("repeat retention %d %v", n, err)
		}
	})
}

func TestWorkflowRecoveryDurableReasonTransitions(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		_, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		now = now.Truncate(time.Second).Add(123456789 * time.Nanosecond)
		runAutomaticPolicy(t, s, now)
		runAutomaticPolicy(t, s, now.Add(time.Minute))
		first := durableRecoveryEvents(t, s, app.ID)
		if len(first) != 4 || first[2].Reason != "child_created" || first[2].ChildJobID == "" || first[2].Outcome != "applied" || first[3].Reason != "admission_wait" || first[3].ActionID != first[2].ActionID || first[2].RunID == "" || first[2].Workflow == "" {
			t.Fatalf("transitions %+v", first)
		}
		if pg, ok := s.(*PgStore); ok {
			s = NewPgStore(pg.pool)
		}
		runAutomaticPolicy(t, s, now.Add(2*time.Minute))
		if got := durableRecoveryEvents(t, s, app.ID); len(got) != len(first) {
			t.Fatalf("repeat poll appended %+v", got)
		}

		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			forgetRecoveryPolicyReview(m.recoveryPolicies[canonicalMemUUID(app.ID)], first[2].ReviewID)
			delete(m.eventRecoveryJobs, canonicalMemUUID(first[2].JobID))
			m.mu.Unlock()
		} else {
			if _, err := s.(*PgStore).pool.Exec(context.Background(), "DELETE FROM event_recovery_jobs WHERE id=$1", first[2].JobID); err != nil {
				t.Fatal(err)
			}
		}
		after := durableRecoveryEvents(t, s, app.ID)
		if len(after) != len(first) || after[2].ChildJobID != first[2].ChildJobID || after[2].ReviewID != first[2].ReviewID {
			t.Fatalf("source pruning lost retry lineage %+v", after)
		}
	})
}

func TestWorkflowRecoveryDurableHealthEpisodes(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		_, app, _, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
			p.HealthAlerts = &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}
		})
		policies := s.(WorkflowRecoveryPolicyStore)
		operator := s.(WorkflowRecoveryPolicyOperatorStore)
		control := func(operation string) {
			p, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(p, operation)); err != nil {
				t.Fatal(err)
			}
		}
		worker := s.(WorkflowRecoveryHealthAlertStore)
		tick := func(at time.Time) {
			if did, err := worker.ProcessNextWorkflowRecoveryHealthAlert(ctx, at); err != nil || !did {
				t.Fatalf("health tick %t %v", did, err)
			}
		}
		control("pause")
		tick(now)
		tick(now.Add(time.Minute))
		tick(now.Add(2 * time.Minute))
		events := durableRecoveryEvents(t, s, app.ID)
		if len(events) != 4 || events[2].Kind != "health_opened" || events[3].Kind != "health_alert_captured" || events[3].Reason != "operator: operator investigation" || events[3].PolicyVersion != 1 {
			t.Fatalf("opening/capture %+v", events)
		}
		// Opt out closes the episode without changing its captured historical reason.
		p, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		p.Policy.HealthAlerts.Enabled = false
		if _, err = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
			t.Fatal(err)
		}
		tick(now.Add(3 * time.Minute))
		p, err = policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		p.Policy.HealthAlerts.Enabled = true
		if _, err = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
			t.Fatal(err)
		}
		control("pause")
		tick(now.Add(4 * time.Minute))
		events = durableRecoveryEvents(t, s, app.ID)
		var episodes []string
		closed := false
		for _, e := range events {
			if e.Kind == "health_opened" {
				episodes = append(episodes, e.EpisodeID)
			}
			if e.Kind == "health_closed" && e.Reason == "health_alerts_disabled" {
				closed = true
			}
		}
		if len(episodes) != 2 || episodes[0] == episodes[1] || !closed || events[2].Reason != "operator: operator investigation" {
			t.Fatalf("replaced episode lost evidence %+v", events)
		}
	})
}

func TestWorkflowRecoveryDurableHistoryCapacity(t *testing.T) {
	now := time.Now().UTC()
	p := &memWorkflowRecoveryPolicy{}
	for i := 0; i < api.WorkflowRecoveryHistoryEventsMax; i++ {
		p.History = append(p.History, memRecoveryHistoryEvent{Sequence: int64(i + 1), RecordedAt: now, Entry: api.WorkflowRecoveryHistoryEntry{ID: fmt.Sprint(i)}})
	}
	p.HistorySequence = int64(api.WorkflowRecoveryHistoryEventsMax)
	p.appendHistoryEvent(api.WorkflowRecoveryHistoryEntry{Kind: "policy_changed", OccurredAt: now}, now)
	if len(p.History) != api.WorkflowRecoveryHistoryEventsMax || p.History[0].Entry.ID != "1" || p.History[len(p.History)-1].Sequence != int64(api.WorkflowRecoveryHistoryEventsMax+1) {
		t.Fatal("capacity did not evict oldest append")
	}
}

func TestWorkflowRecoveryDurableHistoryAtomicFailures(t *testing.T) {
	for _, kind := range []string{"policy_changed", "retry_transition", "health_opened", "candidates_observed"} {
		t.Run(kind, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				pg, ok := s.(*PgStore)
				if !ok {
					t.Skip("Postgres failure injection")
				}
				ctx := context.Background()
				_, app, _, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
					p.HealthAlerts = &api.WorkflowRecoveryHealthAlertPolicy{Enabled: true, StallAfterSeconds: 300, AlertAfterSeconds: 60}
				})
				policies := s.(WorkflowRecoveryPolicyStore)
				p, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "health_opened" {
					if _, err = s.(WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(p, "pause")); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "retry_transition" {
					// The next failed poll must capture an alert. Earlier failures
					// only retain bounded polling backoff after rolling back work.
					failure := nextAutomaticPollingFailure(nil, "invalid_polling_data", now)
					failure.Attempts = api.WorkflowRecoveryPollingAlertAttempts - 1
					raw, e := json.Marshal(failure)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = pg.pool.Exec(ctx, "UPDATE workflow_recovery_policies SET automatic_poll_failure=$2 WHERE app_id=$1::uuid", app.ID, raw); e != nil {
						t.Fatal(e)
					}
				}
				before := policyChangeEvidence(t, s, app.ID)
				count := len(durableRecoveryEvents(t, s, app.ID))
				// An append failure must roll back policy replacement, child admission and incident state.
				// Retry polling may retain a failure alert after rolling back its work.
				// Reject that audit append too: without either audit the entire poll must roll back.
				constraint := map[string]string{"policy_changed": "CHECK(entry->>'kind'<>'policy_changed' OR (entry->>'policy_version')::bigint=1)", "retry_transition": "CHECK(entry->>'kind' NOT IN ('retry_transition','polling_failed'))", "health_opened": "CHECK(entry->>'kind'<>'health_opened')", "candidates_observed": "CHECK(entry->>'kind'<>'candidates_observed') NOT VALID"}[kind]
				if _, err = pg.pool.Exec(ctx, "ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT durable_failure_test "+constraint); err != nil {
					t.Fatal(err)
				}
				defer func() {
					_, _ = pg.pool.Exec(context.Background(), "ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT IF EXISTS durable_failure_test")
				}()
				mutate := func() error {
					switch kind {
					case "policy_changed":
						_, e := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
						return e
					case "candidates_observed":
						_, e := policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(time.Minute))
						return e
					case "retry_transition":
						_, e := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now)
						return e
					default:
						_, e := s.(WorkflowRecoveryHealthAlertStore).ProcessNextWorkflowRecoveryHealthAlert(ctx, now)
						return e
					}
				}
				if err = mutate(); err == nil {
					t.Fatal("audit failure accepted")
				}
				if string(before) != string(policyChangeEvidence(t, s, app.ID)) || len(durableRecoveryEvents(t, s, app.ID)) != count {
					t.Fatal("audit failure partially committed")
				}
				if kind == "health_opened" {
					queue, e := s.(WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
					if e != nil || queue.HealthIncident != nil {
						t.Fatalf("incident partially committed %+v %v", queue.HealthIncident, e)
					}
				}
				if _, err = pg.pool.Exec(ctx, "ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT durable_failure_test"); err != nil {
					t.Fatal(err)
				}
				if err = mutate(); err != nil {
					t.Fatal(err)
				}
				if len(durableRecoveryEvents(t, s, app.ID)) != count+1 {
					t.Fatal("retry duplicated or lost audit event")
				}
			})
		})
	}
}

func TestWorkflowRecoveryDurableHistoryCursorFence(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		_, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		history := s.(WorkflowRecoveryHistoryStore)
		first, err := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{Limit: 1})
		if err != nil || first.NextAfter == "" {
			t.Fatalf("first page %+v %v", first, err)
		}
		// Simulate an event with an older occurrence/recording time committing after page one.
		late := api.WorkflowRecoveryHistoryEntry{OccurredAt: first.ObservedAt.Add(-time.Minute).Truncate(time.Microsecond), Kind: "retry_transition", Outcome: "pending", Reason: "late_commit", PolicyVersion: 1}
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			m.recoveryPolicies[canonicalMemUUID(app.ID)].appendHistoryEvent(late, first.ObservedAt.Add(-time.Second))
			m.mu.Unlock()
		} else {
			id := canonicalMemUUID(newID())
			late.ID = "audit:" + id
			late.Durable = true
			raw, _ := json.Marshal(late)
			if _, err = s.(*PgStore).pool.Exec(ctx, "INSERT INTO workflow_recovery_history_events(id,app_id,occurred_at,recorded_at,entry) VALUES($1,$2,$3,$4,$5)", id, app.ID, late.OccurredAt, first.ObservedAt.Add(-time.Second), raw); err != nil {
				t.Fatal(err)
			}
		}
		cursor := first.NextAfter
		for pages := 0; cursor != ""; pages++ {
			if pages > 10 {
				t.Fatal("unbounded cursor")
			}
			page, e := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{After: cursor, Limit: 1})
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range page.Entries {
				if entry.Reason == "late_commit" {
					t.Fatal("later append entered fenced traversal")
				}
			}
			cursor = page.NextAfter
		}
		fresh, err := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range fresh.Entries {
			if entry.Reason == "late_commit" {
				found = true
			}
		}
		if !found {
			t.Fatal("fresh traversal lost committed event")
		}
	})
}

func TestWorkflowRecoveryDurableHistoryPostgresCapacity(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("Postgres cap")
		}
		ctx := context.Background()
		review, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		at := time.Now().UTC().Add(-time.Minute)
		if _, err := pg.pool.Exec(ctx, `WITH ids AS (SELECT gen_random_uuid() AS id FROM generate_series(1,$1::integer))
INSERT INTO workflow_recovery_history_events(id,app_id,occurred_at,recorded_at,entry)
SELECT id,$2,$3,$3,jsonb_build_object('id','audit:'||id::text,'durable',true,'kind','retry_transition','outcome','pending','reason','fixture','policy_version',1,'occurred_at',$3::timestamptz) FROM ids`, api.WorkflowRecoveryHistoryEventsMax, app.ID, at); err != nil {
			t.Fatal(err)
		}
		if _, err := s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: review.PolicyVersion, Policy: review.Policy}); err != nil {
			t.Fatal(err)
		}
		events := durableRecoveryEvents(t, s, app.ID)
		if len(events) != api.WorkflowRecoveryHistoryEventsMax || events[0].Kind != "retry_transition" || events[len(events)-1].PolicyVersion != 2 {
			t.Fatalf("retention capacity %d", len(events))
		}
	})
}

func TestWorkflowRecoveryDurableMigrationGuard(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("Postgres migration guard")
		}
		ctx := context.Background()
		_, app, _, _ := seedAutomaticRecoveryReview(t, s, nil)
		source, err := os.ReadFile("../../migrations/20261011161000001_workflow_recovery_durable_history.sql")
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(string(source), "-- +goose Down")
		up, down := strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
		tx, err := pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, down); err == nil {
			_ = tx.Rollback(ctx)
			t.Fatal("downgrade discarded retained history")
		}
		_ = tx.Rollback(ctx)
		if len(durableRecoveryEvents(t, s, app.ID)) != 2 {
			t.Fatal("guard lost event")
		}
		tx, err = pg.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, "DELETE FROM workflow_recovery_history_events"); err != nil {
			t.Fatal(err)
		}
		for _, ddl := range []string{down, up, down, up} {
			if _, err = tx.Exec(ctx, ddl); err != nil {
				t.Fatalf("empty migration cycle %v", err)
			}
		}
		// Rollback restores the original table and evidence after the empty up/down cycles.
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if len(durableRecoveryEvents(t, s, app.ID)) != 2 {
			t.Fatal("migration cycle lost event")
		}
	})
}

func TestWorkflowRecoveryDurableChildResultAndPause(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		review, app, run, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) { p.PauseAfterFailures = 1 })
		runAutomaticPolicy(t, s, now)
		failAutomaticChild(t, s, run)
		runAutomaticPolicy(t, s, now.Add(time.Minute))
		retained, err := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
		if err != nil {
			t.Fatal(err)
		}
		result := retained.AutomaticExecution.Actions[0].Result
		if result == nil || result.Outcome != "failed" {
			t.Fatalf("fixture result %+v", result)
		}
		events := durableRecoveryEvents(t, s, app.ID)
		found, pause := false, false
		for _, e := range events {
			if e.EvaluationReason == "result_recorded" {
				found = true
				if e.Outcome != result.Outcome || e.Reason != result.OutcomeReason || e.ChildJobID != result.ChildJobID || e.ActorKind != "system" {
					t.Fatalf("lost final proof %+v", e)
				}
			}
			if e.Kind == "policy_paused" {
				pause = true
				if e.Reason != "consecutive_failures" || e.PolicyVersion != review.PolicyVersion {
					t.Fatalf("lost pause cause %+v", e)
				}
			}
		}
		if !found || !pause {
			t.Fatalf("missing final result/pause %+v", events)
		}
	})
}
