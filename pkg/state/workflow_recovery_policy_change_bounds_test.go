package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowRecoveryPolicyChangeRecentJobs(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		r, app, run, _ := state.SeedAutomaticRecoveryReview(t, s, nil)
		newest := ""
		for i := 0; i < api.WorkflowRecoveryPolicyJobsPerPoll+1; i++ {
			job, err := s.(state.EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, ExpectedResumeCount: 1}}}})
			if err != nil {
				t.Fatal(err)
			}
			newest = job.ID
			if _, err = s.(state.EventRecoveryStore).CancelEventRecovery(ctx, app.AccountID, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		// Requests created above have no admitted or saved failure yet. They
		// displace the older saved failure in the bounded newest-first sample.
		before := state.PolicyChangeEvidence(t, s, app.ID)
		out, err := s.(state.WorkflowRecoveryPolicyChangeStore).PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: r.PolicyVersion, Policy: r.Policy})
		if err != nil {
			t.Fatal(err)
		}
		if !out.JobsTruncated || out.InspectedJobs != api.WorkflowRecoveryPolicyJobsPerPoll || len(out.Candidates) != 0 {
			t.Fatalf("recent bounds %+v newest %s", out, newest)
		}
		if string(before) != string(state.PolicyChangeEvidence(t, s, app.ID)) {
			t.Fatal("bounded preview wrote state")
		}
	})
}
func TestWorkflowRecoveryPolicyChangePreparationEligibility(t *testing.T) {
	for _, scenario := range []string{"eligible", "disabled", "generation", "budget", "cooldown"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			p := state.TestAutomaticRecoveryPolicy("recover")
			b := state.RecoveryPolicyBudget{Existing: map[string]bool{}}
			want := "would_prepare"
			switch scenario {
			case "disabled":
				p.Enabled = false
				want = "policy_disabled"
			case "generation":
				p.MaxResumeCount = 1
				want = "generation_limit"
			case "budget":
				b.Hourly = p.HourlyActionBudget
				want = "hourly_budget"
			case "cooldown":
				b.Last = now
				want = "cooldown"
			}
			queue := api.WorkflowRecoveryAutomaticQueue{Policy: api.WorkflowRecoveryPolicyResponse{AppID: uuid.NewString(), Version: 1}, ObservedAt: now}
			out := state.RecoveryPolicyChangePreview(queue, p, nil, b)
			c := state.RecoveryPolicyCandidate{Decision: api.WorkflowRecoveryPolicyDecision{RunID: uuid.NewString(), Position: 1, ResumeCount: 1, Action: "retry", Trigger: "saved_failure", Outcome: "eligible"}, Action: api.WorkflowRecoveryResponseAction{Kind: "retry", Position: 1, Retry: &api.EventRecoveryRequest{}}}
			state.AddRecoveryPolicyChangeCandidates(&out, api.EventRecoveryJob{ID: uuid.NewString()}, []state.RecoveryPolicyCandidate{c}, &b)
			if len(out.Candidates) != 1 || out.Candidates[0].Decision.Outcome != want {
				t.Fatalf("eligibility %+v", out)
			}
		})
	}
}
