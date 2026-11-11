package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// keyProjects resolves the project slugs a key request limits the key to
// (ADR-20261104085712): only agent keys take a limit, a limited key cannot
// manage databases (they belong to the organization, not to a project), and
// every project must belong to the key's organization. It returns nil for a
// request that names no project.
func (s *server) keyProjects(ctx context.Context, acct state.Account, orgID string, scopes, slugs []string) ([]state.Project, *api.Problem) {
	if len(slugs) == 0 {
		return nil, nil
	}
	if _, ok := s.store.(state.KeyProjectLimitStore); !ok {
		return nil, api.ErrCapacity("project limits are unavailable")
	}
	if !slices.Contains(scopes, api.ScopeAgent) {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Projects need the agent scope",
			"only agent keys can be limited to projects; add the agent scope")
	}
	if slices.Contains(scopes, api.ScopeManagedPostgresManage) || slices.Contains(scopes, api.ScopeManagedPostgresRead) {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Databases are not in a project",
			"a key limited to projects cannot hold postgres:manage or postgres:read; databases belong to the organization")
	}
	slugs = slices.Compact(slices.Sorted(slices.Values(slugs)))
	if len(slugs) > state.MaxKeyProjects {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Too many projects",
			fmt.Sprintf("a key can be limited to at most %d projects; got %d", state.MaxKeyProjects, len(slugs)))
	}
	projects := make([]state.Project, 0, len(slugs))
	owners, _ := s.store.(state.ProjectOwnershipStore)
	for _, slug := range slugs {
		project, err := s.keyProjectBySlug(ctx, owners, acct, orgID, slug)
		if errors.Is(err, state.ErrNotFound) || err == nil && orgID != "" && project.OrgID != "" && project.OrgID != orgID {
			return nil, projectNotFound(slug)
		}
		if err != nil {
			return nil, api.ErrCapacity("could not load project")
		}
		projects = append(projects, project)
	}
	return projects, nil
}

// keyProjectBySlug finds a project a key names: an organization's key looks
// in that organization first, where a company project's slug is unique, then
// among the creator's own projects.
func (s *server) keyProjectBySlug(ctx context.Context, owners state.ProjectOwnershipStore, acct state.Account, orgID, slug string) (state.Project, error) {
	if orgID != "" && owners != nil {
		project, err := owners.ProjectByOrgSlug(ctx, orgID, slug)
		if !errors.Is(err, state.ErrNotFound) {
			return project, err
		}
	}
	return s.store.ProjectBySlug(ctx, acct.ID, slug)
}

// limitKeyToProjects records a new key's project limit. When it cannot, it
// revokes the key, so a key meant to be limited is never left unlimited.
func (s *server) limitKeyToProjects(ctx context.Context, acct state.Account, key state.APIKey, projects []state.Project) *api.Problem {
	if len(projects) == 0 {
		return nil
	}
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	limits, _ := s.store.(state.KeyProjectLimitStore)
	if limits != nil && limits.SetAPIKeyProjectLimit(ctx, key.ID, ids) == nil {
		return nil
	}
	if key.OrgID != "" {
		_, _ = s.store.RevokeOrgAPIKey(ctx, key.OrgID, key.ID)
	} else {
		_ = s.store.DeleteAPIKey(ctx, acct.ID, key.ID)
	}
	return api.ErrCapacity("could not limit the key to its projects; it was revoked, try again")
}

// projectSlugsByID names a limit's projects; a deleted project, still in the
// limit but matching nothing, is left out.
func (s *server) projectSlugsByID(ctx context.Context, ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if project, err := s.store.ProjectByID(ctx, id); err == nil {
			out = append(out, project.Slug)
		}
	}
	return out
}

func projectSlugs(projects []state.Project) []string {
	out := make([]string, 0, len(projects))
	for _, project := range projects {
		out = append(out, project.Slug)
	}
	return out
}
