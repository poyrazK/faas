package state_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1269
func TestWorkflowRetryEvidenceNotifications(t *testing.T) {
	for _, scenario := range []string{"parent", "child", "no_receivers", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := context.Background()
				review, app, run, now := state.SeedAutomaticRecoveryReview(t, s, nil)
				id := review.AutomaticExecution.Actions[0].ActionID
				job, kind := review.JobID, "parent"
				if scenario == "child" {
					kind = "child"
					state.RunAutomaticPolicy(t, s, now)
					child, err := state.AutomaticChild(t, s, app, review)
					if err != nil {
						t.Fatal(err)
					}
					job = child.ID
					if _, err = s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
						t.Fatal(err)
					}
					if err = s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if m, ok := s.(*state.MemStore); ok {
					(*m.Mu()).Lock()
					delete((*m.EventRecoveryJobs())[job].WorkflowResults, 1)
					if kind == "parent" {
						(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Executions[id].Reason = "evidence_unknown"
					}
					(*m.Mu()).Unlock()
				} else {
					pg := s.(*state.PgStore)
					if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", job); err != nil {
						t.Fatal(err)
					}
					if kind == "parent" {
						if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_recovery_policy_executions SET reason='evidence_unknown' WHERE action_id=$1::uuid", id); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "unknown" {
					state.RemoveAutomaticLiveRun(t, s, run)
				}
				if scenario != "no_receivers" {
					if _, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://retry-alert.example.test/hook", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{string(state.AppWebhookEventRecoveryQueueBlocked)}}); err != nil {
						t.Fatal(err)
					}
				}
				worker := s.(state.WorkflowRecoveryAutomaticBlockedStore)
				poll := func(at time.Time) {
					t.Helper()
					var wg sync.WaitGroup
					errs := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, err := worker.ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx, at)
							errs <- err
						}()
					}
					wg.Wait()
					close(errs)
					for err := range errs {
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				get := func() api.WorkflowRecoveryAutomaticAction {
					t.Helper()
					r, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if err != nil {
						t.Fatal(err)
					}
					return r.AutomaticExecution.Actions[0]
				}
				at := now.Add(time.Minute)
				poll(at)
				first := get().BlockedNotification
				if first == nil || first.State != "blocked" || first.Sequence != 1 || first.EvidenceKind != kind {
					t.Fatalf("first %+v", first)
				}
				if scenario != "unknown" && first.Reason != "terminal_outcome_not_saved" {
					t.Fatalf("repairable reason %+v", first)
				}
				if pg, ok := s.(*state.PgStore); ok {
					s = state.NewPgStore((*pg.Pool()).Pool)
					worker = s.(state.WorkflowRecoveryAutomaticBlockedStore)
				}
				poll(at.Add(time.Minute))
				if get().BlockedNotification.EventID != first.EventID {
					t.Fatal("unchanged alert repeated")
				}
				if scenario != "unknown" {
					req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "verified alert guidance"}
					var err error
					if kind == "parent" {
						_, err = s.(state.WorkflowRetryParentEvidenceStore).RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, id, req)
					} else {
						_, err = s.(state.WorkflowRetryEvidenceStore).RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, id, req)
					}
					if err != nil {
						t.Fatal(err)
					}
					poll(at.Add(2 * time.Minute))
					cleared := get().BlockedNotification
					if cleared.State != "recovered" || cleared.Sequence != 2 || cleared.EventID == first.EventID || cleared.Reason != "blocker_cleared" {
						t.Fatalf("clear %+v", cleared)
					}
					poll(at.Add(3 * time.Minute))
					if get().BlockedNotification.EventID != cleared.EventID {
						t.Fatal("clear repeated")
					}
				} else {
					poll(at.Add(2 * time.Minute))
					if get().BlockedNotification.State != "blocked" {
						t.Fatal("unknown cleared")
					}
				}
				if _, err := s.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil {
					t.Fatal(err)
				}
				rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, at.Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, row := range rows {
					if row.Event != state.AppWebhookEventRecoveryQueueBlocked {
						continue
					}
					count++
					var p api.WorkflowRecoveryQueueBlockedWebhookPayload
					if err = json.Unmarshal(row.Payload, &p); err != nil {
						t.Fatal(err)
					}
					wantCount := int64(1)
					if kind == "child" {
						wantCount = 2
					}
					if p.EvidenceKind != kind || p.RunID != run.ID || p.ResumeCount == nil || *p.ResumeCount != wantCount || p.Repairable == nil || p.DiagnosisReason == "" || p.EvidenceURL == "" || p.CLICommand == "" {
						t.Fatalf("guidance %s", row.Payload)
					}
					if strings.Contains(p.CLICommand, "--parent") != (kind == "parent") {
						t.Fatal("wrong CLI target")
					}
					if p.State == "blocked" && scenario != "unknown" && !*p.Repairable {
						t.Fatal("historical repairability changed")
					}
				}
				want := 2
				if scenario == "unknown" {
					want = 1
				}
				if scenario == "no_receivers" {
					want = 0
				}
				if count != want {
					t.Fatalf("captures %d want %d", count, want)
				}
			})
		})
	}
}

// adr: 1269
func TestWorkflowRetryEvidenceTransitionSafety(t *testing.T) {
	e := api.WorkflowRecoveryAutomaticAction{ActionID: uuid.NewString(), Status: "applied", BlockedNotification: &api.WorkflowRecoveryAutomaticBlockedNotification{Reason: "evidence_unknown"}}
	d := api.WorkflowRetryEvidenceDiagnosis{EvidenceKind: "child", Reason: "completion_time_unverified"}
	p := state.AutomaticEvidenceTransition(uuid.NewString(), "orders", uuid.NewString(), 1, e, d, time.Now())
	if p == nil || p.State != "blocked" {
		t.Fatal("missing proof cleared")
	}
	e.BlockedNotification = func() *api.WorkflowRecoveryAutomaticBlockedNotification {
		r := state.AutomaticEvidenceReceipt(*p, e.BlockedNotification, nil)
		return &r
	}()
	if state.AutomaticEvidenceTransition(p.AppID, p.App, p.ReviewID, 1, e, d, time.Now()) != nil {
		t.Fatal("repeat")
	}
	d.Reason = "generation_superseded"
	if p = state.AutomaticEvidenceTransition(p.AppID, p.App, p.ReviewID, 1, e, d, time.Now()); p == nil || p.State != "blocked" {
		t.Fatal("reason change lost")
	}
}
