package state

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryReviewCompletionLifecycle(t *testing.T) {
	for _, scenario := range []string{"cancel", "retry_failed", "retry_succeeded", "no_receivers", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				retry := scenario == "retry_failed" || scenario == "retry_succeeded"
				review, app, run, now := seedRecoveryNotificationReview(t, s, retry)
				inspector := s.(WorkflowRecoveryReviewInspector)
				notices := s.(WorkflowRecoveryReviewCompletionNotificationStore)
				policies := s.(WorkflowRecoveryPolicyStore)
				var hook AppWebhook
				var err error
				if scenario != "no_receivers" {
					hook, err = s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, Enabled: true, TargetURL: "https://completion.example.test/finished", SecretSealed: []byte("sealed"), EventFilter: []string{string(AppWebhookEventRecoveryReviewFinished)}})
					if err != nil {
						t.Fatal(err)
					}
				}
				status, err := InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, review.ID, now)
				if err != nil || status.Complete || status.Outcome != "pending" {
					t.Fatalf("unapplied=%+v %v", status, err)
				}
				if _, err = notices.ProcessNextWorkflowRecoveryReviewCompletion(ctx, now); err != nil {
					t.Fatal(err)
				}
				retained, err := policies.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				if err != nil || retained.CompletionNotification != nil {
					t.Fatalf("premature=%+v %v", retained, err)
				}
				want := "cancelled"
				if retry {
					child, e := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, *review.Plan.Actions[0].Retry)
					if e != nil {
						t.Fatal(e)
					}
					status, e = InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, review.ID, now)
					if e != nil || status.Complete || status.Outcome != "pending" || status.Actions[0].ChildJobID != child.ID || status.Actions[0].ResumeCount != 2 {
						t.Fatalf("admission pending=%+v %v", status, e)
					}
					if did, e := s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); e != nil || !did {
						t.Fatalf("admit=%t %v", did, e)
					}
					status, e = InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, review.ID, now)
					if e != nil || status.Complete || status.Outcome != "pending" {
						t.Fatalf("queued=%+v %v", status, e)
					}
					if _, e = s.ClaimNextDueWorkflowRun(ctx); e != nil {
						t.Fatal(e)
					}
					status, e = InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, review.ID, now)
					if e != nil || status.Complete || status.Outcome != "running" {
						t.Fatalf("running=%+v %v", status, e)
					}
					if _, e = notices.ProcessNextWorkflowRecoveryReviewCompletion(ctx, now.Add(time.Minute)); e != nil {
						t.Fatal(e)
					}
					if retry && scenario == "retry_failed" {
						failResumeRun(t, s, run, "send")
						want = "failed"
					} else {
						if e = s.MarkWorkflowRunStatus(WithWorkflowRunGeneration(ctx, run.ID, 2), run.ID, WorkflowRunStatusSucceeded, nil, nil); e != nil {
							t.Fatal(e)
						}
						want = "succeeded"
					}
				} else {
					response, e := s.(WorkflowRecoveryCancellationStore).WorkflowRecoveryCancellation(ctx, app.AccountID, review.JobID, *review.Plan.Actions[0].Cancellation, false)
					if e != nil || response.Outcome != "cancelled" {
						t.Fatalf("cancel=%+v %v", response, e)
					}
				}
				captureAt := now.Add(2 * time.Minute)
				calls := 1
				if scenario == "concurrent" {
					calls = 2
				}
				done := make(chan error, calls)
				for i := 0; i < calls; i++ {
					go func() { _, e := notices.ProcessNextWorkflowRecoveryReviewCompletion(ctx, captureAt); done <- e }()
				}
				for i := 0; i < calls; i++ {
					if e := <-done; e != nil {
						t.Fatal(e)
					}
				}
				retained, err = policies.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
				n := retained.CompletionNotification
				wantCapture := "queued"
				if scenario == "no_receivers" {
					wantCapture = "no_receivers"
				}
				if err != nil || n == nil || n.Status != wantCapture || !n.Result.Complete || n.Result.Outcome != want || n.Result.Actions[0].RecordedAt == nil {
					t.Fatalf("capture=%+v %v", n, err)
				}
				if scenario != "no_receivers" && (len(n.ReceiverWebhookIDs) != 1 || n.ReceiverWebhookIDs[0] != hook.ID) {
					t.Fatalf("receivers=%+v", n)
				}
				if pg, ok := s.(*PgStore); ok {
					s = NewPgStore(pg.pool.Pool)
					notices = s.(WorkflowRecoveryReviewCompletionNotificationStore)
					inspector = s.(WorkflowRecoveryReviewInspector)
					policies = s.(WorkflowRecoveryPolicyStore)
				}
				if worked, e := notices.ProcessNextWorkflowRecoveryReviewCompletion(ctx, captureAt.Add(time.Hour)); e != nil || worked {
					t.Fatalf("duplicate=%t %v", worked, e)
				}
				// Completion is historical even when the reviewer dismisses the incident later.
				if _, e := policies.DecideWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowRecoveryReviewDecisionRequest{Decision: "dismissed", Reason: "resolved"}); e != nil {
					t.Fatal(e)
				}
				if retry && want == "failed" {
					if _, _, _, e := s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 2)); e != nil {
						t.Fatal(e)
					}
				}
				// The retained snapshot also outlives removal of the live workflow row.
				if m, ok := s.(*MemStore); ok {
					m.mu.Lock()
					delete(m.workflowRuns, run.ID)
					m.mu.Unlock()
				} else {
					if _, e := s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); e != nil {
						t.Fatal(e)
					}
				}
				status, err = InspectWorkflowRecoveryReview(ctx, inspector, app.AccountID, app.ID, review.ID, captureAt.Add(time.Hour))
				expected := n.Result
				expected.ObservedAt = status.ObservedAt
				if err != nil || !reflect.DeepEqual(status, expected) {
					t.Fatalf("snapshot changed=%+v %v", status, err)
				}
				if _, e := s.CreateAppWebhook(ctx, AppWebhook{AccountID: app.AccountID, AppID: app.ID, Enabled: true, TargetURL: "https://completion.example.test/late", SecretSealed: []byte("sealed"), EventFilter: []string{string(AppWebhookEventRecoveryReviewFinished)}}); e != nil {
					t.Fatal(e)
				}
				if _, e := s.(AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); e != nil {
					t.Fatal(e)
				}
				rows, e := s.ClaimDueAppWebhookDeliveries(ctx, 32, captureAt.Add(time.Hour))
				if e != nil {
					t.Fatal(e)
				}
				count := 0
				for _, row := range rows {
					if row.Event != AppWebhookEventRecoveryReviewFinished {
						continue
					}
					count++
					allowed, reason, e := notices.WorkflowRecoveryReviewCompletionNotificationAllowed(ctx, row)
					if e != nil || !allowed {
						t.Fatalf("historical delivery denied=%s %v", reason, e)
					}
					var payload api.WorkflowRecoveryReviewFinishedWebhookPayload
					if e = json.Unmarshal(row.Payload, &payload); e != nil {
						t.Fatal(e)
					}
					if !reflect.DeepEqual(payload.Result, n.Result) {
						t.Fatalf("payload changed=%+v", payload)
					}
					bad := row
					bad.AccountID = uuid.NewString()
					if allowed, _, e = notices.WorkflowRecoveryReviewCompletionNotificationAllowed(ctx, bad); e != nil || allowed {
						t.Fatalf("foreign allowed=%t %v", allowed, e)
					}
					payload.Result.Outcome = "tampered"
					bad = row
					bad.Payload, _ = json.Marshal(payload)
					if allowed, _, e = notices.WorkflowRecoveryReviewCompletionNotificationAllowed(ctx, bad); e != nil || allowed {
						t.Fatalf("tampered allowed=%t %v", allowed, e)
					}
				}
				expectedCount := 1
				if scenario == "no_receivers" {
					expectedCount = 0
				}
				if count != expectedCount {
					t.Fatalf("deliveries=%d want=%d", count, expectedCount)
				}
			})
		})
	}
}

func TestRecoveryReviewOutcomeAggregation(t *testing.T) {
	for _, tc := range []struct {
		outcomes []string
		complete []bool
		want     string
		done     bool
	}{
		{[]string{"succeeded", "cancelled"}, []bool{true, true}, "completed", true},
		{[]string{"failed", "succeeded"}, []bool{true, true}, "failed", true},
		{[]string{"failed", "running"}, []bool{true, false}, "running", false},
		{[]string{"succeeded", "unknown"}, []bool{true, false}, "unknown", false},
	} {
		out := api.WorkflowRecoveryReviewStatus{}
		for i, v := range tc.outcomes {
			out.Actions = append(out.Actions, api.WorkflowRecoveryReviewActionStatus{Outcome: v, Complete: tc.complete[i]})
		}
		finishRecoveryReviewOutcome(&out)
		if out.Outcome != tc.want || out.Complete != tc.done {
			t.Fatalf("aggregate=%+v", out)
		}
	}
}

type recoveryReviewItemsEvidence struct {
	WorkflowRecoveryReviewInspector
	rows api.EventRecoveryItems
}

func (s recoveryReviewItemsEvidence) ListEventRecoveryItems(context.Context, string, string, int64, int) (api.EventRecoveryItems, error) {
	return s.rows, nil
}
func TestRecoveryReviewRequiresExactFinalEvidence(t *testing.T) {
	now := time.Now().UTC()
	count := 1
	position := int64(4)
	plan := api.WorkflowRecoveryResponsePlan{JobID: uuid.NewString()}
	action := api.WorkflowRecoveryResponseAction{Kind: "retry", RunID: uuid.NewString(), Position: position, Retry: &api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Runs: []api.WorkflowRunRecoveryTarget{{ExpectedResumeCount: count}}}}}
	child := api.EventRecoveryJob{ID: uuid.NewString(), State: "completed", CompletedAt: &now}
	base := api.EventRecoveryItem{WorkflowRunID: action.RunID, ExpectedResumeCount: &count, ParentJobID: plan.JobID, ParentPosition: &position, State: "queued"}
	for _, scenario := range []string{"missing", "live_terminal", "saved_without_time", "saved_wrong_source", "saved", "superseded", "skipped", "cancelled_admission", "wrong_generation", "wrong_parent"} {
		t.Run(scenario, func(t *testing.T) {
			item := base
			item.Execution = &api.EventRecoveryExecution{State: "succeeded", Source: "recovery_result", EvidenceSource: "workflow_run", RecordedAt: &now}
			wantComplete := false
			wantOutcome := "unknown"
			wantError := false
			switch scenario {
			case "missing":
				item.Execution = nil
			case "live_terminal":
				item.Execution.Source = "workflow_run"
			case "saved_without_time":
				item.Execution.RecordedAt = nil
			case "saved_wrong_source":
				item.Execution.EvidenceSource = "unknown"
			case "saved":
				wantComplete = true
				wantOutcome = "succeeded"
			case "superseded":
				item.Execution.State = "superseded"
			case "skipped":
				item.State = "skipped"
				wantComplete = true
				wantOutcome = "failed"
			case "cancelled_admission":
				item.State = "cancelled"
				wantComplete = true
				wantOutcome = "cancelled"
			case "wrong_generation":
				other := 2
				item.ExpectedResumeCount = &other
				wantError = true
			case "wrong_parent":
				item.ParentJobID = uuid.NewString()
				wantError = true
			}
			s := recoveryReviewItemsEvidence{rows: api.EventRecoveryItems{JobID: child.ID, Items: []api.EventRecoveryItem{item}}}
			out := api.WorkflowRecoveryReviewActionStatus{Outcome: "unknown"}
			err := inspectRecoveryReviewRetryOutcome(context.Background(), s, "account", plan, action, child, &out)
			if (err != nil) != wantError || out.Complete != wantComplete || out.Outcome != wantOutcome {
				t.Fatalf("proof=%+v err=%v", out, err)
			}
		})
	}
}
