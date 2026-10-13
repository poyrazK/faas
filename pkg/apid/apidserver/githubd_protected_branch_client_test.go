package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestStubGithubdProtectedBranchEvidenceCannotQualify(t *testing.T) {
	evidence, err := (apidserver.StubGithubdClient{}).GetProtectedBranchEvidence(t.Context(), "owner", 42, 123, "octo/api", "main", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assertGithubdNotReadyError(t, "GetProtectedBranchEvidence", err)
	if evidence.Qualified {
		t.Fatal("unconfigured GitHub transport qualified a policy")
	}
}
