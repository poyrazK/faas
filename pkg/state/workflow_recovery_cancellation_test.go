package state_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestWorkflowRecoveryCancellation(t *testing.T) {
	for _, scenario := range []string{"cancel", "changed_request", "new_generation", "terminal", "unknown", "not_admitted", "missing_account"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
				ctx := state.WithEventRecoveryActor(context.Background(), "api_key", "review-key")
				run, app := state.SeedResumeRun(t, store, state.SimpleResumeSpec())
				state.FailResumeRun(t, store, run, "send")
				recovery := store.(state.EventRecoveryStore)
				job, err := recovery.CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
				if err != nil {
					t.Fatal(err)
				}
				if scenario != "not_admitted" {
					if did, err := recovery.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
						t.Fatalf("admit=%v %v", did, err)
					}
				}
				count := 1
				req := api.WorkflowRecoveryCancellationRequest{Position: 1, ExpectedResumeCount: &count, Reason: "provider incident"}
				controls := store.(state.WorkflowRecoveryCancellationStore)
				preview, err := controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, true)
				if err != nil || preview.Preview != true || scenario != "not_admitted" && preview.Outcome != "eligible" {
					t.Fatalf("preview=%+v %v", preview, err)
				}
				h, err := recovery.ListEventRecoveryHistory(ctx, app.AccountID, job.ID, 0, 100)
				if err != nil || len(h.Entries) != 1 {
					t.Fatalf("preview audit=%+v %v", h, err)
				}
				want := "cancelled"
				switch scenario {
				case "not_admitted":
					want = "not_admitted"
				case "changed_request":
					count = 2
					want = "changed"
				case "new_generation":
					if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
						t.Fatal(err)
					}
					state.FailResumeRun(t, store, run, "send")
					if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.ResumeOptions(run, app, 1)); err != nil {
						t.Fatal(err)
					}
					want = "changed"
				case "terminal":
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
					want = "already_terminal"
				case "unknown":
					if mem, ok := store.(*state.MemStore); ok {
						(*mem.Mu()).Lock()
						delete((*mem.WorkflowResumes()), run.ID)
						(*mem.Mu()).Unlock()
					} else {
						if _, err := (*store.(*state.PgStore).Pool()).Exec(ctx, "DELETE FROM workflow_run_resumes WHERE run_id=$1::uuid", run.ID); err != nil {
							t.Fatal(err)
						}
					}
					want = "unknown"
				case "missing_account":
					if _, err := controls.WorkflowRecoveryCancellation(ctx, uuid.NewString(), job.ID, req, false); err == nil {
						t.Fatal("foreign account cancelled run")
					}
					return
				}
				var out api.WorkflowRecoveryCancellationResponse
				if scenario == "cancel" {
					type result struct {
						out api.WorkflowRecoveryCancellationResponse
						err error
					}
					results := make(chan result, 2)
					for i := 0; i < 2; i++ {
						go func() {
							r, e := controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
							results <- result{r, e}
						}()
					}
					first, second := <-results, <-results
					if first.err != nil || second.err != nil {
						t.Fatalf("concurrent errors=%v %v", first.err, second.err)
					}
					if first.out.Outcome == "cancelled" {
						out = first.out
						if second.out.Outcome != "already_cancelled" {
							t.Fatalf("second=%+v", second.out)
						}
					} else {
						out = second.out
						if first.out.Outcome != "already_cancelled" {
							t.Fatalf("first=%+v", first.out)
						}
					}
				} else {
					out, err = controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
				}
				if err != nil || out.Outcome != want || out.Preview {
					t.Fatalf("outcome=%+v want=%s %v", out, want, err)
				}
				live, err := store.GetWorkflowRun(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "cancel" {
					if live.CancelledAt == nil || live.ResumeCount != 1 {
						t.Fatalf("cancelled=%+v", live)
					}
					duplicate, err := controls.WorkflowRecoveryCancellation(ctx, app.AccountID, job.ID, req, false)
					if err != nil || duplicate.Outcome != "already_cancelled" {
						t.Fatalf("duplicate=%+v %v", duplicate, err)
					}
					h, err = recovery.ListEventRecoveryHistory(ctx, app.AccountID, job.ID, 0, 100)
					if err != nil || len(h.Entries) != 2 {
						t.Fatalf("audit=%+v %v", h, err)
					}
					entry := h.Entries[1]
					if entry.Action != "workflow_cancelled" || entry.ActorKind != "api_key" || entry.ActorID != "review-key" || entry.Reason != req.Reason || entry.TargetPosition == nil || *entry.TargetPosition != 1 || entry.TargetRunID != run.ID || entry.TargetResumeCount == nil || *entry.TargetResumeCount != 1 {
						t.Fatalf("audit target=%+v", entry)
					}
				} else {
					if live.CancelledAt != nil {
						t.Fatalf("refused action cancelled=%+v", live)
					}
					h, err = recovery.ListEventRecoveryHistory(ctx, app.AccountID, job.ID, 0, 100)
					if err != nil || len(h.Entries) != 1 {
						t.Fatalf("refused audit=%+v %v", h, err)
					}
				}
			})
		})
	}
}
