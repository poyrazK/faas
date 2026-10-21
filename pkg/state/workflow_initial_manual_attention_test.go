package state_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1277
func TestWorkflowInitialManualAttention(t *testing.T) {
	for _, receivers := range []bool{false, true} {
		t.Run(map[bool]string{false: "late_receiver", true: "subscribed"}[receivers], func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "attention-operator")
				run, app, failure, p := state.SeedInitialReview(t, s)
				reviews := s.(state.WorkflowInitialRecoveryReviewStore)
				r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
				if err != nil {
					t.Fatal(err)
				}
				r, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve recovery"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
					t.Fatal(err)
				}
				p.Policy.Mode, p.Policy.HourlyExecutionBudget, p.Policy.PauseAfterFailures = "automatic_initial", 1, 1
				p, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
				if err != nil {
					t.Fatal(err)
				}
				p, err = s.(state.WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.ControlWorkflowRecoveryPolicyRequest{Operation: "pause", ExpectedVersion: p.Version, ExpectedUpdatedAt: p.UpdatedAt, Reason: "paused while investigating"})
				if err != nil {
					t.Fatal(err)
				}
				if err = s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
					t.Fatal(err)
				}
				finished, err := s.GetWorkflowRun(ctx, run.ID)
				if err != nil || finished.FinishedAt == nil {
					t.Fatalf("terminal proof %+v %v", finished, err)
				}
				if m, ok := s.(*state.MemStore); ok {
					(*m.Mu()).Lock()
					delete((*m.EventRecoveryJobs())[r.JobID].WorkflowResults, 1)
					(*m.Mu()).Unlock()
				} else {
					if _, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", r.JobID); err != nil {
						t.Fatal(err)
					}
				}
				hook := func() string {
					t.Helper()
					h, e := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://manual-attention.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryQueueBlocked)}})
					if e != nil {
						t.Fatal(e)
					}
					return h.ID
				}
				hookID := ""
				if receivers {
					hookID = hook()
				}
				poll := func() {
					t.Helper()
					now := time.Now().UTC().Truncate(time.Microsecond)
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						delete((*m.InitialCompletionNext()), r.ID)
						(*m.Mu()).Unlock()
					} else {
						if _, e := (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_initial_recovery_reviews SET completion_next_observe_at=$2 WHERE id=$1::uuid", r.ID, now); e != nil {
							t.Fatal(e)
						}
					}
					did, e := s.(state.WorkflowInitialManualCompletionStore).ProcessNextWorkflowInitialManualCompletion(ctx, time.Now().UTC())
					if e != nil || !did {
						t.Fatalf("observer %v %v", did, e)
					}
				}
				poll()
				first := state.InitialAutomaticReview(t, s, app, r.ID)
				a := first.Attention
				if a == nil || a.BlockReason != "terminal_outcome_not_saved" || a.Sequence != 1 || a.Diagnosis == nil || !a.Diagnosis.Repairable || a.Notification == nil || a.Notification.State != "blocked" || a.EvidenceURL == "" || a.CLICommand == "" || a.OperatorAction == "" {
					t.Fatalf("missing attention %+v", a)
				}
				if !receivers {
					if a.Notification.Status != "no_receivers" {
						t.Fatal(a.Notification)
					}
					hookID = hook()
				}
				// Inspection cannot update the committed snapshot or capture an alert.
				if _, err = reviews.GetWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.(state.WorkflowInitialEvidenceStore).DiagnoseWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID); err != nil {
					t.Fatal(err)
				}
				if after := state.InitialAutomaticReview(t, s, app, r.ID); !reflect.DeepEqual(first, after) {
					t.Fatal("read changed attention")
				}
				poll()
				same := state.InitialAutomaticReview(t, s, app, r.ID)
				if same.Attention.Sequence != 1 || !reflect.DeepEqual(same.Attention.Notification, a.Notification) {
					t.Fatal("unchanged evidence replayed or recipients changed")
				}
				setFinished := func(at *time.Time) {
					t.Helper()
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						v := (*m.WorkflowRuns())[run.ID]
						v.FinishedAt = at
						(*m.WorkflowRuns())[run.ID] = v
						(*m.Mu()).Unlock()
					} else {
						tx, e := (*s.(*state.PgStore).Pool()).Begin(ctx)
						if e != nil {
							t.Fatal(e)
						}
						defer func() { _ = tx.Rollback(ctx) }()
						if _, e = tx.Exec(ctx, "ALTER TABLE workflow_runs DISABLE TRIGGER event_recovery_workflow_result"); e != nil {
							t.Fatal(e)
						}
						if _, e = tx.Exec(ctx, "UPDATE workflow_runs SET finished_at=$2 WHERE id=$1::uuid", run.ID, at); e != nil {
							t.Fatal(e)
						}
						if _, e = tx.Exec(ctx, "ALTER TABLE workflow_runs ENABLE TRIGGER event_recovery_workflow_result"); e != nil {
							t.Fatal(e)
						}
						if e = tx.Commit(ctx); e != nil {
							t.Fatal(e)
						}
					}
				}
				setFinished(nil)
				poll()
				changed := state.InitialAutomaticReview(t, s, app, r.ID)
				if changed.Attention.Sequence != 2 || changed.Attention.Notification.EventID == a.Notification.EventID || changed.Attention.Diagnosis.Reason != "completion_time_unverified" || changed.Attention.Diagnosis.Repairable || changed.Attention.BlockReason == "" || changed.Result != nil {
					t.Fatalf("changed proof %+v", changed.Attention)
				}
				poll()
				if unchanged := state.InitialAutomaticReview(t, s, app, r.ID); unchanged.Attention.Sequence != 2 {
					t.Fatal("unverified proof repeatedly alerted")
				}
				setFinished(finished.FinishedAt)
				req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "restore verified completion proof"}
				if _, err = s.(state.WorkflowInitialEvidenceStore).RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req); err != nil {
					t.Fatal(err)
				}
				if pg, ok := s.(*state.PgStore); ok {
					s = state.NewPgStore((*pg.Pool()).Pool)
				}
				poll()
				final := state.InitialAutomaticReview(t, s, app, r.ID)
				if final.Result == nil || final.Result.Outcome != "failed" || final.Attention.Sequence != 3 || final.Attention.BlockReason != "" || final.Attention.Notification.State != "recovered" || final.Attention.Diagnosis.Reason != "already_confirmed" {
					t.Fatalf("verified clearance %+v", final)
				}
				if final.AutomaticResult != nil || final.AutomaticEvaluation != nil || final.AutomaticBlockSequence != 0 || final.AutomaticBlockNotification != nil {
					t.Fatal("manual attention changed automatic state")
				}
				current, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil || current.AutomaticExecution == nil || current.AutomaticExecution.ConsecutiveFailures != 0 || current.AutomaticExecution.PausedAt == nil || !current.AutomaticExecution.PausedAt.Equal(*p.AutomaticExecution.PausedAt) {
					t.Fatalf("pause or counters changed %+v %v", current, err)
				}
				history, err := s.(state.WorkflowRecoveryHistoryStore).GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{RunID: run.ID, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				counts := map[string]int{}
				for _, e := range history.Entries {
					if e.ReviewID == r.ID {
						counts[e.Kind]++
						if e.Kind == "initial_recovery_blocked" || e.Kind == "initial_recovery_cleared" {
							if !e.Durable || e.ActorID != "initial_completion_observer" || e.NotificationEventID == "" || e.PolicyVersion != r.PolicyVersion {
								t.Fatalf("attention history %+v", e)
							}
						}
					}
				}
				if counts["initial_recovery_blocked"] != 2 || counts["initial_recovery_cleared"] != 1 || counts["initial_evidence_repaired"] != 1 || counts["initial_recovery_result"] != 1 {
					t.Fatalf("history counts %+v", counts)
				}
				if _, err = s.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, time.Now().Add(time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				captured := 0
				for _, row := range rows {
					if row.Event != state.AppWebhookEventRecoveryQueueBlocked {
						continue
					}
					captured++
					var payload api.WorkflowRecoveryQueueBlockedWebhookPayload
					if err = json.Unmarshal(row.Payload, &payload); err != nil {
						t.Fatal(err)
					}
					if row.WebhookID != hookID || payload.ReviewID != r.ID || payload.InitialFailureID != failure || payload.QueueURL != "/v1/apps/"+app.Slug+"/initial-recovery-reviews" || payload.OperatorAction == "" || payload.EvidenceURL == "" || payload.CLICommand == "" {
						t.Fatalf("manual payload %+v", payload)
					}
					if payload.EventID == a.Notification.EventID && (payload.Repairable == nil || !*payload.Repairable || payload.DiagnosisReason != "terminal_outcome_not_saved") {
						t.Fatal("historical alert was rewritten")
					}
				}
				want := 2
				if receivers {
					want = 3
				}
				if captured != want {
					t.Fatalf("capture count %d want %d", captured, want)
				}
			})
		})
	}
}

func TestWorkflowInitialManualAttentionClearance(t *testing.T) {
	now := time.Now().UTC()
	r := api.WorkflowInitialRecoveryReview{ID: uuid.NewString(), Policy: api.WorkflowRecoveryPolicy{Mode: "preview"}, Status: "submitted", SubmittedAt: &now, Target: api.WorkflowRunRecoveryTarget{RunID: uuid.NewString()}}
	d := api.WorkflowInitialEvidenceDiagnosis{RunID: r.Target.RunID, ResumeCount: 1, Reason: "job_not_retained"}
	if p := state.InitialManualAttentionTransition(&r, "evidence_unknown", now, d); p == nil {
		t.Fatal("missing block")
	}
	for _, reason := range []string{"admission_wait", "execution_wait", "recovery_running", "result_recorded", "policy_changed", "automatic_paused"} {
		if p := state.InitialManualAttentionTransition(&r, reason, now, d); p != nil || r.Attention.BlockReason == "" {
			t.Fatalf("unverified %s cleared attention", reason)
		}
	}
	d.Reason = "run_not_terminal"
	d.Execution = &api.EventRecoveryExecution{Source: "workflow_run", State: "running"}
	if p := state.InitialManualAttentionTransition(&r, "recovery_running", now, d); p == nil || p.State != "recovered" || r.Result != nil {
		t.Fatal("verified progress did not clear, or invented completion")
	}
}
