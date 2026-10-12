package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestWorkflowRecoveryAutomaticOutstandingWait(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		review, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		runAutomaticPolicy(t, s, now)
		// Retain a distinct pending plan behind the committed child. Its target
		// is deliberately never revalidated while that child holds the slot.
		pending := recoveryPolicyClone(review)
		pending.ID = uuid.NewString()
		pending.ObservedAt = now.Add(time.Second)
		pending.Plan.Key += "-waiting"
		pending.AutomaticExecution = nil
		id := api.WorkflowRecoveryResponseActionID(*pending.Plan, pending.Plan.Actions[0])
		pending.Plan.Actions[0].Retry.RequestID = id
		raw, err := json.Marshal(pending)
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := s.(*MemStore); ok {
			m.mu.Lock()
			p := m.recoveryPolicies[canonicalMemUUID(app.ID)]
			p.Reviews[pending.ID] = raw
			p.Executions[id] = &api.WorkflowRecoveryAutomaticAction{ActionID: id, Position: pending.Plan.Actions[0].Position, Status: "pending", Reason: "awaiting_execution"}
			m.mu.Unlock()
		} else {
			pg := s.(*PgStore)
			q := sqlc.New()
			if _, err = q.WorkflowRecoveryPolicyReviewInsert(ctx, pg.pool, sqlc.WorkflowRecoveryPolicyReviewInsertParams{ID: mustPgUUID(pending.ID), AppID: mustPgUUID(app.ID), JobID: mustPgUUID(pending.JobID), Fingerprint: id, Review: raw, NowAt: pgtypeFromTime(pending.ObservedAt)}); err != nil {
				t.Fatal(err)
			}
			if err = q.WorkflowRecoveryAutomaticExecutionInsert(ctx, pg.pool, sqlc.WorkflowRecoveryAutomaticExecutionInsertParams{ActionID: mustPgUUID(id), AppID: mustPgUUID(app.ID), ReviewID: mustPgUUID(pending.ID), PolicyVersion: pending.PolicyVersion, Position: pending.Plan.Actions[0].Position}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 1; i <= 2; i++ {
			at := now.Add(time.Duration(i) * time.Minute)
			runAutomaticPolicy(t, s, at)
			out, err := s.(WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if out.Pending != 1 || out.Outstanding != 1 || out.HourlyExecutions != 1 || len(out.Entries) != 2 {
				t.Fatalf("slot or charge changed %+v", out)
			}
			e := out.Entries[1].Action.LastEvaluation
			if e == nil || e.Reason != "outstanding_retry" || !e.EvaluatedAt.Equal(at) || e.WaitingSince == nil || !e.WaitingSince.Equal(now.Add(time.Minute)) {
				t.Fatalf("slot wait %+v", e)
			}
			if out.Entries[1].Action.Status != "pending" || out.Entries[1].Action.ChildJobID != "" {
				t.Fatal("wait authorized a child")
			}
		}
	})
}
