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

func seedRecoveryNotificationReview(t *testing.T, s state.Store, retry bool) (api.WorkflowRecoveryPolicyReview, state.App, *state.WorkflowRun, time.Time) {
	t.Helper()
	ctx := context.Background()
	run, app := state.SeedResumeRun(t, s, state.SimpleResumeSpec())
	state.FailResumeRun(t, s, run, "send")
	recovery := s.(state.EventRecoveryStore)
	_, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, ExecutionDeadlineSeconds: 60, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if did, e := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil || !did {
		t.Fatalf("admit=%v %v", did, e)
	}
	if retry {
		if _, e := s.ClaimNextDueWorkflowRun(ctx); e != nil {
			t.Fatal(e)
		}
		state.FailResumeRun(t, s, run, "send")
	}
	p := state.TestRecoveryPreviewPolicy(run.WorkflowName)
	if !retry {
		p.Triggers = []string{"deadline_exceeded"}
	}
	policies := s.(state.WorkflowRecoveryPolicyStore)
	if _, e := policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{Policy: p}); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Add(2 * time.Minute)
	if _, e := policies.ProcessNextWorkflowRecoveryPolicy(ctx, now); e != nil {
		t.Fatal(e)
	}
	reviews, e := policies.ListWorkflowRecoveryPolicyReviews(ctx, app.AccountID, app.ID, "", 100)
	if e != nil || len(reviews.Reviews) != 1 || reviews.Reviews[0].Plan == nil {
		t.Fatalf("reviews=%+v %v", reviews, e)
	}
	return reviews.Reviews[0], app, run, now
}
func TestWorkflowRecoveryReviewNotifications(t *testing.T) {
	for _, scenario := range []string{"queued", "no_receivers", "dismissed", "approved", "stale", "applied", "retry_applied", "unknown", "late_hook", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := context.Background()
				review, app, run, now := seedRecoveryNotificationReview(t, s, scenario == "retry_applied")
				hookInput := state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://review.example.test/ready", SecretSealed: []byte("test-sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryReviewReady)}}
				var hook state.AppWebhook
				var err error
				if scenario != "no_receivers" {
					hook, err = s.CreateAppWebhook(ctx, hookInput)
					if err != nil {
						t.Fatal(err)
					}
				}
				// An unrelated filter must never join the frozen receiver list.
				unrelated := hookInput
				unrelated.TargetURL = "https://review.example.test/unrelated"
				unrelated.EventFilter = []string{string(state.AppWebhookEventRecoveryCompleted)}
				if _, err = s.CreateAppWebhook(ctx, unrelated); err != nil {
					t.Fatal(err)
				}
				policies := s.(state.WorkflowRecoveryPolicyStore)
				notices := s.(state.WorkflowRecoveryReviewNotificationStore)
				switch scenario {
				case "approved", "dismissed":
					if _, err = policies.DecideWorkflowRecoveryPolicyReview(state.WithEventRecoveryActor(ctx, "api_key", "reviewer"), app.AccountID, app.ID, review.ID, api.WorkflowRecoveryReviewDecisionRequest{Decision: scenario, Reason: "reviewed incident"}); err != nil {
						t.Fatal(err)
					}
				case "stale":
					if _, err = s.CancelWorkflowRun(ctx, run.ID, "external resolution"); err != nil {
						t.Fatal(err)
					}
				case "applied":
					if _, err = s.(state.WorkflowRecoveryCancellationStore).WorkflowRecoveryCancellation(ctx, app.AccountID, review.JobID, *review.Plan.Actions[0].Cancellation, false); err != nil {
						t.Fatal(err)
					}
				case "retry_applied":
					if _, err = s.(state.EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, *review.Plan.Actions[0].Retry); err != nil {
						t.Fatal(err)
					}
				case "unknown":
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						delete((*m.WorkflowRuns()), run.ID)
						(*m.Mu()).Unlock()
					} else {
						if _, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				calls := 1
				if scenario == "concurrent" {
					calls = 2
				}
				type result struct {
					worked bool
					err    error
				}
				results := make(chan result, calls)
				for i := 0; i < calls; i++ {
					go func() {
						did, e := notices.ProcessNextWorkflowRecoveryReviewNotification(ctx, now)
						results <- result{did, e}
					}()
				}
				for i := 0; i < calls; i++ {
					r := <-results
					if r.err != nil {
						t.Fatal(r.err)
					}
				}
				retained, err := policies.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "unknown" {
					if retained.Notification != nil {
						t.Fatalf("unknown captured=%+v", retained.Notification)
					}
					if worked, e := notices.ProcessNextWorkflowRecoveryReviewNotification(ctx, now); worked || e != nil {
						t.Fatalf("unknown poll was not deferred=%v %v", worked, e)
					}
					return
				}
				want := "queued"
				if scenario == "no_receivers" {
					want = "no_receivers"
				}
				if scenario == "dismissed" || scenario == "approved" || scenario == "stale" || scenario == "applied" || scenario == "retry_applied" {
					want = "suppressed"
				}
				n := retained.Notification
				if n == nil || n.Status != want || n.EventID != state.RecoveryReviewNotificationID(review.ID) {
					t.Fatalf("capture=%+v want=%s", n, want)
				}
				if want == "queued" && (len(n.ReceiverWebhookIDs) != 1 || n.ReceiverWebhookIDs[0] != hook.ID) {
					t.Fatalf("receivers=%+v", n)
				}
				if _, e := policies.GetWorkflowRecoveryPolicyReview(ctx, uuid.NewString(), app.ID, review.ID); !errors.Is(e, state.ErrNotFound) {
					t.Fatalf("foreign receipt=%v", e)
				}
				// A process restart and repeated polls must not create another event.
				if pg, ok := s.(*state.PgStore); ok {
					s = state.NewPgStore((*pg.Pool()).Pool)
					notices = s.(state.WorkflowRecoveryReviewNotificationStore)
				}
				if worked, e := notices.ProcessNextWorkflowRecoveryReviewNotification(ctx, now.Add(time.Hour)); worked || e != nil {
					t.Fatalf("duplicate capture=%v %v", worked, e)
				}
				if scenario == "late_hook" {
					late := hookInput
					late.TargetURL = "https://review.example.test/late"
					if _, err = s.CreateAppWebhook(ctx, late); err != nil {
						t.Fatal(err)
					}
				}
				relayer := s.(state.AppWebhookEventOutboxStore)
				if _, err = relayer.DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, now.Add(time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				noticeRows := []state.AppWebhookDelivery{}
				for _, row := range rows {
					if row.Event == state.AppWebhookEventRecoveryReviewReady {
						noticeRows = append(noticeRows, row)
					}
				}
				if want != "queued" {
					if len(noticeRows) != 0 {
						t.Fatal("suppressed review was sent to outbox")
					}
					return
				}
				if len(noticeRows) != 1 {
					t.Fatalf("delivery count=%d", len(noticeRows))
				}
				row := noticeRows[0]
				var payload api.WorkflowRecoveryReviewReadyWebhookPayload
				if json.Unmarshal(row.Payload, &payload) != nil || payload.EventID != n.EventID || payload.ReviewID != review.ID || len(payload.Targets) != 1 || payload.Targets[0].RunID != run.ID || payload.Targets[0].ResumeCount != 1 || payload.Targets[0].Trigger != "deadline_exceeded" || payload.ReviewCommand == "" || payload.ExportCommand == "" {
					t.Fatalf("payload=%+v", payload)
				}
				allowed, reason, e := notices.WorkflowRecoveryReviewNotificationAllowed(ctx, row, now)
				if e != nil || !allowed {
					t.Fatalf("ready delivery=%v %s %v", allowed, reason, e)
				}
				tampered := row
				bad := payload
				bad.ReviewCommand = "unrelated command"
				tampered.Payload, _ = json.Marshal(bad)
				permitted, why, e := notices.WorkflowRecoveryReviewNotificationAllowed(ctx, tampered, now)
				if e != nil || permitted || why != "payload_mismatch" {
					t.Fatalf("tampered payload=%v %s %v", permitted, why, e)
				}
				foreign := row
				foreign.AccountID = uuid.NewString()
				permitted, why, e = notices.WorkflowRecoveryReviewNotificationAllowed(ctx, foreign, now)
				if e != nil || permitted || why != "review_not_retained" {
					t.Fatalf("foreign scope=%v %s %v", permitted, why, e)
				}
				// Backoff retries preserve the original delivery and event identities.
				if err = s.MarkAppWebhookDeliveryFailed(ctx, row.ID, 503, row.Attempt, row.NextAttemptAt, "temporary receiver failure", now.Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
				retryRows, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now.Add(3*time.Minute))
				if err != nil || len(retryRows) != 1 || retryRows[0].ID != row.ID || string(retryRows[0].Payload) != string(row.Payload) {
					t.Fatalf("retry=%+v %v", retryRows, err)
				}
				if scenario == "queued" || scenario == "concurrent" {
					if _, err = s.(state.WorkflowRecoveryPolicyStore).DecideWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowRecoveryReviewDecisionRequest{Decision: "dismissed", Reason: "incident already resolved"}); err != nil {
						t.Fatal(err)
					}
					allowed, reason, e = notices.WorkflowRecoveryReviewNotificationAllowed(ctx, retryRows[0], now)
					if e != nil || allowed || reason != "dismissed" {
						t.Fatalf("retry after dismissal=%v %s %v", allowed, reason, e)
					}
				} else if scenario == "late_hook" {
					if _, err = s.CancelWorkflowRun(ctx, run.ID, "finished externally"); err != nil {
						t.Fatal(err)
					}
					allowed, reason, e = notices.WorkflowRecoveryReviewNotificationAllowed(ctx, retryRows[0], now)
					if e != nil || allowed || reason != "stale" {
						t.Fatalf("retry after stale=%v %s %v", allowed, reason, e)
					}
				}
			})
		})
	}
}
