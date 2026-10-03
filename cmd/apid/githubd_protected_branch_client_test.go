package main

import "testing"

func TestStubGithubdProtectedBranchEvidenceCannotQualify(t *testing.T) {
	evidence, err := (stubGithubdClient{}).GetProtectedBranchEvidence(t.Context(), "owner", 42, 123, "octo/api", "main", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assertGithubdNotReadyError(t, "GetProtectedBranchEvidence", err)
	if evidence.Qualified {
		t.Fatal("unconfigured GitHub transport qualified a policy")
	}
}
