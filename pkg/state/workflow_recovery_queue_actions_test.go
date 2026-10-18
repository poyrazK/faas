package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"reflect"
	"sync"
	"testing"
	"time"
)

func removeAutomaticLiveRun(t *testing.T, s Store, run *WorkflowRun) {
	t.Helper()
	if m, ok := s.(*MemStore); ok {
		m.mu.Lock()
		delete(m.workflowRuns, run.ID)
		m.mu.Unlock()
	} else {
		if _, err := s.(*PgStore).pool.Exec(context.Background(), "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); err != nil {
			t.Fatal(err)
		}
	}
}
func TestWorkflowRecoveryAutomaticSuppression(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		review, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		ctx := WithEventRecoveryActor(context.Background(), "account", app.AccountID)
		operator := s.(WorkflowRecoveryAutomaticActionOperatorStore)
		id := review.AutomaticExecution.Actions[0].ActionID
		req := api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "provider recovery handled externally"}
		out, err := operator.SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, req)
		if err != nil || out.Status != "suppressed" || out.Suppression == nil || out.Suppression.ActorID != app.AccountID || out.Suppression.Reason != req.Reason {
			t.Fatalf("suppression %+v %v", out, err)
		}
		replay, err := operator.SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, req)
		if err != nil || !reflect.DeepEqual(out, replay) {
			t.Fatalf("replay %+v %v", replay, err)
		}
		changed := req
		changed.Reason = "changed reason"
		if _, err = operator.SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, changed); !errors.Is(err, ErrEventRecoveryChanged) {
			t.Fatalf("changed reason %v", err)
		}
		if _, err = operator.SuppressWorkflowRecoveryAutomaticAction(ctx, uuid.NewString(), app.ID, review.ID, id, req); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign %v", err)
		}
		if _, err = operator.SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, uuid.NewString(), id, req); !errors.Is(err, ErrNotFound) {
			t.Fatalf("wrong review %v", err)
		}
		runAutomaticPolicy(t, s, now)
		if _, err = automaticChild(t, s, app, review); !errors.Is(err, ErrNotFound) {
			t.Fatalf("suppressed child %v", err)
		}
		queue, err := s.(WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
		if err != nil || queue.Pending != 0 || queue.HourlyExecutions != 0 {
			t.Fatalf("suppressed queue %+v %v", queue, err)
		}
		retained, err := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
		if err != nil || !reflect.DeepEqual(retained.AutomaticExecution.Actions[0].Suppression, out.Suppression) {
			t.Fatalf("audit missing %+v %v", retained, err)
		}
		// The operator suppresses automatic application, while manual recovery remains available.
		if _, err = s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, *review.Plan.Actions[0].Retry); err != nil {
			t.Fatal(err)
		}
		if _, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: review.PolicyVersion, Policy: review.Policy}); err != nil {
			t.Fatal(err)
		}
		if _, err = operator.SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, req); !errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
			t.Fatalf("obsolete suppression %v", err)
		}
	})
}
func TestWorkflowRecoveryAutomaticSuppressionExecutionRace(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		review, app, _, now := seedAutomaticRecoveryReview(t, s, nil)
		ctx := context.Background()
		id := review.AutomaticExecution.Actions[0].ActionID
		var suppressed api.WorkflowRecoveryAutomaticAction
		var supErr, workErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			suppressed, supErr = s.(WorkflowRecoveryAutomaticActionOperatorStore).SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "stop this retry"})
		}()
		go func() {
			defer wg.Done()
			_, workErr = s.(WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now)
		}()
		wg.Wait()
		if workErr != nil {
			t.Fatal(workErr)
		}
		child, childErr := automaticChild(t, s, app, review)
		if supErr == nil {
			if suppressed.Status != "suppressed" || !errors.Is(childErr, ErrNotFound) {
				t.Fatalf("suppression crossed creation %+v %+v %v", suppressed, child, childErr)
			}
		} else if !errors.Is(supErr, ErrEventRecoveryChanged) || childErr != nil {
			t.Fatalf("creation winner sup=%v child=%v", supErr, childErr)
		}
		if childErr == nil {
			if _, err := s.(WorkflowRecoveryAutomaticActionOperatorStore).SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "stop this retry"}); !errors.Is(err, ErrEventRecoveryChanged) {
				t.Fatalf("applied suppression %v", err)
			}
			queue, err := s.(WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
			if err != nil || queue.Outstanding != 1 || queue.HourlyExecutions != 1 {
				t.Fatalf("committed retry changed %+v %v", queue, err)
			}
		}
	})
}
func TestWorkflowRecoveryAutomaticBlockedNotifications(t *testing.T) {
	for _, scenario := range []string{"pending", "applied", "no_receivers", "running", "stale_pending", "paused", "dismissed"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				review, app, run, now := seedAutomaticRecoveryReview(t, s, nil)
				ctx := context.Background()
				id := review.AutomaticExecution.Actions[0].ActionID
				var hook AppWebhook
				var err error
				if scenario != "no_receivers" {
					hook, err = s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://blocked.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryQueueBlocked)}})
					if err != nil {
						t.Fatal(err)
					}
				}
				switch scenario {
				case "applied", "running":
					runAutomaticPolicy(t, s, now)
					if _, err = s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					if scenario == "applied" {
						removeAutomaticLiveRun(t, s, run)
					}
				case "stale_pending":
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						m.recoveryPolicies[canonicalMemUUID(app.ID)].Executions[id].Reason = "evidence_unknown"
						m.mu.Unlock()
					} else {
						if _, err = s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_policy_executions SET reason='evidence_unknown' WHERE action_id=$1::uuid", id); err != nil {
							t.Fatal(err)
						}
					}
				default:
					removeAutomaticLiveRun(t, s, run)
					runAutomaticPolicy(t, s, now)
					if scenario == "dismissed" {
						if _, err = s.(WorkflowRecoveryPolicyStore).DecideWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowRecoveryReviewDecisionRequest{Decision: "dismissed", Reason: "handled externally"}); err != nil {
							t.Fatal(err)
						}
					}
					if scenario == "paused" {
						p, e := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
						if e != nil {
							t.Fatal(e)
						}
						if _, e = s.(WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, controlRequest(p, "pause")); e != nil {
							t.Fatal(e)
						}
					}
				}
				worker := s.(WorkflowRecoveryAutomaticBlockedStore)
				errs := make(chan error, 2)
				for i := 0; i < 2; i++ {
					go func() {
						_, err := worker.ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx, now.Add(time.Minute))
						errs <- err
					}()
				}
				for i := 0; i < 2; i++ {
					if err = <-errs; err != nil {
						t.Fatal(err)
					}
				}
				if pg, ok := s.(*PgStore); ok {
					s = NewPgStore(pg.pool.Pool)
					worker = s.(WorkflowRecoveryAutomaticBlockedStore)
				}
				retained, err := s.(WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if err != nil {
					t.Fatal(err)
				}
				action := retained.AutomaticExecution.Actions[0]
				quiet := scenario == "running" || scenario == "stale_pending" || scenario == "paused" || scenario == "dismissed"
				if quiet {
					if action.BlockedNotification != nil {
						t.Fatalf("nonblocking notification %+v", action)
					}
					return
				}
				receipt := action.BlockedNotification
				if receipt == nil || receipt.Reason != "evidence_unknown" || action.Result != nil {
					t.Fatalf("missing block or false final proof %+v", action)
				}
				want := "queued"
				if scenario == "no_receivers" {
					want = "no_receivers"
				}
				if receipt.Status != want {
					t.Fatalf("receipt %+v", receipt)
				}
				// Later subscribers cannot receive historical captures.
				if _, err = s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://blocked.example.test/late", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(AppWebhookEventRecoveryQueueBlocked)}}); err != nil {
					t.Fatal(err)
				}
				if _, err = worker.ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx, now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err = s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, now.Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, row := range rows {
					if row.Event != AppWebhookEventRecoveryQueueBlocked {
						continue
					}
					count++
					var payload api.WorkflowRecoveryQueueBlockedWebhookPayload
					if json.Unmarshal(row.Payload, &payload) != nil || payload.EventID != receipt.EventID || payload.ActionID != id || payload.ReviewID != review.ID || payload.PolicyVersion != review.PolicyVersion || row.WebhookID != hook.ID || payload.Status != action.Status {
						t.Fatalf("blocked delivery %s", row.Payload)
					}
				}
				wantCount := 1
				if scenario == "no_receivers" {
					wantCount = 0
				}
				if count != wantCount {
					t.Fatalf("deliveries %d want %d", count, wantCount)
				}
				if action.Status == "pending" {
					if _, err = s.(WorkflowRecoveryAutomaticActionOperatorStore).SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "discard blocked retry"}); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err = s.(WorkflowRecoveryAutomaticActionOperatorStore).SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, id, api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "discard blocked retry"}); !errors.Is(err, ErrEventRecoveryChanged) {
						t.Fatalf("released unresolved slot %v", err)
					}
				}
			})
		})
	}
}
