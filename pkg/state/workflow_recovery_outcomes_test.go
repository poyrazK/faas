package state_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryEvidenceObservations(t *testing.T) {
	now := time.Now().UTC()
	admitted := now.Add(-time.Minute)
	old := admitted.Add(-time.Second)
	future := now.Add(time.Second)
	for _, tc := range []struct {
		name     string
		admitted *time.Time
		updated  time.Time
		finished *time.Time
		count    int
	}{
		{"missing admission", nil, now, nil, 1},
		{"future admission", &future, future, nil, 1},
		{"older run generation", &admitted, now, nil, 0},
		{"run predates admission", &admitted, old, nil, 1},
		{"future run", &admitted, future, nil, 1},
		{"old completion", &admitted, now, &old, 1},
		{"future completion", &admitted, now, &future, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := state.WorkflowRecoveryExecution(now, state.WorkflowRunStatusSucceeded, tc.count, 0, tc.admitted, tc.updated, tc.finished, false)
			if got.State != "unknown" || got.CompletedAt != nil {
				t.Fatalf("inconsistent evidence attributed success: %+v", got)
			}
		})
	}
}

func TestWorkflowRecoveryExecutionObservations(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
		state.FailResumeRun(t, store, run, "send")
		recovery := store.(state.EventRecoveryStore)
		job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
		if err != nil {
			t.Fatal(err)
		}
		before, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
		if err != nil || before.Execution == nil || before.Execution.TrackedCount != 0 {
			t.Fatalf("before admission=%+v %v", before, err)
		}
		if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
			t.Fatalf("admission=%v %v", did, err)
		}
		if pg, ok := store.(*state.PgStore); ok {
			recovery = state.NewPgStore((*pg.Pool()).Pool)
		}
		check := func(want string) {
			t.Helper()
			saved, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			items, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(items.Items) != 1 || items.Items[0].Execution == nil {
				t.Fatalf("items=%+v", items)
			}
			observed := items.Items[0].Execution
			terminal := want == "failed" || want == "succeeded" || want == "cancelled" || want == "superseded"
			source := "workflow_run"
			savedCount := int64(0)
			if terminal {
				source = "recovery_result"
				savedCount = 1
			}
			if observed.State != want || observed.Attempts != 0 || (observed.RecordedAt != nil) != terminal || observed.Source != source {
				t.Fatalf("execution=%+v want=%s", observed, want)
			}
			if saved.State != "completed" || saved.QueuedCount != 1 || saved.Execution == nil || saved.Execution.TrackedCount != 1 || saved.Execution.SavedResults != savedCount {
				t.Fatalf("admission/summary=%+v", saved)
			}
			sum := saved.Execution
			if sum.Queued+sum.Running+sum.Retrying+sum.Succeeded+sum.Failed+sum.DeadLettered+sum.Expired+sum.Cancelled+sum.Superseded+sum.Unknown != 1 {
				t.Fatalf("summary=%+v", sum)
			}
			buckets := map[string]int64{"queued": sum.Queued, "running": sum.Running, "failed": sum.Failed, "succeeded": sum.Succeeded, "superseded": sum.Superseded, "cancelled": sum.Cancelled}
			if buckets[want] != 1 {
				t.Fatalf("summary state=%+v want=%s", sum, want)
			}
			if want == "superseded" && observed.CompletedAt != nil {
				t.Fatal("later resume leaked completion time")
			}
			data, _ := json.Marshal(items)
			if strings.Contains(string(data), "provider unavailable") || strings.Contains(string(data), "enabled") {
				t.Fatal("private workflow state leaked")
			}
		}
		check("queued")
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		check("running")
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusAwaitingEvent, nil, nil); err != nil {
			t.Fatal(err)
		}
		check("running")
		current := state.WithWorkflowRunGeneration(ctx, run.ID, 1)
		if err := store.MarkWorkflowRunStatus(current, run.ID, state.WorkflowRunStatusPending, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(current); err != nil {
			t.Fatal(err)
		}
		step := state.GuardStep(t, store, run.ID, "send")
		if _, err := store.StartWorkflowStep(current, run.ID, "send", step.Attempt+1, run.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(current, run.ID, "send", state.WorkflowStepStatusDead, step.Attempt+1, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(current, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
			t.Fatal(err)
		}
		check("failed")
		// First confirmed failure stays authoritative across later resumes.
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.ResumeOptions(run, app, 1)); err != nil {
			t.Fatal(err)
		}
		check("failed")
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		check("failed")
		records, err := store.(state.WorkflowResumeStore).ListWorkflowResumes(ctx, run.ID)
		if err != nil || len(records) != 2 {
			t.Fatalf("reads mutated resumes=%+v %v", records, err)
		}
		if _, err := recovery.GetEventRecovery(ctx, uuid.NewString(), job.ID); err == nil {
			t.Fatal("cross-account observation exposed")
		}
		page, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 1, 1)
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("page=%+v %v", page, err)
		}
	})
}

func TestWorkflowRecoveryTerminalObservations(t *testing.T) {
	for _, scenario := range []string{"succeeded", "dead", "cancelled", "missing_run", "missing_resume"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
				state.FailResumeRun(t, store, run, "send")
				recovery := store.(state.EventRecoveryStore)
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				want := scenario
				switch scenario {
				case "succeeded":
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
				case "dead":
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
						t.Fatal(err)
					}
					want = "failed"
				case "cancelled":
					if _, err := store.CancelWorkflowRun(ctx, run.ID, "cancelled by operator"); err != nil {
						t.Fatal(err)
					}
				case "missing_run", "missing_resume":
					want = "unknown"
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						if scenario == "missing_run" {
							delete((*mem.WorkflowRuns()), run.ID)
						} else {
							delete((*mem.WorkflowResumes()), run.ID)
						}
						(*mem.Mu()).Unlock()
					} else {
						pg := store.(*state.PgStore)
						query := "DELETE FROM workflow_runs WHERE id=$1::uuid"
						if scenario == "missing_resume" {
							query = "DELETE FROM workflow_run_resumes WHERE run_id=$1::uuid"
						}
						if _, err := (*pg.Pool()).Exec(ctx, query, run.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				saved, err := recovery.GetEventRecovery(ctx, app.AccountID, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				items, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 1)
				if err != nil {
					t.Fatal(err)
				}
				observed := items.Items[0].Execution
				if observed == nil || observed.State != want || saved.Execution == nil || saved.Execution.TrackedCount != 1 {
					t.Fatalf("execution=%+v job=%+v", observed, saved)
				}
				if want == "unknown" {
					if observed.Source != "unavailable" || observed.CompletedAt != nil {
						t.Fatalf("unknown=%+v", observed)
					}
				} else {
					if observed.Source != "recovery_result" || observed.EvidenceSource != "workflow_run" || observed.RecordedAt == nil || saved.Execution.SavedResults != 1 || observed.CompletedAt == nil {
						t.Fatalf("terminal=%+v", observed)
					}
				}
			})
		})
	}
}
