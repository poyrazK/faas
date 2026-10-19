package state

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryAutomaticEvaluationLifecycle(t *testing.T) {
	for _, scenario := range []string{"pending_unknown", "applied_unknown", "completed"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				review, app, run, now := seedAutomaticRecoveryReview(t, s, nil)
				queue := func() api.WorkflowRecoveryAutomaticQueue {
					out, err := s.(WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					return out
				}
				initial := queue()
				if len(initial.Entries) != 1 || !initial.Entries[0].QueuedAt.Truncate(time.Microsecond).Equal(review.ObservedAt.Truncate(time.Microsecond)) || initial.Entries[0].Action.LastEvaluation != nil || initial.Entries[0].WaitingAgeSeconds != nil {
					t.Fatalf("unevaluated queue %+v", initial)
				}
				if scenario == "pending_unknown" {
					removeAutomaticLiveRun(t, s, run)
				}
				runAutomaticPolicy(t, s, now)
				want := "child_created"
				if scenario == "pending_unknown" {
					want = "evidence_unknown"
				}
				first := queue().Entries[0].Action.LastEvaluation
				if first == nil || first.Reason != want || !first.EvaluatedAt.Equal(now) {
					t.Fatalf("first %+v", first)
				}
				if scenario != "pending_unknown" && first.WaitingSince != nil {
					t.Fatalf("creation recorded as waiting %+v", first)
				}
				runAutomaticPolicy(t, s, now.Add(time.Minute))
				if scenario != "pending_unknown" {
					want = "admission_wait"
				}
				waiting := queue().Entries[0].Action.LastEvaluation
				if waiting.Reason != want || waiting.WaitingSince == nil {
					t.Fatalf("waiting %+v", waiting)
				}
				if pg, ok := s.(*PgStore); ok {
					s = NewPgStore(pg.pool.Pool)
				}
				runAutomaticPolicy(t, s, now.Add(2*time.Minute))
				out := queue()
				repeated := out.Entries[0].Action.LastEvaluation
				if !repeated.EvaluatedAt.Equal(now.Add(2*time.Minute)) || !repeated.WaitingSince.Equal(*waiting.WaitingSince) || out.Entries[0].WaitingAgeSeconds == nil {
					t.Fatalf("wait reset across polls/restart %+v", out)
				}
				if !reflect.DeepEqual(repeated, queue().Entries[0].Action.LastEvaluation) {
					t.Fatal("read advanced evaluation")
				}
				if scenario == "completed" {
					failAutomaticChild(t, s, run)
					runAutomaticPolicy(t, s, now.Add(3*time.Minute))
					retained, err := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if err != nil {
						t.Fatal(err)
					}
					a := retained.AutomaticExecution.Actions[0]
					if a.Result == nil || a.LastEvaluation.Reason != "result_recorded" || a.LastEvaluation.WaitingSince != nil || queue().Outstanding != 0 {
						t.Fatalf("final proof %+v", a)
					}
					return
				}
				if scenario == "applied_unknown" {
					if _, err := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					removeAutomaticLiveRun(t, s, run)
					runAutomaticPolicy(t, s, now.Add(3*time.Minute))
					out = queue()
					repeated = out.Entries[0].Action.LastEvaluation
					if repeated.Reason != "evidence_unknown" || !repeated.WaitingSince.Equal(now.Add(3*time.Minute)) || out.Outstanding != 1 || out.HourlyExecutions != 1 {
						t.Fatalf("new reason or slot %+v", out)
					}
				}
				p, err := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.(WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(p, "pause")); err != nil {
					t.Fatal(err)
				}
				runAutomaticPolicy(t, s, now.Add(time.Hour))
				if !reflect.DeepEqual(repeated, queue().Entries[0].Action.LastEvaluation) {
					t.Fatal("pause advanced evaluation")
				}
			})
		})
	}
}

func TestWorkflowRecoveryAutomaticQueueAges(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	since := now.Add(-90 * time.Second)
	for _, tc := range []struct {
		name      string
		queued    time.Time
		waiting   *time.Time
		wantQueue int64
		wantWait  *int64
	}{
		{"elapsed", now.Add(-10 * time.Minute), &since, 600, ptrQueueAge(90)},
		{"never_evaluated", now, nil, 0, nil},
		{"future_clock", now.Add(time.Minute), ptrQueueTime(now.Add(time.Minute)), 0, ptrQueueAge(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := api.WorkflowRecoveryAutomaticQueue{ObservedAt: now, Pending: 1, Entries: []api.WorkflowRecoveryAutomaticQueueEntry{{QueuedAt: tc.queued, Action: api.WorkflowRecoveryAutomaticAction{LastEvaluation: &api.WorkflowRecoveryAutomaticEvaluation{WaitingSince: tc.waiting}}}}}
			recoveryQueueFinish(&out, nil)
			e := out.Entries[0]
			if e.QueuedAgeSeconds != tc.wantQueue || !reflect.DeepEqual(e.WaitingAgeSeconds, tc.wantWait) {
				t.Fatalf("ages %+v", e)
			}
		})
	}
}

func ptrQueueAge(v int64) *int64          { return &v }
func ptrQueueTime(v time.Time) *time.Time { return &v }
