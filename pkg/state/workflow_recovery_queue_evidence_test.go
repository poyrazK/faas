package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1271
func TestWorkflowRecoveryQueueEvidence(t *testing.T) {
	for _, scenario := range []string{"parent", "paused", "budget", "cooldown", "missing_run", "superseded", "child_running", "child_terminal"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := context.Background()
				review, app, run, now := state.SeedAutomaticRecoveryReview(t, s, nil)
				action := review.AutomaticExecution.Actions[0].ActionID
				job, kind := review.JobID, "parent"
				if strings.HasPrefix(scenario, "child_") {
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
					if scenario == "child_terminal" {
						if err = s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
							t.Fatal(err)
						}
					}
				}
				reason := ""
				if scenario == "budget" {
					reason = "hourly_budget"
				}
				if scenario == "cooldown" {
					reason = "cooldown"
				}
				if m, ok := s.(*state.MemStore); ok {
					(*m.Mu()).Lock()
					delete((*m.EventRecoveryJobs())[job].WorkflowResults, 1)
					if scenario == "superseded" {
						v := (*m.WorkflowRuns())[run.ID]
						v.ResumeCount = 2
						(*m.WorkflowRuns())[run.ID] = v
					}
					if reason != "" {
						(*m.RecoveryPolicies())[state.CanonicalMemUUID(app.ID)].Executions[action].LastEvaluation = state.RecoveryAutomaticEvaluation(nil, reason, time.Now().UTC())
					}
					(*m.Mu()).Unlock()
				} else {
					pg := s.(*state.PgStore)
					if _, err := (*pg.Pool()).Exec(ctx, "DELETE FROM event_recovery_workflow_results WHERE job_id=$1::uuid", job); err != nil {
						t.Fatal(err)
					}
					if scenario == "superseded" {
						tx, err := (*pg.Pool()).Begin(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = tx.Rollback(ctx) }()
						if _, err = tx.Exec(ctx, "ALTER TABLE workflow_runs DISABLE TRIGGER event_recovery_workflow_result"); err != nil {
							t.Fatal(err)
						}
						if _, err = tx.Exec(ctx, "UPDATE workflow_runs SET resume_count=2 WHERE id=$1::uuid", run.ID); err != nil {
							t.Fatal(err)
						}
						if _, err = tx.Exec(ctx, "ALTER TABLE workflow_runs ENABLE TRIGGER event_recovery_workflow_result"); err != nil {
							t.Fatal(err)
						}
						if err = tx.Commit(ctx); err != nil {
							t.Fatal(err)
						}
					}
					if reason != "" {
						raw, _ := json.Marshal(state.RecoveryAutomaticEvaluation(nil, reason, time.Now().UTC()))
						if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_recovery_policy_executions SET last_evaluation=$2::jsonb WHERE action_id=$1::uuid", action, raw); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "missing_run" {
					state.RemoveAutomaticLiveRun(t, s, run)
				}
				policy := s.(state.WorkflowRecoveryPolicyStore)
				if scenario == "paused" {
					p, err := policy.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.(state.WorkflowRecoveryPolicyOperatorStore).ControlWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, state.ControlRequest(p, "pause")); err != nil {
						t.Fatal(err)
					}
				}
				get := func() api.WorkflowRecoveryPolicyReview {
					r, err := policy.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, review.ID)
					if err != nil {
						t.Fatal(err)
					}
					return r
				}
				before := get()
				operator := s.(state.WorkflowRecoveryPolicyOperatorStore)
				out, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Entries) != 1 || out.Entries[0].Evidence == nil {
					t.Fatalf("queue %+v", out)
				}
				entry := out.Entries[0]
				d := entry.Evidence.Diagnosis
				count := 1
				if kind == "child" {
					count = 2
				}
				repairable := scenario == "parent" || scenario == "paused" || scenario == "budget" || scenario == "cooldown" || scenario == "child_terminal"
				if d.EvidenceKind != kind || d.ResumeCount != count || d.JobID != job || d.RunID != run.ID || d.Repairable != repairable || !d.ObservedAt.Equal(out.ObservedAt) || entry.Evidence.EvidenceURL == "" || entry.Evidence.CLICommand == "" {
					t.Fatalf("diagnosis %+v", entry)
				}
				if strings.Contains(entry.Evidence.CLICommand, "--parent") != (kind == "parent") {
					t.Fatal("wrong inspection command")
				}
				if scenario == "missing_run" && d.Reason != "run_not_retained" {
					t.Fatal(d.Reason)
				}
				if scenario == "superseded" && d.Reason != "generation_superseded" {
					t.Fatal(d.Reason)
				}
				if scenario == "paused" && (entry.WaitCategory != "policy" || entry.WaitReason != "automatic_paused") {
					t.Fatal("pause not distinguished")
				}
				if scenario == "budget" && entry.WaitCategory != "budget" {
					t.Fatal("budget not distinguished")
				}
				if scenario == "cooldown" && entry.WaitCategory != "cooldown" {
					t.Fatal("cooldown not distinguished")
				}
				if !reflect.DeepEqual(before, get()) {
					t.Fatal("queue read mutated review/receipt/evaluation")
				}
				if _, err = operator.GetWorkflowRecoveryAutomaticQueue(ctx, uuid.NewString(), app.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("foreign scope %v", err)
				}
				second, err := operator.GetWorkflowRecoveryAutomaticQueue(ctx, app.AccountID, app.ID)
				if err != nil || second.HourlyExecutions != out.HourlyExecutions || second.Outstanding != out.Outstanding || second.Pending != out.Pending || second.Entries[0].Evidence.Diagnosis.Repairable != repairable {
					t.Fatal("read changed admission counters")
				}
			})
		})
	}
}
