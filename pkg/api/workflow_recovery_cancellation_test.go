package api

import "testing"

func TestWorkflowRecoveryCancellationValidation(t *testing.T) {
	for _, count := range []int{0, 1, 16, 17} {
		r := WorkflowRecoveryCancellationRequest{Position: 1, ExpectedResumeCount: &count, Reason: "incident"}
		if err := r.Validate(); (err == nil) != (count >= 1 && count <= 16) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	count := 1
	for _, r := range []WorkflowRecoveryCancellationRequest{{Position: 1, Reason: "incident"}, {Position: 0, ExpectedResumeCount: &count, Reason: "incident"}, {Position: 26, ExpectedResumeCount: &count, Reason: "incident"}, {Position: 1, ExpectedResumeCount: &count, Reason: "  "}, {Position: 1, ExpectedResumeCount: &count, Reason: "line\n"}} {
		if r.Validate() == nil {
			t.Fatalf("invalid accepted=%+v", r)
		}
	}
}

func TestWorkflowRecoveryActionIdentifier(t *testing.T) {
	count := 1
	for _, id := range []string{"invalid", "00000000-0000-0000-0000-000000000000", "A1234567-1234-1234-1234-123456789ABC"} {
		req := WorkflowRecoveryCancellationRequest{RequestID: id, Position: 1, ExpectedResumeCount: &count, Reason: "incident"}
		if req.Validate() == nil {
			t.Fatalf("accepted invalid action ID %s", id)
		}
	}
}
