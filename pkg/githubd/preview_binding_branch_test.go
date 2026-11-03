package githubd

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// pullRequestOpenedOnBranches is an opened delivery with GitHub's base branch
// and repository default branch, the fields the binding lookup keys on.
func pullRequestOpenedOnBranches(prNumber int, headSHA, base, defaultBranch string) []byte {
	return []byte(`{
		"action": "opened",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "open",
			"head": {"ref": "feature/x", "sha": "` + headSHA + `", "repo": {"full_name": "octo/api"}},
			"base": {"ref": "` + base + `"}
		},
		"repository": {"full_name": "octo/api", "name": "api", "default_branch": "` + defaultBranch + `"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

// Bindings are keyed by (repository, production branch) and the stores read
// an empty branch as "main", so a repository bound on master never got a PR
// preview: the delivery completed as unbound, with no check and no comment.
func TestHandlePullRequest_BindingOnNonMainProductionBranch(t *testing.T) {
	cases := []struct {
		name, base, defaultBranch string
	}{
		{name: "PR into the production branch", base: "master", defaultBranch: "master"},
		{name: "PR into another branch of a master repository", base: "release", defaultBranch: "master"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newPreviewRig(t)
			svc, _ := newPreviewService(t, rig)
			binding := svc.Bindings.(*stubBindings).byRepo["octo/api|"]
			binding.ProductionBranch = "master"
			svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{"octo/api|master": binding}}

			result, err := svc.handlePullRequest(context.Background(),
				pullRequestOpenedOnBranches(7, "deadbeef00000000000000000000000000000000", tc.base, tc.defaultBranch))
			if err != nil {
				t.Fatalf("handlePullRequest: %v", err)
			}
			if len(result.Added) != 1 || result.Added[0].Slug != "pr-7-demo-app" {
				t.Fatalf("result.Added = %+v, want the pr-7-demo-app preview", result.Added)
			}
		})
	}
}

// A PR into a branch that has its own binding previews that branch's app.
func TestHandlePullRequest_BaseBranchBindingWins(t *testing.T) {
	rig := newPreviewRig(t)
	svc, _ := newPreviewService(t, rig)
	staging, err := rig.mem.CreateApp(context.Background(), state.App{
		AccountID: rig.acct, Slug: "demo-staging", Type: state.AppTypeApp, RAMMB: 256,
		MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed staging app: %v", err)
	}
	production := svc.Bindings.(*stubBindings).byRepo["octo/api|"]
	stagingBinding := production
	stagingBinding.BindingID, stagingBinding.AppID, stagingBinding.ProductionBranch = "b-staging", staging.ID, "staging"
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{
		"octo/api|main": production, "octo/api|staging": stagingBinding,
	}}

	result, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedOnBranches(8, "deadbeef00000000000000000000000000000000", "staging", "main"))
	if err != nil {
		t.Fatalf("handlePullRequest: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0].Slug != "pr-8-demo-staging" {
		t.Fatalf("result.Added = %+v, want the staging app's preview", result.Added)
	}
}
