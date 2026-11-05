package githubd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/state"
)

// skipIgnoredPullRequestPush applies the project's ignored paths to a PR
// push, as push deploys already do. When everything the push changed since
// the preview's recorded head is ignored and that head is live, nothing is
// rebuilt: the preview keeps serving the recorded head, its lease is
// renewed, and the new head's gregale-preview check passes with that
// explanation. A failing, building or unknown preview, a compare GitHub
// cannot answer exactly (a force push, a truncated diff) or an empty diff
// rebuilds as before.
func (s *Service) skipIgnoredPullRequestPush(ctx context.Context, ev PullRequestEvent, installationID int64,
	policy state.GitHubDeployPolicy, previewURL string, expiresAt time.Time) (bool, error) {
	if ev.Action != PullRequestActionSynchronize || len(policy.IgnoredPaths) == 0 {
		return false, nil
	}
	sets, ok := s.Reconcile.Store.(state.PRPreviewSetStore)
	reader, readable := s.Reconcile.Store.(state.PRPreviewEnvironmentReader)
	if !ok || !readable {
		return false, nil
	}
	set, err := sets.GetPRPreviewSet(ctx, installationID, ev.Repository.FullName, ev.Number)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("githubd: load PR preview set: %w", err)
	}
	if set.Closed || set.CommitSHA == ev.PullRequest.HeadSHA {
		return false, nil
	}
	environment, err := reader.PRPreviewEnvironmentByRoot(ctx, set.RootAppID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("githubd: load PR preview environment: %w", err)
	}
	if previewset.EvaluateSet(set.PRNumber, set.RootAppID, environment.Members, environment.Set.SetupError).Phase != previewset.PhaseLive {
		return false, nil
	}
	compared := PushEvent{Before: set.CommitSHA, After: ev.PullRequest.HeadSHA, Repository: ev.Repository}
	files, mode := s.lookupChangedFiles(ctx, compared, installationID)
	if mode != filterModePaths || len(files) == 0 || len(filterIgnoredGitHubPaths(files, policy)) > 0 {
		return false, nil
	}
	for _, id := range set.MemberAppIDs {
		if _, err := s.Reconcile.Store.RefreshPRPreview(ctx, id, expiresAt); err != nil && !errors.Is(err, state.ErrNotFound) {
			return false, fmt.Errorf("githubd: renew PR preview lease: %w", err)
		}
	}
	summary := fmt.Sprintf("Preview unchanged: only ignored paths changed since %s, so the preview keeps serving that commit.", shortSHA(set.CommitSHA))
	if err := s.writePreviewCheck(ctx, installationID, ev.Repository.FullName, ev.PullRequest.HeadSHA,
		githubdgrpc.CheckPhaseLive, previewURL, summary); err != nil {
		s.Log.Warn("githubd: write unchanged preview check", "err", err,
			"repo", ev.Repository.FullName, "sha", ev.PullRequest.HeadSHA)
	}
	s.Log.Info("githubd pull_request: ignored paths only; preview kept", "repo", ev.Repository.FullName,
		"pr_number", ev.Number, "serving", set.CommitSHA, "head", ev.PullRequest.HeadSHA)
	return true, nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
