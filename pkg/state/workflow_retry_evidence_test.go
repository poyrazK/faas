package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestWorkflowRetryEvidenceRepair(t *testing.T) {
	for _, scenario := range []string{"success", "failed", "cancelled", "running", "superseded", "no_finished", "missing_job", "lineage_changed", "concurrent", "mode_changed", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "retry-operator")
				review, app, run, now := state.SeedAutomaticRecoveryReview(t, s, nil)
				action := review.AutomaticExecution.Actions[0].ActionID
				evidence := s.(state.WorkflowRetryEvidenceStore)
				req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "restore exact retry proof"}
				if scenario == "pending" {
					d, err := evidence.DiagnoseWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action)
					if err != nil || d.Repairable || d.Reason != "retry_not_applied" {
						t.Fatalf("pending diagnosis %+v %v", d, err)
					}
					if _, err = evidence.RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action, req); !errors.Is(err, state.ErrWorkflowRecoveryEvidenceBlocked) {
						t.Fatal(err)
					}
					return
				}
				state.RunAutomaticPolicy(t, s, now)
				child, err := state.AutomaticChild(t, s, app, review)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
					t.Fatal(err)
				}
				if scenario != "running" {
					status := state.WorkflowRunStatusSucceeded
					var last *string
					if scenario == "failed" || scenario == "cancelled" {
						status = state.WorkflowRunStatusFailed
					}
					if scenario == "cancelled" {
						v := "cancelled by operator"
						last = &v
					}
					if err = s.MarkWorkflowRunStatus(ctx, run.ID, status, nil, last); err != nil {
						t.Fatal(err)
					}
				}
				if m, ok := s.(*state.MemStore); ok {
					(*m.Mu()).Lock()
					delete((*m.EventRecoveryJobs())[child.ID].WorkflowResults, 1)
					switch scenario {
					case "superseded":
						v := (*m.WorkflowRuns())[run.ID]
						v.ResumeCount = 3
						(*m.WorkflowRuns())[run.ID] = v
					case "no_finished":
						v := (*m.WorkflowRuns())[run.ID]
						v.FinishedAt = nil
						(*m.WorkflowRuns())[run.ID] = v
					case "missing_job":
						delete((*m.EventRecoveryJobs()), child.ID)
					case "lineage_changed":
						v := int64(42)
						(*m.EventRecoveryJobs())[child.ID].Items[0].ParentPosition = &v
					}
					(*m.Mu()).Unlock()
				} else {
					pg := s.(*state.PgStore)
					if _, err = (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", child.ID); err != nil {
						t.Fatal(err)
					}
					if scenario == "superseded" || scenario == "no_finished" {
						tx, err := (*pg.Pool()).Begin(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = tx.Exec(ctx, "ALTER TABLE workflow_runs DISABLE TRIGGER event_recovery_workflow_result"); err != nil {
							t.Fatal(err)
						}
						if scenario == "superseded" {
							_, err = tx.Exec(ctx, "UPDATE workflow_runs SET resume_count=3 WHERE id=$1::uuid", run.ID)
						} else {
							_, err = tx.Exec(ctx, "UPDATE workflow_runs SET finished_at=NULL WHERE id=$1::uuid", run.ID)
						}
						if err != nil {
							t.Fatal(err)
						}
						if _, err = tx.Exec(ctx, "ALTER TABLE workflow_runs ENABLE TRIGGER event_recovery_workflow_result"); err != nil {
							t.Fatal(err)
						}
						if err = tx.Commit(ctx); err != nil {
							t.Fatal(err)
						}
					}
					if scenario == "missing_job" {
						if _, err = (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", child.ID); err != nil {
							t.Fatal(err)
						}
					}
					if scenario == "lineage_changed" {
						if _, err = (*pg.Pool()).Exec(ctx, "UPDATE event_recovery_items SET expected_progress=jsonb_set(expected_progress,'{parent_position}','42'::jsonb) WHERE job_id=$1::uuid", child.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "mode_changed" {
					p, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					p.Policy.Mode = "automatic_initial"
					p.Policy.Triggers = []string{"initial_failure"}
					if _, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
						t.Fatal(err)
					}
				}
				getReview := func() api.WorkflowRecoveryPolicyReview {
					r, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if err != nil {
						t.Fatal(err)
					}
					return r
				}
				before := getReview()
				d, err := evidence.DiagnoseWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, getReview()) {
					t.Fatal("diagnosis wrote review")
				}
				eligible := scenario == "success" || scenario == "failed" || scenario == "cancelled" || scenario == "concurrent" || scenario == "mode_changed"
				if d.Repairable != eligible || d.ResumeCount != 2 || d.RunID != run.ID || d.ActionID != action {
					t.Fatalf("diagnosis %+v", d)
				}
				if _, err = evidence.DiagnoseWorkflowRetryEvidence(ctx, uuid.NewString(), app.ID, review.ID, action); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("account scope %v", err)
				}
				if _, err = evidence.DiagnoseWorkflowRetryEvidence(ctx, app.AccountID, app.ID, uuid.NewString(), action); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("review scope %v", err)
				}
				if scenario == "concurrent" {
					var wg sync.WaitGroup
					errs := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, e := evidence.RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
							errs <- e
						}()
					}
					wg.Wait()
					close(errs)
					for e := range errs {
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				out, err := evidence.RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
				if !eligible {
					if !errors.Is(err, state.ErrWorkflowRecoveryEvidenceBlocked) {
						t.Fatalf("unsafe repair %+v %v", out, err)
					}
					return
				}
				if err != nil || out.Repair == nil || out.Repair.ActorID != "retry-operator" || out.Execution == nil || out.Execution.Source != "recovery_result" {
					t.Fatalf("repair %+v %v", out, err)
				}
				if getReview().EvidenceRepairs[action].RequestID != req.RequestID {
					t.Fatal("missing review audit")
				}
				if pg, ok := s.(*state.PgStore); ok {
					s = state.NewPgStore((*pg.Pool()).Pool)
					evidence = s.(state.WorkflowRetryEvidenceStore)
				}
				again, err := evidence.RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
				if err != nil || !reflect.DeepEqual(again.Repair, out.Repair) {
					t.Fatalf("replay %+v %v", again, err)
				}
				changed := req
				changed.Reason = "different"
				if _, err = evidence.RepairWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action, changed); !errors.Is(err, state.ErrEventRecoveryChanged) {
					t.Fatalf("changed replay %v", err)
				}
				queue, err := s.(state.WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || queue.Outstanding != 1 || queue.HourlyExecutions != 1 {
					t.Fatalf("repair released slot/refunded %+v %v", queue, err)
				}
				state.RunAutomaticPolicy(t, s, now.Add(3*time.Minute))
				final := getReview()
				result := final.AutomaticExecution.Actions[0].Result
				if result == nil || !result.Complete || result.ResumeCount != 2 || result.Outcome != out.Repair.Outcome {
					t.Fatalf("worker result %+v", final)
				}
				d, err = evidence.DiagnoseWorkflowRetryEvidence(ctx, app.AccountID, app.ID, review.ID, action)
				if err != nil || d.Reason != "already_confirmed" || d.Repairable {
					t.Fatalf("confirmed %+v %v", d, err)
				}
			})
		})
	}
}
