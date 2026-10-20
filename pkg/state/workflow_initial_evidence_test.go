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

// adr: 1272
func TestWorkflowInitialEvidenceRepair(t *testing.T) { testInitialEvidenceRepair(t, false) }

// adr: 1276
func TestWorkflowInitialManualEvidenceRepair(t *testing.T) { testInitialEvidenceRepair(t, true) }

func testInitialEvidenceRepair(t *testing.T, manual bool) {
	for _, scenario := range []string{"success", "failed", "cancelled", "running", "superseded", "no_finished", "missing_job", "conflicting_result", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "evidence-operator")
				var run *state.WorkflowRun
				var app state.App
				var review api.WorkflowInitialRecoveryReview
				var policy api.WorkflowRecoveryPolicyResponse
				if manual {
					var failure string
					run, app, failure, policy = state.SeedInitialReview(t, s)
					var err error
					review, err = s.(state.WorkflowInitialRecoveryReviewStore).PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: policy.Version})
					if err != nil {
						t.Fatal(err)
					}
					prepared, diagErr := s.(state.WorkflowInitialEvidenceStore).DiagnoseWorkflowInitialEvidence(ctx, app.AccountID, app.ID, review.ID)
					if diagErr != nil || prepared.Repairable || prepared.Reason != "not_submitted_manual_initial" {
						t.Fatalf("prepared diagnosis %+v %v", prepared, diagErr)
					}
					if _, diagErr = s.(state.WorkflowInitialEvidenceStore).RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, review.ID, api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "cannot repair before approval"}); !errors.Is(diagErr, state.ErrWorkflowInitialReviewBlocked) {
						t.Fatalf("prepared repair authorized %v", diagErr)
					}
					review, err = s.(state.WorkflowInitialRecoveryReviewStore).DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, review.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve initial recovery"})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					run, app, review, policy = state.SeedInitialAutomatic(t, s)
					state.PollInitialAutomatic(t, s, time.Now().UTC())
				}
				r := state.InitialAutomaticReview(t, s, app, review.ID)
				jobs := s.(state.EventRecoveryStore)
				if _, err := jobs.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ClaimNextDueWorkflowRun(ctx); err != nil {
					t.Fatal(err)
				}
				if manual {
					policy.Policy.Enabled = false
					if scenario == "success" || scenario == "failed" {
						policy.Policy.Mode, policy.Policy.HourlyExecutionBudget, policy.Policy.PauseAfterFailures = "automatic_initial", 1, 1
						policy.Policy.Enabled = scenario == "failed"
					}
					var err error
					policy, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: policy.Version, Policy: policy.Policy})
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "failed" {
						policy, err = s.(state.WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.ControlWorkflowRecoveryPolicyRequest{Operation: "pause", ExpectedVersion: policy.Version, ExpectedUpdatedAt: policy.UpdatedAt, Reason: "paused during manual investigation"})
						if err != nil {
							t.Fatal(err)
						}
					}
					future := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						if (*m.InitialCompletionNext()) == nil {
							(*m.InitialCompletionNext()) = map[string]time.Time{}
						}
						(*m.InitialCompletionNext())[r.ID] = future
						(*m.Mu()).Unlock()
					} else {
						if _, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_initial_recovery_reviews SET completion_next_observe_at=$2 WHERE id=$1::uuid", r.ID, future); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario != "running" {
					status := state.WorkflowRunStatusSucceeded
					if scenario == "failed" || scenario == "cancelled" {
						status = state.WorkflowRunStatusFailed
					}
					var last *string
					if scenario == "cancelled" {
						v := "cancelled by operator"
						last = &v
					}
					if err := s.MarkWorkflowRunStatus(ctx, run.ID, status, nil, last); err != nil {
						t.Fatal(err)
					}
				}
				if m, ok := s.(*state.MemStore); ok {
					(*m.Mu()).Lock()
					delete((*m.EventRecoveryJobs())[r.JobID].WorkflowResults, 1)
					switch scenario {
					case "superseded":
						changed := (*m.WorkflowRuns())[run.ID]
						changed.ResumeCount = 2
						(*m.WorkflowRuns())[run.ID] = changed
					case "no_finished":
						changed := (*m.WorkflowRuns())[run.ID]
						changed.FinishedAt = nil
						(*m.WorkflowRuns())[run.ID] = changed
					case "missing_job":
						delete((*m.EventRecoveryJobs()), r.JobID)
					case "conflicting_result":
						(*m.EventRecoveryJobs())[r.JobID].WorkflowResults[1] = state.MemWorkflowRecoveryResult{RunID: run.ID, ResumeNumber: 2, Execution: api.EventRecoveryExecution{State: "superseded"}}
					}
					(*m.Mu()).Unlock()
				} else {
					pg := s.(*state.PgStore)
					if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", r.JobID); err != nil {
						t.Fatal(err)
					}
					// Run changes must not trigger repair through the normal recorder.
					if scenario == "superseded" || scenario == "no_finished" {
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
					if scenario == "missing_job" {
						if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", r.JobID); err != nil {
							t.Fatal(err)
						}
					}
					if scenario == "conflicting_result" {
						if _, err := (*pg.Pool()).Exec(ctx, "INSERT INTO event_recovery_workflow_results(job_id,position,run_id,resume_number,admitted_at,state,recorded_at) SELECT $1::uuid,1,run_id,2,created_at,'superseded',clock_timestamp() FROM workflow_run_resumes WHERE run_id=$2::uuid AND resume_number=1", r.JobID, run.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				evidence := s.(state.WorkflowInitialEvidenceStore)
				before := state.InitialAutomaticReview(t, s, app, r.ID)
				d, err := evidence.DiagnoseWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !manual {
					queueSnapshot, queueErr := s.(state.WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
					if queueErr != nil || len(queueSnapshot.Entries) != 1 || queueSnapshot.Entries[0].InitialEvidence == nil {
						t.Fatalf("initial queue %+v %v", queueSnapshot, queueErr)
					}
					qd := queueSnapshot.Entries[0].InitialEvidence.Diagnosis
					if qd.Reason != d.Reason || qd.Repairable != d.Repairable || qd.RunID != d.RunID || qd.ResumeCount != d.ResumeCount || qd.JobID != d.JobID || !qd.ObservedAt.Equal(queueSnapshot.ObservedAt) || queueSnapshot.Entries[0].Evidence != nil || queueSnapshot.Entries[0].InitialEvidence.Stage != "submitted" {
						t.Fatalf("queue diagnosis %+v endpoint %+v", qd, d)
					}
				}

				if manual {
					var scheduled time.Time
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						scheduled = (*m.InitialCompletionNext())[r.ID]
						(*m.Mu()).Unlock()
					} else {
						if err = (*s.(*state.PgStore).Pool()).QueryRow(ctx, "SELECT completion_next_observe_at FROM workflow_initial_recovery_reviews WHERE id=$1::uuid", r.ID).Scan(&scheduled); err != nil {
							t.Fatal(err)
						}
					}
					if !scheduled.After(time.Now().Add(time.Minute)) {
						t.Fatal("diagnosis woke completion observer")
					}
				}
				if after := state.InitialAutomaticReview(t, s, app, r.ID); !reflect.DeepEqual(before, after) {
					t.Fatal("diagnosis wrote review")
				}
				repairable := scenario == "success" || scenario == "failed" || scenario == "cancelled" || scenario == "concurrent"
				if d.Repairable != repairable {
					t.Fatalf("diagnosis %+v", d)
				}
				if _, err = evidence.DiagnoseWorkflowInitialEvidence(ctx, uuid.NewString(), app.ID, r.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("account fence %v", err)
				}
				req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "reconcile verified terminal outcome"}
				if scenario == "concurrent" {
					var wg sync.WaitGroup
					errs := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, e := evidence.RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req)
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
				repaired, err := evidence.RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req)
				if !repairable {
					if !errors.Is(err, state.ErrWorkflowInitialReviewBlocked) {
						t.Fatalf("unsafe repair %+v %v", repaired, err)
					}
					return
				}
				if err != nil || repaired.Repair == nil || repaired.Execution == nil || repaired.Execution.Source != "recovery_result" || repaired.Repair.ActorID != "evidence-operator" {
					t.Fatalf("repair %+v %v", repaired, err)
				}
				waiting := state.InitialAutomaticReview(t, s, app, r.ID)
				if waiting.Result != nil || waiting.AutomaticResult != nil {
					t.Fatal("repair confirmed completion directly")
				}
				again, err := evidence.RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req)
				if err != nil || !reflect.DeepEqual(again.Repair, repaired.Repair) {
					t.Fatalf("repair replay %+v %v", again, err)
				}
				req.Reason = "different reason"
				if _, err = evidence.RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req); !errors.Is(err, state.ErrEventRecoveryChanged) {
					t.Fatalf("changed replay %v", err)
				}
				queue, err := s.(state.WorkflowRecoveryPolicyOperatorStore).GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				expected := 1
				if manual {
					expected = 0
				}
				if err != nil || queue.Outstanding != expected || queue.HourlyExecutions != expected {
					t.Fatalf("repair released or refunded %+v %v", queue, err)
				}
				if manual {
					if pg, ok := s.(*state.PgStore); ok {
						s = state.NewPgStore((*pg.Pool()).Pool)
						evidence = s.(state.WorkflowInitialEvidenceStore)
					}
					if _, err = s.(state.WorkflowInitialManualCompletionStore).ProcessNextWorkflowInitialManualCompletion(ctx, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				} else {
					state.PollInitialAutomatic(t, s, time.Now().UTC().Add(time.Minute))
				}
				final := state.InitialAutomaticReview(t, s, app, r.ID)
				diagnosed, err := evidence.DiagnoseWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID)
				if err != nil || diagnosed.Repairable || diagnosed.Reason != "already_confirmed" {
					t.Fatalf("confirmed diagnosis %+v %v", diagnosed, err)
				}
				result := final.AutomaticResult
				if manual {
					result = final.Result
					if final.AutomaticResult != nil {
						t.Fatal("manual repair set automatic result")
					}
				}
				if result == nil || result.ResumeCount != 1 || result.Outcome != repaired.Repair.Outcome {
					t.Fatalf("worker reconciliation %+v", final)
				}
				if manual {
					if final.AutomaticEvaluation != nil {
						t.Fatal("manual repair changed automatic evaluation")
					}
					current, err := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					if current.AutomaticExecution != nil && (current.AutomaticExecution.ConsecutiveFailures != 0 || (current.AutomaticExecution.PausedAt != nil) != (scenario == "failed")) {
						t.Fatal("repair changed automatic counters or pause")
					}
					req.Reason = "reconcile verified terminal outcome"
					replayed, err := evidence.RepairWorkflowInitialEvidence(ctx, app.AccountID, app.ID, r.ID, req)
					if err != nil || replayed.Reason != "already_confirmed" || !reflect.DeepEqual(replayed.Repair, repaired.Repair) {
						t.Fatalf("confirmed repair replay %+v %v", replayed, err)
					}
					history, err := s.(state.WorkflowRecoveryHistoryStore).GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, api.WorkflowRecoveryHistoryOptions{RunID: run.ID, Limit: 100})
					if err != nil {
						t.Fatal(err)
					}
					counts := map[string]int{}
					for _, e := range history.Entries {
						if e.ReviewID == r.ID && (e.Kind == "initial_evidence_repaired" || e.Kind == "initial_recovery_result") {
							counts[e.Kind]++
							if !e.Durable || e.PolicyVersion != r.PolicyVersion || e.ResumeCount == nil || *e.ResumeCount != 1 {
								t.Fatalf("history provenance %+v", e)
							}
							if e.Kind == "initial_evidence_repaired" && (e.ActorID != "evidence-operator" || e.RequestID != req.RequestID) {
								t.Fatalf("repair actor %+v", e)
							}
						}
					}
					if counts["initial_evidence_repaired"] != 1 || counts["initial_recovery_result"] != 1 {
						t.Fatalf("history duplicates %+v", counts)
					}
				}

			})
		})
	}
}
