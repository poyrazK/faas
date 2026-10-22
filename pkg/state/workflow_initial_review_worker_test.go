package state_test

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowInitialReviewWorker(t *testing.T) {
	for _, scenario := range []string{"prepare", "disabled", "excluded", "pending", "cancelled", "cursor"} {
		t.Run(scenario, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := context.Background()
				run, app, failure, p := state.SeedInitialReview(t, s)
				policies := s.(state.WorkflowRecoveryPolicyStore)
				reviews := s.(state.WorkflowInitialRecoveryReviewStore)
				switch scenario {
				case "disabled", "excluded":
					if scenario == "disabled" {
						p.Policy.Enabled = false
					} else {
						p.Policy.FailureStatuses = []string{"failed"}
					}
					var err error
					p, err = policies.SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
					if err != nil {
						t.Fatal(err)
					}
				case "pending":
					request := state.InitialReviewJobRequest(api.WorkflowInitialRecoveryReview{ID: failure, Policy: p.Policy, Failure: api.WorkflowInitialFailure{Workflow: run.WorkflowName}, Target: api.WorkflowRunRecoveryTarget{RunID: run.ID, ExpectedResumeCount: 0, FailureEvidenceID: failure}}, "manual pending")
					if _, err := s.(state.EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, request); err != nil {
						t.Fatal(err)
					}
				case "cursor":
					for i := range api.WorkflowRecoveryPolicyJobsPerPoll + 1 {
						id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
						if m, ok := s.(*state.MemStore); ok {
							(*m.Mu()).Lock()
							e := (*m.WorkflowFailureEvidence())[state.CanonicalMemUUID(app.ID)][0]
							e.ID, e.RunID = id, uuid.NewString()
							(*m.WorkflowFailureEvidence())[state.CanonicalMemUUID(app.ID)] = append((*m.WorkflowFailureEvidence())[state.CanonicalMemUUID(app.ID)], e)
							(*m.Mu()).Unlock()
						} else {
							pg := s.(*state.PgStore)
							_, err := (*pg.Pool()).Exec(ctx, "INSERT INTO workflow_recovery_failure_evidence(id,app_id,run_id,workflow,resume_count,failure_status,observed_at,completed_at) SELECT $1::uuid,app_id,$2::uuid,workflow,resume_count,failure_status,observed_at,completed_at FROM workflow_recovery_failure_evidence WHERE id=$3::uuid", id, uuid.NewString(), failure)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
				case "cancelled":
					r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
					if err != nil {
						t.Fatal(err)
					}
					if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "cancel", Reason: "operator dismissed"}); err != nil {
						t.Fatal(err)
					}
				}
				now := time.Now().UTC().Add(time.Second)
				for i := range 2 {
					if _, err := policies.ProcessNextWorkflowRecoveryPolicy(ctx, now.Add(time.Duration(i)*time.Minute)); err != nil {
						t.Fatal(err)
					}
					if scenario == "cursor" && i == 0 {
						page, err := reviews.ListWorkflowInitialRecoveryReviews(ctx, app.AccountID, app.ID, "", 100)
						if err != nil || len(page.Reviews) != 0 {
							t.Fatalf("first bounded page %+v %v", page, err)
						}
					}
				}
				page, err := reviews.ListWorkflowInitialRecoveryReviews(ctx, app.AccountID, app.ID, "", 100)
				if err != nil {
					t.Fatal(err)
				}
				expected := 0
				if scenario == "prepare" || scenario == "cancelled" || scenario == "cursor" {
					expected = 1
				}
				if len(page.Reviews) != expected {
					t.Fatalf("reviews %+v, want %d", page, expected)
				}
				if expected == 1 {
					r := page.Reviews[0]
					if r.JobID != "" {
						t.Fatalf("dry run submitted job: %+v", r)
					}
					if scenario == "prepare" && (r.Status != "prepared" || len(r.Events) != 1 || r.Events[0].ActorKind != "system" || r.Events[0].ActorID != "initial_recovery_policy") {
						t.Fatalf("worker review %+v", r)
					}
					if scenario == "cancelled" && r.Status != "cancelled" {
						t.Fatalf("cancelled review recreated: %+v", r)
					}
				}
				current, err := s.GetWorkflowRun(ctx, run.ID)
				if err != nil || current.ResumeCount != 0 {
					t.Fatalf("dry run resumed run: %+v %v", current, err)
				}
			})
		})
	}
}

func TestWorkflowInitialReviewWorkerCursor(t *testing.T) {
	ids := make([]string, api.WorkflowRecoveryPolicyJobsPerPoll)
	for i := range ids {
		ids[i] = "candidate"
	}
	if state.InitialPolicyNextCursor(ids) != "candidate" {
		t.Fatal("full page must advance")
	}
	if state.InitialPolicyNextCursor(ids[:len(ids)-1]) != "" {
		t.Fatal("short page must wrap to revisit blockers")
	}
}
