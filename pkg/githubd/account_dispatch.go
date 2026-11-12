package githubd

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/githubscope"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/state"
)

// InstallationBindingsLister enumerates deployment targets across accounts.
// Production implements it; older embedded services retain their single-target
// lookup. Every target is still checked against its own durable installation.
type InstallationBindingsLister interface {
	ListBindingsForInstallation(context.Context, int64) ([]state.GitHubBinding, error)
}

func (s *Service) dispatchRepositoryAccounts(ctx context.Context, installationID int64, repo string, handle func(context.Context, *Service) (reconcile.Result, error)) (reconcile.Result, error) {
	lister, ok := s.Bindings.(InstallationBindingsLister)
	if !ok {
		return handle(ctx, s)
	}
	bindings, err := lister.ListBindingsForInstallation(ctx, installationID)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: list installation targets: %w", err)
	}
	byAccount := make(map[string][]state.GitHubBinding)
	for _, b := range bindings {
		if b.AccountID != "" && b.InstallID == installationID && strings.EqualFold(b.RepoFullName, repo) {
			byAccount[b.AccountID] = append(byAccount[b.AccountID], b)
		}
	}
	accounts := make([]string, 0, len(byAccount))
	for accountID := range byAccount {
		accounts = append(accounts, accountID)
	}
	sort.Strings(accounts)
	result := reconcile.Result{WasIgnored: true}
	var failures []error
	handled := false
	for _, accountID := range accounts {
		// Service contains dependencies, not locks or mutable per-request state.
		// Scope a copy so concurrent deliveries cannot change each other's account.
		scoped := *s
		scoped.eventAccountID = accountID
		scoped.Bindings = accountEventBindings{bindings: byAccount[accountID], source: s.Bindings, accountID: accountID}
		next, err := handle(githubscope.WithAccount(ctx, accountID), &scoped)
		if s.DeliveryTargets != nil && webhookDeliveryID(ctx) != "" {
			skipped := IsIgnored(err) || IsNoBinding(err) || errors.Is(err, ErrSkipDeploy) || isReleaseTagRejected(err)
			if recordErr := s.DeliveryTargets.RecordDeliveryTarget(ctx, webhookDeliveryID(ctx), accountID, err == nil || skipped, SkipReason(err)); recordErr != nil {
				failures = append(failures, recordErr)
			}
		}
		if err != nil && !IsIgnored(err) && !IsNoBinding(err) && !errors.Is(err, ErrSkipDeploy) && !isReleaseTagRejected(err) {
			failures = append(failures, fmt.Errorf("githubd: dispatch repository target: %w", err))
			continue
		}
		if err != nil {
			continue
		}
		handled = true
		result.Added = append(result.Added, next.Added...)
		result.Changed = append(result.Changed, next.Changed...)
		result.Removed = append(result.Removed, next.Removed...)
		result.BuildIDs = append(result.BuildIDs, next.BuildIDs...)
		result.Alerts = append(result.Alerts, next.Alerts...)
		result.WasIgnored = result.WasIgnored && next.WasIgnored
	}
	if len(failures) > 0 {
		return result, errors.Join(failures...)
	}
	if !handled {
		return result, ErrIgnored
	}
	return result, nil
}

type accountEventBindings struct {
	bindings  []state.GitHubBinding
	source    AppBindingStore
	accountID string
}

func (b accountEventBindings) GetAppBinding(ctx context.Context, repo, branch string) (state.GitHubBinding, error) {
	bindings, err := b.eventBindings(ctx)
	if err != nil {
		return state.GitHubBinding{}, err
	}
	if branch == "" {
		branch = defaultProductionBranch
	}
	for _, binding := range bindings {
		if strings.EqualFold(binding.RepoFullName, repo) && bindingBranch(binding) == branch {
			return binding, nil
		}
	}
	return state.GitHubBinding{}, state.ErrNotFound
}

// Keep the initial live-app target set, but re-read each binding before use.
// A retained binding on a deleted app must not displace an active PR parent.
func (b accountEventBindings) eventBindings(ctx context.Context) ([]state.GitHubBinding, error) {
	live, err := b.ListForAccount(ctx, b.accountID)
	if err != nil {
		return nil, err
	}
	var bindings []state.GitHubBinding
	for _, initial := range b.bindings {
		key := initial.AppID
		if key == "" {
			key = initial.BindingID
		}
		if current, ok := live[key]; ok && current.AccountID == b.accountID {
			bindings = append(bindings, current)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].AppID < bindings[j].AppID })
	return bindings, nil
}

func (b accountEventBindings) ListForAccount(ctx context.Context, accountID string) (map[string]state.GitHubBinding, error) {
	if accountID != b.accountID {
		return nil, state.ErrNotFound
	}
	// Keep live account lookups for PR rechecks and every app of a monorepo.
	// The initial enumeration chooses accounts, never substitutes for revocation.
	if lister, ok := b.source.(AccountAppBindingsStore); ok {
		return lister.ListForAccount(ctx, accountID)
	}
	bindings := make(map[string]state.GitHubBinding, len(b.bindings))
	for _, binding := range b.bindings {
		key := binding.AppID
		if key == "" {
			key = binding.BindingID
		}
		bindings[key] = binding
	}
	return bindings, nil
}

func (b accountEventBindings) ListForInstallationRepo(ctx context.Context, installationID int64, repo string) ([]state.GitHubBinding, error) {
	all, err := b.eventBindings(ctx)
	if err != nil {
		return nil, err
	}
	var bindings []state.GitHubBinding
	for _, binding := range all {
		if binding.InstallID == installationID && strings.EqualFold(binding.RepoFullName, repo) {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

func (b accountEventBindings) GetAppBindingForInstallation(ctx context.Context, repo, branch string, installationID int64) (state.GitHubBinding, error) {
	binding, err := b.GetAppBinding(ctx, repo, branch)
	if err == nil && binding.InstallID != installationID {
		return state.GitHubBinding{}, state.ErrNotFound
	}
	return binding, err
}
