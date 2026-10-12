package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryPolicyChangeRecentJobs(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		r, app, run, _ := seedAutomaticRecoveryReview(t, s, nil)
		newest := ""
		for i := 0; i < api.WorkflowRecoveryPolicyJobsPerPoll+1; i++ {
			job, err := s.(EventRecoveryStore).CreateEventRecovery(ctx, app.AccountID, app.ID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID, ExpectedResumeCount: 1}}}})
			if err != nil {
				t.Fatal(err)
			}
			newest = job.ID
			if _, err = s.(EventRecoveryStore).CancelEventRecovery(ctx, app.AccountID, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		// Requests created above have no admitted or saved failure yet. They
		// displace the older saved failure in the bounded newest-first sample.
		before := policyChangeEvidence(t, s, app.ID)
		out, err := s.(WorkflowRecoveryPolicyChangeStore).PreviewWorkflowRecoveryPolicyChange(ctx, app.AccountID, app.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: r.PolicyVersion, Policy: r.Policy})
		if err != nil {
			t.Fatal(err)
		}
		if !out.JobsTruncated || out.InspectedJobs != api.WorkflowRecoveryPolicyJobsPerPoll || len(out.Candidates) != 0 {
			t.Fatalf("recent bounds %+v newest %s", out, newest)
		}
		if string(before) != string(policyChangeEvidence(t, s, app.ID)) {
			t.Fatal("bounded preview wrote state")
		}
	})
}
func TestWorkflowRecoveryPolicyChangePreparationEligibility(t *testing.T) {
	for _, scenario := range []string{"eligible", "disabled", "generation", "budget", "cooldown"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			p := testAutomaticRecoveryPolicy("recover")
			b := recoveryPolicyBudget{Existing: map[string]bool{}}
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
			out := recoveryPolicyChangePreview(queue, p, nil, b)
			c := recoveryPolicyCandidate{Decision: api.WorkflowRecoveryPolicyDecision{RunID: uuid.NewString(), Position: 1, ResumeCount: 1, Action: "retry", Trigger: "saved_failure", Outcome: "eligible"}, Action: api.WorkflowRecoveryResponseAction{Kind: "retry", Position: 1, Retry: &api.EventRecoveryRequest{}}}
			addRecoveryPolicyChangeCandidates(&out, api.EventRecoveryJob{ID: uuid.NewString()}, []recoveryPolicyCandidate{c}, &b)
			if len(out.Candidates) != 1 || out.Candidates[0].Decision.Outcome != want {
				t.Fatalf("eligibility %+v", out)
			}
		})
	}
}
