package api

import "testing"

func TestWorkflowInitialReviewDecisionValidation(t *testing.T) {
	for _, tc := range []struct {
		decision, reason string
		valid            bool
	}{{"submit", "reviewed", true}, {"cancel", "handled manually", true}, {"submit", "", false}, {"cancel", " \n ", false}, {"automatic", "reviewed", false}} {
		err := (WorkflowInitialRecoveryReviewDecisionRequest{Decision: tc.decision, Reason: tc.reason}).Validate()
		if (err == nil) != tc.valid {
			t.Fatalf("decision %q reason %q: %v", tc.decision, tc.reason, err)
		}
	}
	if (WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: 0}).Validate() == nil {
		t.Fatal("zero policy version accepted")
	}
}
