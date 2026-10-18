package state

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1274
func TestWorkflowInitialLifecycleHistory(t *testing.T) {
	for _, scenario := range []string{"success", "failed", "cancelled", "expired", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := WithEventRecoveryActor(context.Background(), "api_key", "lifecycle-operator")
				run, app, failure, p := seedInitialReview(t, s)
				if scenario == "success" || scenario == "failed" {
					p.Policy.Mode, p.Policy.HourlyExecutionBudget, p.Policy.PauseAfterFailures = "automatic_initial", 1, 1
					var err error
					p, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy})
					if err != nil {
						t.Fatal(err)
					}
				}
				reviews := s.(WorkflowInitialRecoveryReviewStore)
				request := api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version}
				r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, request)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, request); err != nil {
					t.Fatal(err)
				}
				history := s.(WorkflowRecoveryHistoryStore)
				options := api.WorkflowRecoveryHistoryOptions{RunID: run.ID, Workflow: run.WorkflowName, PolicyVersion: p.Version, Limit: 100}
				read := func() api.WorkflowRecoveryHistory {
					t.Helper()
					out, err := history.GetWorkflowRecoveryHistory(ctx, app.AccountID, app.ID, options)
					if err != nil {
						t.Fatal(err)
					}
					return out
				}
				before := read()
				if _, err = reviews.GetWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID); err != nil {
					t.Fatal(err)
				}
				after := read()
				if !reflect.DeepEqual(before.Entries, after.Entries) {
					t.Fatal("read appended lifecycle history")
				}
				expected := "initial_recovery_cancelled"
				switch scenario {
				case "cancelled":
					req := api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "cancel", Reason: "handled externally"}
					for range 2 {
						if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, req); err != nil {
							t.Fatal(err)
						}
					}
				case "expired":
					mutateInitialReview(t, s, r.ID, func(saved *api.WorkflowInitialRecoveryReview) {
						saved.PreparedAt = time.Now().UTC().Add(-2 * api.WorkflowInitialRecoveryReviewLifetime).Truncate(time.Microsecond)
						saved.ExpiresAt = saved.PreparedAt.Add(api.WorkflowInitialRecoveryReviewLifetime)
					})
					if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "submit observed review"}); err != nil {
						t.Fatal(err)
					}
					expected = "initial_recovery_expired"
				case "stale":
					p.Policy.Reason = "revised policy"
					if _, err = s.(WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: p.Version, Policy: p.Policy}); err != nil {
						t.Fatal(err)
					}
					if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "submit observed review"}); err != nil {
						t.Fatal(err)
					}
					expected = "initial_recovery_stale"
				default:
					req := api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve initial recovery"}
					for range 2 {
						if _, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, req); err != nil {
							t.Fatal(err)
						}
					}
					if _, err = s.(EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
						t.Fatal(err)
					}
					status := WorkflowRunStatusSucceeded
					if scenario == "failed" {
						status = WorkflowRunStatusFailed
					}
					if err = s.MarkWorkflowRunStatus(ctx, run.ID, status, nil, nil); err != nil {
						t.Fatal(err)
					}
					pollInitialAutomatic(t, s, time.Now().UTC())
					pollInitialAutomatic(t, s, time.Now().Add(time.Minute))
					expected = "initial_recovery_result"
				}
				out := read()
				counts := map[string]int{}
				for _, e := range out.Entries {
					if e.ReviewID != r.ID || e.Kind == "initial_evidence_repaired" {
						continue
					}
					counts[e.Kind]++
					if !e.Durable || e.InitialFailureID != failure || e.ActorKind == "" || e.ActorID == "" || e.ResumeCount == nil || *e.ResumeCount != 1 {
						t.Fatalf("provenance %+v", e)
					}
					if e.Kind == expected && expected == "initial_recovery_result" && e.Outcome != scenario {
						want := "succeeded"
						if scenario == "failed" {
							want = "failed"
						}
						if e.Outcome != want {
							t.Fatalf("result %+v", e)
						}
					}
				}
				if counts["initial_recovery_prepared"] != 1 || counts[expected] != 1 {
					t.Fatalf("events %+v", counts)
				}
				if scenario == "success" || scenario == "failed" {
					if counts["initial_recovery_submitted"] != 1 {
						t.Fatal("submission replay duplicated")
					}
				}
				if pg, ok := s.(*PgStore); ok {
					s = NewPgStore(pg.pool.Pool)
					history = s.(WorkflowRecoveryHistoryStore)
				}
				// Source review pruning must not erase durable lifecycle evidence.
				if m, ok := s.(*MemStore); ok {
					m.mu.Lock()
					delete(m.initialRecoveryReviews, r.ID)
					m.mu.Unlock()
				} else {
					if _, err = s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_initial_recovery_reviews WHERE id=$1::uuid", r.ID); err != nil {
						t.Fatal(err)
					}
				}
				found := 0
				for _, e := range read().Entries {
					if e.ReviewID == r.ID && (e.Kind == "initial_recovery_prepared" || e.Kind == expected) {
						found++
					}
				}
				if found != 2 {
					t.Fatalf("pruning lost events %d", found)
				}
			})
		})
	}
}
