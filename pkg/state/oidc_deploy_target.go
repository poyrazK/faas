package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// OIDCDeployTarget is what a GitHub Actions identity may deploy under a trust
// policy Gregale created (ADR-20261101100000): the app it names, when that app
// is bound to the identity's repository, and the branches Gregale deploys from
// that repository (the production branches of its bindings and projects, and
// the projects' deploy branches).
type OIDCDeployTarget struct {
	AppID    string
	Branches []string
}

// OIDCDeployTargetResolver resolves an OIDC subject's deploy target. It
// returns ErrNotFound when the subject is not a GitHub Actions repository
// subject or the named app is not bound to its repository.
type OIDCDeployTargetResolver interface {
	OIDCDeployTargetForSubject(ctx context.Context, accountID, issuerURL, subject, appSlug string) (OIDCDeployTarget, error)
}

var (
	_ OIDCDeployTargetResolver = (*PgStore)(nil)
	_ OIDCDeployTargetResolver = (*MemStore)(nil)
)

func (s *PgStore) OIDCDeployTargetForSubject(ctx context.Context, accountID, issuerURL, subject, appSlug string) (OIDCDeployTarget, error) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(issuerURL, subject)
	if !ok || strings.TrimSpace(appSlug) == "" {
		return OIDCDeployTarget{}, ErrNotFound
	}
	var target OIDCDeployTarget
	err := s.pool.QueryRow(ctx, `
		select a.id::text,
		       array(
		         select distinct branch from (
		           select bound.github_production_branch as branch
		             from apps bound
		            where bound.account_id = $1::uuid
		              and bound.deleted_at is null
		              and lower(bound.github_repo_full_name) = lower($2)
		           union all
		           select project.production_branch
		             from projects project
		            where project.account_id = $1::uuid
		              and lower(project.repo_full_name) = lower($2)
		           union all
		           select deploy.branch
		             from github_deploy_branches deploy
		             join projects project on project.id = deploy.project_id
		            where project.account_id = $1::uuid
		              and lower(project.repo_full_name) = lower($2)
		         ) refs
		         where coalesce(branch, '') <> ''
		         order by branch)
		  from apps a
		  left join projects p on p.id = a.project_id
		 where a.account_id = $1::uuid
		   and a.slug = $3
		   and a.status <> 'deleted'
		   and a.deleted_at is null
		   and (lower(a.github_repo_full_name) = lower($2) or lower(p.repo_full_name) = lower($2))
		 limit 1`,
		accountID, identity.FullName, appSlug).Scan(&target.AppID, &target.Branches)
	if errors.Is(err, pgx.ErrNoRows) {
		return OIDCDeployTarget{}, ErrNotFound
	}
	if err != nil {
		return OIDCDeployTarget{}, fmt.Errorf("state: resolve OIDC deploy target: %w", err)
	}
	return target, nil
}

func (m *MemStore) OIDCDeployTargetForSubject(_ context.Context, accountID, issuerURL, subject, appSlug string) (OIDCDeployTarget, error) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(issuerURL, subject)
	if !ok || strings.TrimSpace(appSlug) == "" {
		return OIDCDeployTarget{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	repo := identity.FullName
	branches := map[string]struct{}{}
	add := func(branch string) {
		if branch != "" {
			branches[branch] = struct{}{}
		}
	}
	for appID, binding := range m.githubBindings {
		app, found := m.apps[appID]
		if found && app.AccountID == accountID && app.Status != AppDeleted && strings.EqualFold(binding.RepoFullName, repo) {
			add(binding.ProductionBranch)
		}
	}
	for id, project := range m.projects {
		if project.AccountID != accountID || !strings.EqualFold(project.RepoFullName, repo) {
			continue
		}
		add(project.ProductionBranch)
		for branch := range m.githubDeployBranches[id] {
			add(branch)
		}
	}
	for id, app := range m.apps {
		if app.AccountID != accountID || app.Slug != appSlug || app.Status == AppDeleted {
			continue
		}
		bound := strings.EqualFold(m.githubBindings[id].RepoFullName, repo)
		if project, ok := m.projects[app.ProjectID]; ok && strings.EqualFold(project.RepoFullName, repo) {
			bound = true
		}
		if !bound {
			return OIDCDeployTarget{}, ErrNotFound
		}
		target := OIDCDeployTarget{AppID: id}
		for branch := range branches {
			target.Branches = append(target.Branches, branch)
		}
		sort.Strings(target.Branches)
		return target, nil
	}
	return OIDCDeployTarget{}, ErrNotFound
}
