package githubd

// Push-to-deploy for apps bound to a repository without a project
// (ADR-20261101160300). The dashboard wizard, `gregale github bind` and the
// bind APIs write the binding on the app row only. A push used to need a
// project bound to the same repository, so such an app was never deployed and
// its connection still read healthy.

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

// appsBoundForPush returns the live apps bound to binding's repository and
// production branch under installationID. Apps whose production deploys
// belong to GitHub Actions are left out; actionsOnly reports that every
// bound app was left out for that reason.
func (s *Service) appsBoundForPush(ctx context.Context, binding state.GitHubBinding, installationID int64) (apps []state.App, actionsOnly bool, err error) {
	bindings := []state.GitHubBinding{binding}
	if lister, ok := s.Bindings.(AccountAppBindingsStore); ok {
		all, err := lister.ListForAccount(ctx, binding.AccountID)
		if err != nil {
			return nil, false, fmt.Errorf("githubd: list app bindings: %w", err)
		}
		for appID, candidate := range all {
			if appID != binding.AppID && candidate.InstallID == installationID &&
				strings.EqualFold(candidate.RepoFullName, binding.RepoFullName) &&
				candidate.ProductionBranch == binding.ProductionBranch {
				bindings = append(bindings, candidate)
			}
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].AppID < bindings[j].AppID })
	actions := 0
	for _, candidate := range bindings {
		app, err := s.Reconcile.Store.AppByID(ctx, candidate.AppID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("githubd: load bound app %s: %w", candidate.AppID, err)
		}
		if app.AccountID != binding.AccountID || app.Status == state.AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		trigger, owned, err := s.boundAppProductionTrigger(ctx, app, candidate)
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
		apps = append(apps, app)
	}
	return apps, len(apps) == 0 && actions > 0, nil
}

// boundAppProductionTrigger returns the production trigger of an app bound
// at the app level. A project-backed app follows its project's policy; owned
// is false when that project is bound to another repository, whose pushes
// own the app's source.
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
