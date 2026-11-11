package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Organization-owned GitHub installations (ADR-20261110200000): an adopted
// organization's resource namespace owns the installation, an owner or admin
// connects it, and the company's projects bind only through it.

const githubOrgInstallDocs = "https://gregale.dev/docs/orgs"

// githubConnectOrgKey carries the organization a Connect GitHub flow targets
// while apid seals its OAuth state.
type githubConnectOrgKey struct{}

func withGitHubConnectOrg(ctx context.Context, orgID string) context.Context {
	if orgID == "" {
		return ctx
	}
	return context.WithValue(ctx, githubConnectOrgKey{}, orgID)
}

func githubConnectOrgFrom(ctx context.Context) string {
	orgID, _ := ctx.Value(githubConnectOrgKey{}).(string)
	return orgID
}

// githubInstallOwner is the namespace that owns an organization's GitHub
// installations: the adopted organization's resource namespace.
func githubInstallOwner(org state.Org) (string, *api.Problem) {
	if org.Personal || !org.ResourceOwnershipEnabled {
		return "", api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization GitHub unavailable",
			"only an organization with its own resources can own a GitHub installation; finish billing adoption first").
			WithDocs(githubOrgInstallDocs)
	}
	if org.Status != state.OrgStatusActive {
		return "", api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Organization inactive",
			"an inactive organization cannot connect GitHub").WithDocs(githubOrgInstallDocs)
	}
	return org.ID, nil
}

// githubOrgAdmin checks that acct is a current owner or admin of org and
// returns the namespace that owns the organization's installations. It runs
// when the flow starts and again at the OAuth callback, so a member removed or
// demoted in between cannot complete it.
func (s *server) githubOrgAdmin(ctx context.Context, acct state.Account, org state.Org) (string, *api.Problem) {
	member, err := s.store.OrgMemberByAccount(ctx, org.ID, acct.ID)
	switch {
	case errors.Is(err, state.ErrNotFound) || err == nil && member.RemovedAt != nil:
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such organization")
	case err != nil:
		return "", api.ErrCapacity("could not check the organization membership")
	case member.Role != state.OrgRoleOwner && member.Role != state.OrgRoleAdmin:
		return "", api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Owner or admin required",
			"only an organization owner or admin can connect the organization's GitHub").WithDocs(githubOrgInstallDocs)
	}
	return githubInstallOwner(org)
}

// githubConnectOrgBySlug resolves the organization named on a Connect GitHub
// start and checks the caller may connect for it.
func (s *server) githubConnectOrgBySlug(ctx context.Context, acct state.Account, slug string) (state.Org, *api.Problem) {
	org, err := s.store.OrgBySlug(ctx, strings.TrimSpace(slug))
	if errors.Is(err, state.ErrNotFound) {
		return state.Org{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such organization")
	}
	if err != nil {
		return state.Org{}, api.ErrCapacity("could not load the organization")
	}
	if _, problem := s.githubOrgAdmin(ctx, acct, org); problem != nil {
		return state.Org{}, problem
	}
	return org, nil
}

// githubConnectAccountAtCallback is the namespace the OAuth callback hands
// githubd: the signed-in account, or the organization's namespace when the
// sealed state named an organization and the caller is still its owner or
// admin.
func (s *server) githubConnectAccountAtCallback(ctx context.Context, acct state.Account, orgID string) (string, *api.Problem) {
	if orgID == "" {
		return acct.ID, nil
	}
	org, err := s.store.OrgByID(ctx, orgID)
	if errors.Is(err, state.ErrNotFound) {
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such organization")
	}
	if err != nil {
		return "", api.ErrCapacity("could not load the organization")
	}
	return s.githubOrgAdmin(ctx, acct, org)
}

// projectInstallOwner is the namespace whose installations a project's
// repository resolves through: a company project's organization, or the
// caller's own account for a personal project. A member's personal
// installation never serves a company project.
func (s *server) projectInstallOwner(ctx context.Context, acct state.Account, project state.Project) (string, *api.Problem) {
	if project.OrgID == "" {
		return acct.ID, nil
	}
	org, err := s.store.OrgByID(ctx, project.OrgID)
	if err != nil {
		return "", api.ErrCapacity("could not load the project's organization")
	}
	if org.Personal {
		return acct.ID, nil
	}
	return githubInstallOwner(org)
}

// listOrgGitHubInstallations lists the organization's GitHub installations
// (GET /v1/orgs/{organization}/github/installations). Members who can read
// the organization's projects see them; tokens never leave the server.
func (s *server) listOrgGitHubInstallations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	org, err := s.store.OrgBySlug(r.Context(), r.PathValue("organization"))
	if err == nil {
		var member state.OrgMembership
		member, err = s.store.OrgMemberByAccount(r.Context(), org.ID, acct.ID)
		if err == nil && member.RemovedAt != nil {
			err = state.ErrNotFound
		}
	}
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such organization"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the organization"))
		return
	}
	out := []api.GitHubInstallationSummary{}
	if owner, problem := githubInstallOwner(org); problem == nil {
		installs, err := s.store.ListGitHubInstallationsForAccount(r.Context(), owner)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not list GitHub installations"))
			return
		}
		for _, inst := range installs {
			out = append(out, api.GitHubInstallationSummary{InstallationID: inst.InstallationID, GitHubLogin: inst.AuditGithubLogin})
		}
	}
	writeJSON(w, http.StatusOK, out)
}
