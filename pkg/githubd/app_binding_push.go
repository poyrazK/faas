package githubd

// Push-to-deploy for apps bound to a repository at the app level
// (ADR-20261101160300). The dashboard wizard, `gregale github bind` and the
// bind APIs write the binding on the app row only. A push used to need a
// project bound to the same repository, so such an app was never deployed and
// its connection still read healthy. App-bound apps now deploy on their own
// and beside a project bound to the same repository.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// AccountAppBindingsStore lists an account's app bindings keyed by app ID.
// The app-bound push path uses it to deploy every app bound to the pushed
// repository and branch, not only the first one the dispatch lookup returns.
type AccountAppBindingsStore interface {
	ListForAccount(ctx context.Context, accountID string) (map[string]state.GitHubBinding, error)
}

// InstallationRepoBindingsStore lists the live app bindings of one
// repository under one installation, whatever their production branch. A
// release tag uses it to find the apps bound to a branch other than the
// repository's default one.
type InstallationRepoBindingsStore interface {
	ListForInstallationRepo(ctx context.Context, installationID int64, repoFullName string) ([]state.GitHubBinding, error)
}

// boundApp is a live app bound to a repository at the app level, with the
// binding that bound it.
type boundApp struct {
	app     state.App
	binding state.GitHubBinding
}

// bindingBranch is a binding's production branch; the stores read an empty
// one as "main".
func bindingBranch(binding state.GitHubBinding) string {
	if binding.ProductionBranch == "" {
		return defaultProductionBranch
	}
	return binding.ProductionBranch
}

// accountRepoBindings returns accountID's bindings of repo under
// installationID, sorted by app ID. first is included when the account
// listing is unavailable.
func (s *Service) accountRepoBindings(ctx context.Context, accountID string, installationID int64, repo string, first state.GitHubBinding) ([]state.GitHubBinding, error) {
	var bindings []state.GitHubBinding
	lister, ok := s.Bindings.(AccountAppBindingsStore)
	if !ok {
		if first.AppID != "" {
			bindings = append(bindings, first)
		}
		return bindings, nil
	}
	all, err := lister.ListForAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("githubd: list app bindings: %w", err)
	}
	for appID, candidate := range all {
		if candidate.InstallID == installationID && strings.EqualFold(candidate.RepoFullName, repo) {
			candidate.AppID = appID
			bindings = append(bindings, candidate)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].AppID < bindings[j].AppID })
	return bindings, nil
}

// appsBoundToBranch returns the live apps of binding's account bound at the
// app level to repo's branch under installationID, in app ID order. Members
// of projectID, the project bound to the repository, are left out: its
// reconcile owns them. So are apps whose project is bound to another
// repository, and PR previews.
func (s *Service) appsBoundToBranch(ctx context.Context, binding state.GitHubBinding, installationID int64, repo, branch, projectID string) ([]boundApp, error) {
	bindings, err := s.accountRepoBindings(ctx, binding.AccountID, installationID, repo, binding)
	if err != nil {
		return nil, err
	}
	var out []boundApp
	for _, candidate := range bindings {
		if candidate.AppID == "" || bindingBranch(candidate) != branch {
			continue
		}
		app, err := s.Reconcile.Store.AppByID(ctx, candidate.AppID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("githubd: load bound app %s: %w", candidate.AppID, err)
		}
		if app.AccountID != binding.AccountID || app.Status == state.AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		if projectID != "" && app.ProjectID == projectID {
			continue
		}
		out = append(out, boundApp{app: app, binding: candidate})
	}
	return out, nil
}

// appsBoundForPush returns the apps a push to repo's branch deploys at the
// app level. Apps whose production deploys belong to GitHub Actions are left
// out; actionsOnly reports that every bound app was left out for that reason.
func (s *Service) appsBoundForPush(ctx context.Context, binding state.GitHubBinding, installationID int64, repo, branch, projectID string) (apps []state.App, actionsOnly bool, err error) {
	bound, err := s.appsBoundToBranch(ctx, binding, installationID, repo, branch, projectID)
	if err != nil {
		return nil, false, err
	}
	actions := 0
	for _, candidate := range bound {
		trigger, owned, err := s.boundAppProductionTrigger(ctx, candidate.app, candidate.binding)
		if err != nil {
			return nil, false, err
		}
		if !owned {
			continue
		}
		if trigger == state.ProductionTriggerActions {
			actions++
			continue
		}
		apps = append(apps, candidate.app)
	}
	return apps, len(apps) == 0 && actions > 0, nil
}

// boundAppProductionTrigger returns the production trigger of an app bound
// at the app level. An app in a project without a repository follows its
// project's policy; owned is false when that project is bound to another
// repository, whose pushes own the app's source.
func (s *Service) boundAppProductionTrigger(ctx context.Context, app state.App, binding state.GitHubBinding) (trigger state.ProductionTrigger, owned bool, err error) {
	if app.ProjectID == "" {
		return binding.ProductionTrigger, true, nil
	}
	project, err := s.Reconcile.Store.ProjectByID(ctx, app.ProjectID)
	if errors.Is(err, state.ErrNotFound) {
		return binding.ProductionTrigger, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("githubd: load project of bound app %s: %w", app.ID, err)
	}
	if project.RepoFullName != "" && !strings.EqualFold(project.RepoFullName, binding.RepoFullName) {
		return "", false, nil
	}
	policy, err := s.githubDeployPolicy(ctx, project)
	if err != nil {
		return "", false, fmt.Errorf("githubd: resolve GitHub deployment policy of bound app %s: %w", app.ID, err)
	}
	return policy.ProductionTrigger, true, nil
}

// tagBindingForRepository finds a binding of repo under installationID for a
// release tag whose default-branch lookup found none: an app bound without a
// project to another branch.
func (s *Service) tagBindingForRepository(ctx context.Context, installationID int64, repo string) (state.GitHubBinding, bool, error) {
	lister, ok := s.Bindings.(InstallationRepoBindingsStore)
	if !ok || installationID <= 0 {
		return state.GitHubBinding{}, false, nil
	}
	bindings, err := lister.ListForInstallationRepo(ctx, installationID, repo)
	if err != nil {
		return state.GitHubBinding{}, false, fmt.Errorf("githubd: list repository bindings for tag: %w", err)
	}
	var found state.GitHubBinding
	for _, candidate := range bindings {
		if candidate.AppID != "" && candidate.BindingID != "" && (found.AppID == "" || candidate.AppID < found.AppID) {
			found = candidate
		}
	}
	return found, found.AppID != "", nil
}

// taggedBoundBranch picks the branch a release tag deploys when no project is
// bound to the repository: the production branch of the account's bound apps
// whose head is the tagged commit, the repository's default branch first. A
// tag elsewhere deploys nothing; ok is false.
func (s *Service) taggedBoundBranch(ctx context.Context, binding state.GitHubBinding, installationID int64, ev PushEvent) (branch string, ok bool, err error) {
	bindings, err := s.accountRepoBindings(ctx, binding.AccountID, installationID, ev.Repository.FullName, binding)
	if err != nil {
		return "", false, err
	}
	seen := make(map[string]bool, len(bindings))
	var branches []string
	for _, candidate := range bindings {
		if name := bindingBranch(candidate); !seen[name] {
			seen[name] = true
			branches = append(branches, name)
		}
	}
	defaultBranch := ev.Repository.DefaultBranch
	sort.SliceStable(branches, func(i, j int) bool {
		if (branches[i] == defaultBranch) != (branches[j] == defaultBranch) {
			return branches[i] == defaultBranch
		}
		return branches[i] < branches[j]
	})
	for _, candidate := range branches {
		if s.BranchHeads == nil {
			return candidate, true, nil
		}
		head, err := s.BranchHeads.BranchHead(ctx, installationID, ev.Repository.FullName, candidate)
		if err != nil {
			return "", false, fmt.Errorf("githubd: read head of %s for release tag: %w", candidate, err)
		}
		if strings.EqualFold(head, ev.After) {
			return candidate, true, nil
		}
	}
	s.Log.Info("githubd: release tag is not the head of a bound production branch",
		"repo", ev.Repository.FullName, "sha", ev.After, "branches", len(branches))
	return "", false, nil
}
