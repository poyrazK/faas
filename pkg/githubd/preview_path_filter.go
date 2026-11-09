// adr: 20261108150000
package githubd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewKeep is a preview service a PR push does not rebuild: its preview
// app and the commit its live deployment was built from.
type previewKeep struct {
	appID  string
	commit string
}

// previewUnchangedServices picks the preview services a PR push can leave on
// the build they serve (ADR-20261108150000), keyed by lower-cased workload
// name. A service is kept when the push's changed files since the previous
// head lie outside its directory, by the production push's path filter
// (shared files rebuild everything), and it has a live preview deployment at
// the commit the previous head expected of it. Anything uncertain rebuilds:
// an opened or reopened PR, a previous head that failed setup, a compare
// GitHub cannot answer exactly, or a store without the preview set.
func (s *Service) previewUnchangedServices(ctx context.Context, ev PullRequestEvent, installationID int64,
	policy state.GitHubDeployPolicy, previewed []state.App, workloads map[string]reposcan.Workload) map[string]previewKeep {
	if ev.Action != PullRequestActionSynchronize || len(previewed) < 2 {
		return nil
	}
	sets, ok := s.Reconcile.Store.(state.PRPreviewSetStore)
	reader, readable := s.Reconcile.Store.(state.PRPreviewEnvironmentReader)
	if _, pinnable := s.Reconcile.Store.(state.PRPreviewMemberPinStore); !ok || !readable || !pinnable {
		return nil
	}
	previous, err := sets.GetPRPreviewSet(ctx, installationID, ev.Repository.FullName, ev.Number)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			s.Log.Warn("githubd: load PR preview set for path filter", "err", err, "pr", ev.Number)
		}
		return nil
	}
	if previous.Closed || previous.SetupError != "" || previous.CommitSHA == ev.PullRequest.HeadSHA {
		return nil
	}
	environment, err := reader.PRPreviewEnvironmentByRoot(ctx, previous.RootAppID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			s.Log.Warn("githubd: load PR preview environment for path filter", "err", err, "pr", ev.Number)
		}
		return nil
	}
	live := make(map[string]previewKeep, len(environment.Members))
	for _, member := range environment.Members {
		if member.DeploymentStatus != previewset.PhaseLive || member.PreviewState != state.PreviewPrStateOpen ||
			(member.AppStatus != string(state.AppActive) && member.AppStatus != string(state.AppEvictedCold)) {
			continue
		}
		live[strings.ToLower(member.WorkloadName)] = previewKeep{appID: member.AppID, commit: previous.MemberCommit(member.AppID)}
	}
	if len(live) == 0 {
		return nil
	}
	compared := PushEvent{Before: previous.CommitSHA, After: ev.PullRequest.HeadSHA, Repository: ev.Repository}
	files, mode := s.lookupChangedFiles(ctx, compared, installationID)
	if mode != filterModePaths || len(files) == 0 {
		return nil
	}
	if files = filterIgnoredGitHubPaths(files, policy); len(files) == 0 {
		return nil
	}
	// Filter by what each service builds from at the new head: its scanned
	// directory, or the app's for an app bound without a project. A service
	// the PR adds has no production app id, so candidates are keyed by name.
	candidates := make([]state.App, 0, len(previewed))
	for i, app := range previewed {
		workload := strings.ToLower(app.WorkloadName)
		rootDir := app.RootDir
		if scanned, ok := workloads[workload]; ok {
			rootDir = scanned.RootDir
		}
		id := workload
		if id == "" {
			id = fmt.Sprintf("\x00%d", i) // never kept
		}
		candidates = append(candidates, applyGitHubRootPolicy(state.App{ID: id, WorkloadName: app.WorkloadName, RootDir: rootDir}, policy))
	}
	_, skipped := s.filterByPath(candidates, files, mode)
	keep := map[string]previewKeep{}
	for _, workload := range skipped {
		if kept, ok := live[workload]; ok {
			keep[workload] = kept
		}
	}
	if len(keep) == 0 {
		return nil
	}
	return keep
}

// pinUnchangedPreviews records the kept services on the reserved set and
// returns the preview app ids it pinned. reserved are the set's preview apps
// as reserved for this head; a kept service whose preview app changed
// rebuilds. A failed pin rebuilds everything.
func (s *Service) pinUnchangedPreviews(ctx context.Context, head state.PRPreviewHead, keep map[string]previewKeep, reserved []state.App) map[string]bool {
	if len(keep) == 0 {
		return nil
	}
	pins := map[string]string{}
	for _, app := range reserved {
		if kept, ok := keep[strings.ToLower(app.WorkloadName)]; ok && kept.appID == app.ID && kept.commit != head.CommitSHA {
			pins[app.ID] = kept.commit
		}
	}
	if len(pins) == 0 || len(pins) == len(reserved) {
		return nil
	}
	store, ok := s.Reconcile.Store.(state.PRPreviewMemberPinStore)
	if !ok {
		return nil
	}
	if err := store.PinPRPreviewMembers(ctx, head, pins); err != nil {
		s.Log.Warn("githubd: pin unchanged PR preview services; rebuilding all", "err", err,
			"repo", head.RepoFullName, "pr", head.PRNumber)
		return nil
	}
	pinned := make(map[string]bool, len(pins))
	for id := range pins {
		pinned[id] = true
	}
	return pinned
}
