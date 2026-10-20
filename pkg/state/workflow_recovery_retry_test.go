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

func TestWorkflowRecoveryReviewedRetries(t *testing.T) {
	for _, scenario := range []string{"queue", "changed", "parent_pruned", "success", "unknown", "wrong_position", "wrong_count"} {
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
				if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
					t.Fatalf("parent admission=%v %v", did, err)
				}
				outcome := "failed"
				if scenario == "success" {
					outcome = "succeeded"
				}
				if scenario != "unknown" && scenario != "success" {
					if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
						t.Fatal(err)
					}
					state.FailResumeRun(t, store, run, "send")
				}
				if scenario == "success" {
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, outcome, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				req := api.EventRecoveryRequest{RequestID: uuid.NewString(), ParentJobID: parent.ID, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, ExpectedResumeCount: 1, ParentPosition: 1}}}}
				if scenario == "wrong_position" {
					req.WorkflowRuns.Runs[0].ParentPosition = 2
				}
				if scenario == "wrong_count" {
					req.WorkflowRuns.Runs[0].ExpectedResumeCount = 2
				}
				_, err = recovery.PreviewEventRecovery(ctx, app.AccountID, app.ID, req)
				reject := scenario == "success" || scenario == "unknown" || scenario == "wrong_position" || scenario == "wrong_count"
				if reject {
					if !errors.Is(err, state.ErrEventRecoveryChanged) {
						t.Fatalf("unsafe preview=%v", err)
					}
					if _, err = recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req); !errors.Is(err, state.ErrEventRecoveryChanged) {
						t.Fatalf("unsafe create=%v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				child, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "changed" {
					if _, _, _, err = store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.ResumeOptions(run, app, 1)); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "parent_pruned" {
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						delete((*mem.EventRecoveryJobs()), state.CanonicalMemUUID(parent.ID))
						(*mem.Mu()).Unlock()
					} else {
						if _, err = (*store.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM event_recovery_jobs WHERE id=$1::uuid", parent.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				again, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil || again.ID != child.ID {
					t.Fatalf("idempotency=%+v %v", again, err)
				}
				if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC().Add(time.Second)); err != nil || !did {
					t.Fatalf("child admission=%v %v", did, err)
				}
				items, err := recovery.ListEventRecoveryItems(ctx, app.AccountID, child.ID, 0, 25)
				if err != nil {
					t.Fatal(err)
				}
				item := items.Items[0]
				want := "queued"
				if scenario == "changed" {
					want = "skipped"
				}
				if item.State != want || item.ParentJobID != parent.ID || (item.ParentPosition == nil || *item.ParentPosition != 1) || *item.ExpectedResumeCount != 1 {
					t.Fatalf("child item=%+v", item)
				}
			})
		})
	}
}
