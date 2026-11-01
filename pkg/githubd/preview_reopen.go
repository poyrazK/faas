package githubd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/state"
)

// reopenLivePullRequestPreview reopens a PR's preview in place when the PR
// is reopened within its close grace at the head the preview still serves.
// Rebuilding every workload for an unchanged commit spent builder time and
// the hourly preview deploy budget, and showed the PR a build where the
// preview was already live. Anything else (a new head, a set that is not
// live, the allowance full) returns false and takes the normal path, which
// reserves, builds and reports.
func (s *Service) reopenLivePullRequestPreview(ctx context.Context, ev PullRequestEvent, installationID int64,
	limits api.Limits, previewURL string, expiresAt time.Time) (bool, error) {
	if ev.Action != PullRequestActionReopened {
		return false, nil
	}
	sets, ok := s.Reconcile.Store.(state.PRPreviewSetStore)
	reader, readable := s.Reconcile.Store.(state.PRPreviewEnvironmentReader)
	reopener, reopenable := s.Reconcile.Store.(state.PRPreviewReopenStore)
	if !ok || !readable || !reopenable {
		return false, nil
	}
	set, err := sets.GetPRPreviewSet(ctx, installationID, ev.Repository.FullName, ev.Number)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("githubd: load PR preview set: %w", err)
	}
	if !set.Closed || set.CommitSHA != ev.PullRequest.HeadSHA {
		return false, nil
	}
	environment, err := reader.PRPreviewEnvironmentByRoot(ctx, set.RootAppID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("githubd: load PR preview environment: %w", err)
	}
	members := make([]previewset.Member, len(environment.Members))
	for i, member := range environment.Members {
		if member.PreviewState == state.PreviewPrStateClosed {
			member.PreviewState = state.PreviewPrStateOpen // still serving in its grace
		}
		members[i] = member
	}
	if previewset.EvaluateSet(set.PRNumber, set.RootAppID, members, environment.Set.SetupError).Phase != previewset.PhaseLive {
		return false, nil
	}
	// The root first: it takes the environment's allowance, which its
	// siblings then share.
	ids := append([]string{set.RootAppID}, set.MemberAppIDs...)
	reopened := make(map[string]bool, len(ids))
	for _, id := range ids {
		if reopened[id] {
			continue
		}
		if _, err := reopener.ReopenPRPreview(ctx, id, expiresAt, limits); err != nil {
			var quota *state.QuotaError
			if errors.As(err, &quota) || errors.Is(err, state.ErrNotFound) {
				return false, nil // the normal path reports or rebuilds
			}
			return false, fmt.Errorf("githubd: reopen PR preview: %w", err)
		}
		reopened[id] = true
	}
	set.Closed = false
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		return false, fmt.Errorf("githubd: reopen PR preview set: %w", err)
	}
	summary := fmt.Sprintf("Preview reopened: it is still serving %s, so nothing was rebuilt.", shortSHA(set.CommitSHA))
	if err := s.writePreviewCheck(ctx, installationID, ev.Repository.FullName, ev.PullRequest.HeadSHA,
		githubdgrpc.CheckPhaseLive, previewURL, summary); err != nil {
		s.Log.Warn("githubd: write reopened preview check", "err", err,
			"repo", ev.Repository.FullName, "sha", ev.PullRequest.HeadSHA)
	}
	if rootSlug := environmentRootSlug(environment, set.RootAppID); rootSlug != "" {
		marker := previewCommentMarker(rootSlug)
		body := previewCommentBody(marker, rootSlug, "https://"+previewHostnameForSlug(rootSlug), set.CommitSHA,
			"live", "The pull request was reopened. The preview is serving this commit again.")
		if err := s.writePreviewComment(ctx, installationID, ev.Repository.FullName, ev.Number, marker, body); err != nil {
			s.Log.Warn("githubd: upsert reopened preview comment", "err", err,
				"repo", ev.Repository.FullName, "pr", ev.Number)
		}
	}
	s.Log.Info("githubd pull_request: reopened live preview in place", "repo", ev.Repository.FullName,
		"pr_number", ev.Number, "serving", set.CommitSHA)
	return true, nil
}

// keepLivePreviewOnReady answers ready_for_review for a PR whose preview
// already serves the head: with draft previews on, the draft was previewed,
// and rebuilding every workload for the same commit spent builder time and
// the hourly preview deploy budget (ADR-20261101100000). The leases are
// renewed and the check says the preview is live. Anything else (a new head,
// a set that is not live) returns false and takes the normal path.
func (s *Service) keepLivePreviewOnReady(ctx context.Context, ev PullRequestEvent, installationID int64,
	previewURL string, expiresAt time.Time) (bool, error) {
	if ev.Action != PullRequestActionReadyForReview {
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
	if set.Closed || set.CommitSHA != ev.PullRequest.HeadSHA {
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
	for _, id := range set.MemberAppIDs {
		if _, err := s.Reconcile.Store.RefreshPRPreview(ctx, id, expiresAt); err != nil && !errors.Is(err, state.ErrNotFound) {
			return false, fmt.Errorf("githubd: renew PR preview lease: %w", err)
		}
	}
	summary := fmt.Sprintf("Ready for review: the preview is already serving %s, so nothing was rebuilt.", shortSHA(set.CommitSHA))
	if err := s.writePreviewCheck(ctx, installationID, ev.Repository.FullName, ev.PullRequest.HeadSHA,
		githubdgrpc.CheckPhaseLive, previewURL, summary); err != nil {
		s.Log.Warn("githubd: write ready preview check", "err", err,
			"repo", ev.Repository.FullName, "sha", ev.PullRequest.HeadSHA)
	}
	s.Log.Info("githubd pull_request: ready for review; live preview kept", "repo", ev.Repository.FullName,
		"pr_number", ev.Number, "serving", set.CommitSHA)
	return true, nil
}

func environmentRootSlug(environment state.PRPreviewEnvironment, rootAppID string) string {
	for _, member := range environment.Members {
		if member.AppID == rootAppID {
			return member.Slug
		}
	}
	return ""
}
