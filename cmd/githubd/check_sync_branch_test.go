package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261113121342: a branch preview build (kind preview on a production
// app, in the pull request's branch scope) reports the branch's stable URL;
// every other deployment keeps its own.
func TestPullRequestBranchURL(t *testing.T) {
	const project, app = "11111111-2222-4333-8444-555555555555", "66666666-7777-4888-8999-aaaaaaaaaaaa"
	want := githubd.EnvironmentBranchWorkloadURL(project, "pr-7", app)
	if want == "" || want != "https://"+hostOf(t, want) {
		t.Fatalf("branch URL = %q", want)
	}
	for _, tc := range []struct {
		name, kind, previewOf, scope, want string
	}{
		{"branch preview build", string(state.DeploymentKindPreview), "", "pr-7", want},
		{"app preview build", string(state.DeploymentKindPreview), "shop", "default", ""},
		{"preview build in default scope", string(state.DeploymentKindPreview), "", "default", ""},
		{"push to an environment", string(state.DeploymentKindGitHub), "", "pr-7", ""},
	} {
		if got := pullRequestBranchURL(tc.kind, tc.previewOf, tc.scope, project, app); got != tc.want {
			t.Errorf("%s: URL = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := pullRequestBranchURL(string(state.DeploymentKindPreview), "", "pr-7", "", app); got != "" {
		t.Errorf("an app without a project got branch URL %q", got)
	}
}

func hostOf(t *testing.T, url string) string {
	t.Helper()
	const scheme = "https://"
	if len(url) <= len(scheme) || url[:len(scheme)] != scheme {
		t.Fatalf("URL %q is not https", url)
	}
	return url[len(scheme):]
}
