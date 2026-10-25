package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestWorkflowRecoveryPreflight(t *testing.T) {
	for _, scenario := range []string{"eligible", "changed", "unsafe", "capacity", "limit", "missing", "unknown", "succeeded"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
				state.FailResumeRun(t, store, run, "send")
				recovery := store.(state.EventRecoveryStore)
				parent, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				pending, err := store.(state.EventRecoveryPreflightStore).GetEventRecoveryPreflight(ctx, app.AccountID, parent.ID, time.Now().UTC())
				if err != nil || pending.EligibleCount != 1 || pending.PendingCount != 1 || pending.Sample[0].WorkflowRunID != run.ID {
					t.Fatalf("pending=%+v %v", pending, err)
				}
				if _, err = store.(state.WorkflowRecoveryRetryPreflightStore).GetWorkflowRecoveryRetryPreflight(ctx, app.AccountID, parent.ID, time.Now().UTC()); !errors.Is(err, state.ErrEventRecoveryRetryParent) {
					t.Fatalf("active parent=%v", err)
				}
				if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
					t.Fatalf("admit=%v %v", did, err)
				}
				if scenario != "unknown" && scenario != "succeeded" {
					if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
						t.Fatal(err)
					}
					state.FailResumeRun(t, store, run, "send")
				}
				if scenario == "succeeded" {
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "changed" {
					if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.ResumeOptions(run, app, 1)); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "unsafe" {
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						a := (*mem.Apps())[app.ID]
						a.MaintenanceMode = true
						(*mem.Apps())[app.ID] = a
						(*mem.Mu()).Unlock()
					} else {
						if _, err := (*store.(*state.PgStore).Pool()).Exec(ctx, "UPDATE apps SET maintenance_mode=true WHERE id=$1::uuid", app.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "capacity" {
					for i := 0; i < api.PlanHobby.WorkflowMaxConcurrentRuns(); i++ {
						other := &state.WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, DefinitionSnapshot: run.DefinitionSnapshot, Input: run.Input}
						if err := store.CreateWorkflowRun(ctx, other); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "missing" {
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						delete((*mem.WorkflowRuns()), run.ID)
						(*mem.Mu()).Unlock()
					} else {
						if _, err := (*store.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "limit" { // Seed the retained generation at the supported resume boundary.
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						r := (*mem.WorkflowRuns())[run.ID]
						r.ResumeCount = api.WorkflowRunMaxResumes
						(*mem.WorkflowRuns())[run.ID] = r
						job := (*mem.EventRecoveryJobs())[state.CanonicalMemUUID(parent.ID)]
						n := api.WorkflowRunMaxResumes - 1
						job.Items[0].ExpectedResumeCount = &n
						saved := job.WorkflowResults[1]
						saved.ResumeNumber = api.WorkflowRunMaxResumes
						job.WorkflowResults[1] = saved
						(*mem.Mu()).Unlock()
					} else {
						pg := store.(*state.PgStore)
						if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_runs SET resume_count=$2 WHERE id=$1::uuid", run.ID, api.WorkflowRunMaxResumes); err != nil {
							t.Fatal(err)
						}
						if _, err := (*pg.Pool()).Exec(ctx, "UPDATE event_recovery_items SET expected_progress=jsonb_set(expected_progress,'{expected_resume_count}',to_jsonb($2::int)) WHERE job_id=$1::uuid", parent.ID, api.WorkflowRunMaxResumes-1); err != nil {
							t.Fatal(err)
						}
						if _, err := (*pg.Pool()).Exec(ctx, "UPDATE event_recovery_workflow_results SET resume_number=$2 WHERE job_id=$1::uuid", parent.ID, api.WorkflowRunMaxResumes); err != nil {
							t.Fatal(err)
						}
					}
				}
				review, err := store.(state.WorkflowRecoveryRetryPreflightStore).GetWorkflowRecoveryRetryPreflight(ctx, app.AccountID, parent.ID, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				reason := map[string]string{"eligible": "eligible", "changed": "changed", "unsafe": "resume_ineligible", "capacity": "capacity", "limit": "resume_limit_reached", "missing": "run_not_retained", "unknown": "outcome_not_saved", "succeeded": "outcome_not_failed"}[scenario]
				if review.ReviewMode != "workflow_retry" || review.ReviewedCount != 1 || review.PendingCount != 0 || len(review.Sample) != 1 || review.Sample[0].Reason != reason {
					t.Fatalf("review=%+v item=%+v", review, review.Sample)
				}
				if scenario == "capacity" && (review.WaitingCount != 1 || review.Sample[0].CapacityScope != "app") {
					t.Fatalf("capacity=%+v", review)
				}
				if scenario == "unknown" && review.UnknownCount != 1 {
					t.Fatalf("unknown=%+v", review)
				}
				if _, err := store.(state.WorkflowRecoveryRetryPreflightStore).GetWorkflowRecoveryRetryPreflight(ctx, uuid.NewString(), parent.ID, time.Now().UTC()); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("tenant=%v", err)
				}
				resumes, err := store.(state.WorkflowResumeStore).ListWorkflowResumes(ctx, run.ID)
				if scenario != "missing" && (err != nil || len(resumes) != map[bool]int{true: 2, false: 1}[scenario == "changed"]) {
					t.Fatalf("preflight mutated resumes=%+v %v", resumes, err)
				}
			})
		})
	}
}

func TestWorkflowRecoveryPreflightPermanentBlockerPriority(t *testing.T) {
	count := 1
	for _, scenario := range []string{"unsafe_and_capacity", "operation_owned"} {
		t.Run(scenario, func(t *testing.T) {
			row := api.EventRecoveryPreflightItem{WorkflowRunID: "run", ExpectedResumeCount: &count}
			diagnostics := api.WorkflowRunDiagnosticsResponse{RunID: "run", WorkflowName: "recover", Status: "failed", Resume: api.WorkflowResumePreview{ExpectedResumeCount: count, Blockers: []api.WorkflowDiagnosticBlocker{{Code: "active_run_quota"}}}}
			if scenario == "unsafe_and_capacity" {
				diagnostics.Resume.Blockers = append(diagnostics.Resume.Blockers, api.WorkflowDiagnosticBlocker{Code: "maintenance"})
			}
			if err := state.WorkflowPreflightDiagnostics(&row, "recover", diagnostics, nil, scenario == "operation_owned"); err != nil || row.Reason != "resume_ineligible" || row.CapacityScope != "" {
				t.Fatalf("classification=%+v %v", row, err)
			}
		})
	}
}
