package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"reflect"
	"sync"
	"testing"
	"time"
)

func controlRequest(p api.WorkflowRecoveryPolicyResponse, op string) api.ControlWorkflowRecoveryPolicyRequest {
	r := api.ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Operation: op, Reason: "operator investigation"}
	if p.AutomaticExecution != nil {
		r.ExpectedPausedAt = p.AutomaticExecution.PausedAt
	}
	return r
}
func TestWorkflowRecoveryOperatorControls(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		review, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		operator := s.(WorkflowRecoveryPolicyOperatorStore)
		policies := s.(WorkflowRecoveryPolicyStore)
		ctx := WithEventRecoveryActor(context.Background(), "account", app.AccountID)
		queue, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil || queue.Pending != 1 || queue.Outstanding != 0 || queue.RemainingBudget != 10 || len(queue.Entries) != 1 || queue.Entries[0].ReviewID != review.ID {
			t.Fatalf("initial queue: %+v %v", queue, err)
		}
		p := queue.Policy
		r := controlRequest(p, "pause")
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, r)
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		accepted, conflicts := 0, 0
		for e := range errs {
			if e == nil {
				accepted++
			} else if errors.Is(e, ErrWorkflowRecoveryPolicyConflict) {
				conflicts++
			} else {
				t.Fatal(e)
			}
		}
		if accepted != 1 || conflicts != 1 {
			t.Fatalf("concurrent controls accepted=%d conflicts=%d", accepted, conflicts)
		}
		paused, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if paused.Version != p.Version || !reflect.DeepEqual(paused.Policy, p.Policy) || paused.Status != "paused" || paused.ActorID != app.AccountID || paused.AutomaticExecution.PausedAt == nil {
			t.Fatalf("pause changed policy: %+v", paused)
		}
		if processed, e := s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); e != nil || processed {
			t.Fatalf("paused worker: %t %v", processed, e)
		}
		if _, e := operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(p, "resume")); !errors.Is(e, ErrWorkflowRecoveryPolicyConflict) {
			t.Fatalf("stale resume: %v", e)
		}
		resumed, err := operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(paused, "resume"))
		if err != nil {
			t.Fatal(err)
		}
		if resumed.Version != p.Version || resumed.AutomaticExecution.PausedAt != nil || resumed.AutomaticExecution.ConsecutiveFailures != 0 {
			t.Fatalf("resume: %+v", resumed)
		}
		runAutomaticPolicy(t, s, now)
		queue, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil || queue.Pending != 0 || queue.Outstanding != 1 || queue.HourlyExecutions != 1 || queue.RemainingBudget != 9 || queue.Entries[0].Action.ChildJobID == "" || queue.CooldownUntil == nil {
			t.Fatalf("applied queue: %+v %v", queue, err)
		}
		// Controls leave an already committed child and its charge intact.
		if _, err = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(resumed, "pause")); err != nil {
			t.Fatal(err)
		}
		after, e := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if e != nil || after.Outstanding != 1 || after.HourlyExecutions != 1 {
			t.Fatalf("pause committed child: %+v %v", after, e)
		}
		if _, e = operator.GetWorkflowRecoveryAutomaticQueue(ctx, uuid.NewString(), app.ID); !errors.Is(e, ErrNotFound) {
			t.Fatalf("foreign queue: %v", e)
		}
		if _, e = operator.ControlWorkflowRecoveryPolicy(ctx, uuid.NewString(), app.ID, controlRequest(after.Policy, "resume")); !errors.Is(e, ErrNotFound) {
			t.Fatalf("foreign control: %v", e)
		}
		switch st := s.(type) {
		case *MemStore:
			if len(st.recoveryPolicies[canonicalMemUUID(app.ID)].Controls) != 3 {
				t.Fatal("missing control audit")
			}
		case *PgStore:
			rows, e := sqlc.New().WorkflowRecoveryPolicyControlAudit(ctx, st.pool, mustPgUUID(app.ID))
			if e != nil || len(rows) != 3 || rows[0].Reason != "operator investigation" || rows[0].ActorID != app.AccountID {
				t.Fatalf("audit: %+v %v", rows, e)
			}
		}
	})
}
func TestWorkflowRecoveryOperatorFailurePauseFence(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		_, app, run, now := seedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) { p.PauseAfterFailures = 1 })
		operator := s.(WorkflowRecoveryPolicyOperatorStore)
		ctx := context.Background()
		before, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		runAutomaticPolicy(t, s, now)
		failAutomaticChild(t, s, run)
		runAutomaticPolicy(t, s, now.Add(time.Minute))
		if _, e = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(before, "resume")); !errors.Is(e, ErrWorkflowRecoveryPolicyConflict) {
			t.Fatalf("cleared unseen failure pause: %v", e)
		}
		queue, e := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if e != nil || queue.Policy.Status != "paused" || queue.Policy.AutomaticExecution.ConsecutiveFailures != 1 {
			t.Fatalf("failure pause: %+v %v", queue, e)
		}
		if _, e = operator.ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(queue.Policy, "resume")); e != nil {
			t.Fatal(e)
		}
	})
}

func TestWorkflowRecoveryOperatorPauseExecutionFence(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		pg, ok := s.(*PgStore)
		if !ok {
			t.Skip("memory control and application share one mutex")
		}
		review, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		ctx := context.Background()
		p, e := pg.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		tx, e := pg.pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, e = sqlc.New().WorkflowRecoveryPolicyControl(ctx, tx, sqlc.WorkflowRecoveryPolicyControlParams{ControlID: mustPgUUID(uuid.NewString()), AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), ExpectedVersion: p.Version, ExpectedUpdatedAt: pgtypeFromTime(p.UpdatedAt), Operation: "pause", Reason: "investigate", NowAt: pgtypeFromTime(now), ActorKind: "account", ActorID: app.AccountID})
		if e != nil {
			t.Fatal(e)
		}
		// The worker can see the previous unpaused snapshot, but cannot claim
		// the policy row while an uncommitted operator pause owns its lock.
		if did, e := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); e != nil || did {
			t.Fatalf("worker crossed pause lock: %t %v", did, e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		runAutomaticPolicy(t, s, now.Add(time.Minute))
		if _, e = automaticChild(t, s, app, review); !errors.Is(e, ErrNotFound) {
			t.Fatalf("paused child created: %v", e)
		}
		queue, e := pg.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if e != nil || queue.Pending != 1 || queue.HourlyExecutions != 0 || queue.Policy.Status != "paused" {
			t.Fatalf("pause queue: %+v %v", queue, e)
		}
	})
}
