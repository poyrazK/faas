package main

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIGitHubRequestsRequireExplicitImmutableBindings(t *testing.T) {
	req := ci.GitHubBindingRequest{RevisionID: uuid.NewString(), InstallationID: 41, RepositoryID: 123, PushBranches: ciBranches("main, develop"), PullRequestBranches: ciBranches("main")}
	method, path, body, err := ciGitHubRequest("bind", "checks", req)
	if err != nil || method != http.MethodPut || path != "/v1/internal/ci/github/bindings/checks" || body.(ci.GitHubBindingRequest).PushBranches[0] != "develop" {
		t.Fatalf("invalid binding request: %v", err)
	}
	req.PushBranches, req.PullRequestBranches = nil, nil
	if _, _, _, err := ciGitHubRequest("bind", "checks", req); err == nil {
		t.Fatal("implicit all-branch trigger allowed")
	}
	if _, _, _, err := ciGitHubRequest("unbind", "../other", req); err == nil {
		t.Fatal("unsafe binding path accepted")
	}
	if method, _, _, err := ciGitHubRequest("unbind", "checks", req); err != nil || method != http.MethodDelete {
		t.Fatal("unbind request malformed")
	}
}
