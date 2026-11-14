// adr: 20261113121342
package githubd

import (
	"context"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// branchPreviewUnchanged picks the apps a pull request push can leave on the
// build they serve in the pull request's environment branch
// (ADR-20261113121342 slice 2), by app id. An app is kept when its live
// deployment in the branch is a build of this pull request at an earlier
// commit, and the files changed since that commit lie outside its directory
// by the production push's path filter (shared files rebuild everything).
// Anything uncertain rebuilds: an opened or reopened pull request, a branch
// deployment copied from production, a compare GitHub cannot answer exactly.
// Keeping every app rebuilds them all, so the head still gets a deployment
// to report on.
func (s *Service) branchPreviewUnchanged(ctx context.Context, ev PullRequestEvent, installationID int64, environment string,
	policy state.GitHubDeployPolicy, apps []state.App, workloads map[string]reposcan.Workload) map[string]bool {
	if ev.Action != PullRequestActionSynchronize || len(apps) < 2 || s.Reconcile.Store == nil {
		return nil
	}
	head := ev.PullRequest.HeadSHA
	byCommit := map[string][]string{}
	for _, app := range apps {
		live, err := s.Reconcile.Store.LiveDeploymentForScope(ctx, app.ID, environment)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				s.Log.Warn("githubd: branch preview live deployment", "err", err, "app_id", app.ID, "environment", environment)
			}
			continue
		}
		if live.Kind != state.DeploymentKindPreview || live.PRNumber != ev.Number || live.CommitSHA == "" || live.CommitSHA == head {
			continue
		}
		byCommit[live.CommitSHA] = append(byCommit[live.CommitSHA], app.ID)
	}
	// Filter by what each app builds from at the new head: its scanned
	// directory, or the app's own.
	candidates := make([]state.App, 0, len(apps))
	for _, app := range apps {
		rootDir := app.RootDir
		if scanned, ok := workloads[strings.ToLower(app.WorkloadName)]; ok {
			rootDir = scanned.RootDir
		}
		candidates = append(candidates, applyGitHubRootPolicy(state.App{ID: app.ID, WorkloadName: app.WorkloadName, RootDir: rootDir}, policy))
	}
	keep := map[string]bool{}
	for commit, ids := range byCommit {
		files, mode := s.lookupChangedFiles(ctx, PushEvent{Before: commit, After: head, Repository: ev.Repository}, installationID)
		if mode != filterModePaths || len(files) == 0 {
			continue
		}
		if files = filterIgnoredGitHubPaths(files, policy); len(files) == 0 {
			continue
		}
		_, skipped := s.filterByPath(candidates, files, mode)
		unchanged := make(map[string]bool, len(skipped))
		for _, id := range skipped {
			unchanged[id] = true
		}
		for _, id := range ids {
			if unchanged[id] {
				keep[id] = true
			}
		}
	}
	if len(keep) == 0 || len(keep) == len(apps) {
		return nil
	}
	return keep
}
