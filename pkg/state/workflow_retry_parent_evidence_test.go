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

func TestWorkflowRetryParentEvidenceRepair(t *testing.T) {
	for _, scenario := range []string{"repair", "already_recorded", "concurrent", "paused", "policy_changed", "suppressed", "superseded", "missing_history", "missing_finish", "wrong_item", "already_applied", "budget"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "parent-operator")
				review, app, run, now := state.SeedAutomaticRecoveryReview(t, s, func(p *api.WorkflowRecoveryPolicy) {
					if scenario == "budget" {
						p.HourlyExecutionBudget = 1
					}
				})
				action := review.AutomaticExecution.Actions[0].ActionID
				parent := review.JobID
				position := review.AutomaticExecution.Actions[0].Position
				evidence := s.(state.WorkflowRetryParentEvidenceStore)
				req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "restore verified parent proof"}
				if scenario == "already_applied" {
					state.RunAutomaticPolicy(t, s, now)
				}
				if scenario == "suppressed" {
					if _, err := s.(state.WorkflowRecoveryAutomaticActionOperatorStore).SuppressWorkflowRecoveryAutomaticAction(ctx, app.AccountID, app.ID, review.ID, action, api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "do not retry"}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario != "already_recorded" {
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						delete((*m.EventRecoveryJobs())[parent].WorkflowResults, position)
						switch scenario {
						case "superseded":
							v := (*m.WorkflowRuns())[run.ID]
							v.ResumeCount = 2
							(*m.WorkflowRuns())[run.ID] = v
						case "missing_history":
							delete((*m.WorkflowResumes()), run.ID)
						case "missing_finish":
							v := (*m.WorkflowRuns())[run.ID]
							v.FinishedAt = nil
							(*m.WorkflowRuns())[run.ID] = v
						case "wrong_item":
							(*m.EventRecoveryJobs())[parent].Items[0].Position = position + 1
						}
						(*m.Mu()).Unlock()
					} else {
						pg := s.(*state.PgStore)
						if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid AND position=$2", parent, position); err != nil {
							t.Fatal(err)
						}
						if scenario == "superseded" || scenario == "missing_finish" {
							tx, err := (*pg.Pool()).Begin(ctx)
							if err != nil {
								t.Fatal(err)
							}
							if _, err = tx.Exec(ctx, "ALTER TABLE workflow_runs DISABLE TRIGGER event_recovery_workflow_result"); err != nil {
								t.Fatal(err)
							}
							if scenario == "superseded" {
								_, err = tx.Exec(ctx, "UPDATE workflow_runs SET resume_count=2 WHERE id=$1::uuid", run.ID)
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
						if scenario == "missing_history" {
							if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM workflow_run_resumes WHERE run_id=$1::uuid", run.ID); err != nil {
								t.Fatal(err)
							}
						}
						if scenario == "wrong_item" {
							if _, err := (*pg.Pool()).Exec(ctx, "UPDATE event_recovery_items SET position=position+1 WHERE job_id=$1::uuid", parent); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				policies := s.(state.WorkflowRecoveryPolicyStore)
				p, err := policies.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "paused" {
					if _, err = s.(state.WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, state.ControlRequest(p, "pause")); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "policy_changed" {
					p.Policy.Reason = "new settings"
					if _, err = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "budget" {
					at := time.Now().UTC().Truncate(time.Microsecond)
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						if (*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Charges == nil {
							(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Charges = map[string]time.Time{}
						}
						(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Charges[uuid.NewString()] = at
						(*m.Mu()).Unlock()
					} else {
						if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "INSERT INTO workflow_recovery_policy_execution_charges(action_id,app_id,applied_at) VALUES($1::uuid,$2::uuid,$3)", uuid.NewString(), app.ID, at); err != nil {
							t.Fatal(err)
						}
					}
				}
				getReview := func() api.WorkflowRecoveryPolicyReview {
					r, err := policies.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if err != nil {
						t.Fatal(err)
					}
					return r
				}
				before := getReview()
				d, err := evidence.DiagnoseWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, getReview()) {
					t.Fatal("diagnosis wrote review")
				}
				eligible := scenario == "repair" || scenario == "concurrent" || scenario == "paused" || scenario == "budget"
				if d.Repairable != eligible || d.EvidenceKind != "parent" || d.JobID != parent || d.ResumeCount != 1 || d.ItemPosition != position {
					t.Fatalf("diagnosis %+v", d)
				}
				if _, err = evidence.DiagnoseWorkflowRetryParentEvidence(ctx, uuid.NewString(), app.ID, review.ID, action); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("account scope %v", err)
				}
				if scenario == "concurrent" {
					var wg sync.WaitGroup
					errs := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, e := evidence.RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
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
				out, err := evidence.RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
				if !eligible {
					if !errors.Is(err, state.ErrWorkflowRecoveryEvidenceBlocked) {
						t.Fatalf("unsafe repair %+v %v", out, err)
					}
					return
				}
				if err != nil || out.Repair == nil || out.Repair.ActorID != "parent-operator" || out.Execution == nil || out.Execution.Source != "recovery_result" {
					t.Fatalf("repair %+v %v", out, err)
				}
				audited := getReview()
				if audited.ParentEvidenceRepairs[action].RequestID != req.RequestID || len(audited.EvidenceRepairs) != 0 {
					t.Fatal("parent/child audit mismatch")
				}
				if audited.AutomaticExecution.Actions[0].Status != "pending" || audited.AutomaticExecution.Actions[0].ChildJobID != "" {
					t.Fatal("repair submitted child")
				}
				again, err := evidence.RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
				if err != nil || !reflect.DeepEqual(again.Repair, out.Repair) {
					t.Fatalf("replay %+v %v", again, err)
				}
				changed := req
				changed.Reason = "different reason"
				if _, err = evidence.RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action, changed); !errors.Is(err, state.ErrEventRecoveryChanged) {
					t.Fatalf("changed replay %v", err)
				}
				queue, err := s.(state.WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				spent := 0
				if scenario == "budget" {
					spent = 1
				}
				if err != nil || queue.Outstanding != 0 || queue.HourlyExecutions != spent {
					t.Fatalf("repair charged/submitted %+v %v", queue, err)
				}
				if _, err = s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now.Add(3*time.Minute)); err != nil {
					t.Fatal(err)
				}
				final := getReview().AutomaticExecution.Actions[0]
				shouldSubmit := scenario == "repair" || scenario == "concurrent"
				if (final.ChildJobID != "") != shouldSubmit {
					t.Fatalf("fresh policy/budget checks %+v", final)
				}
				if shouldSubmit {
					replay, err := evidence.RepairWorkflowRetryParentEvidence(ctx, app.AccountID, app.ID, review.ID, action, req)
					if err != nil || !reflect.DeepEqual(replay.Repair, out.Repair) {
						t.Fatalf("post-submit replay %+v %v", replay, err)
					}
				}
			})
		})
	}
}
