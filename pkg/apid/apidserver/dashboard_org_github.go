package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/apid/githubconnect"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// The company's GitHub panel on the organization page (ADR-20261110200000,
// slice 2): owners and admins connect an installation the company owns and
// disconnect it; members see what is connected.

const (
	orgGitHubDisconnectAction = "org_github_disconnect"
	orgGitHubDisconnectCookie = "faas_csrf_org_github"
)

// populateOrgGitHub fills the organization page's GitHub panel. Failures are
// shown in the panel, not as a page error.
func (s *server) populateOrgGitHub(w http.ResponseWriter, ctx context.Context, log *slog.Logger, acct state.Account, org state.Org, role state.OrgRole, data *dashboard.OrgDetailData) {
	panel := &dashboard.OrgGitHubSection{CanManage: role == state.OrgRoleOwner || role == state.OrgRoleAdmin}
	data.GitHub = panel
	owner, problem := githubInstallOwner(org)
	if problem != nil {
		panel.Unavailable = problem.Detail
		return
	}
	installs, err := s.store.ListGitHubInstallationsForAccount(ctx, owner)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		log.Warn("dashboard org GitHub: list installations", "org_id", org.ID, "err", err)
		panel.Error = "could not load the organization's GitHub installations"
	}
	for _, inst := range installs {
		panel.Installations = append(panel.Installations, dashboard.OrgGitHubInstallItem{
			ID: inst.InstallationID, ConnectedBy: inst.AuditGithubLogin,
			ConnectedAt: inst.SealedAt.UTC().Format("2006-01-02 15:04 MST"),
		})
	}
	if !panel.CanManage {
		return
	}
	connect, err := issueConnectGithubToken(s, w, acct.ID)
	if err != nil {
		log.Error("dashboard org GitHub: issue connect token", "org_id", org.ID, "err", err)
		panel.CanManage = false
		return
	}
	disconnect, err := middleware.IssueForAuthenticatedNamed(s.sessions, orgGitHubDisconnectAction, acct.ID, orgGitHubDisconnectCookie)
	if err != nil {
		log.Error("dashboard org GitHub: issue disconnect token", "org_id", org.ID, "err", err)
		panel.CanManage = false
		return
	}
	http.SetCookie(w, &http.Cookie{Name: orgGitHubDisconnectCookie, Value: disconnect, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	panel.ConnectToken, panel.DisconnectToken = connect, disconnect
}

// dashboardDisconnectOrgGitHub is POST
// /dashboard/orgs/{slug}/github/{installationID}/disconnect: an owner or admin
// removes the company's installation. Company projects bound through it lose
// their repository link, wherever their resources are stored.
func (s *server) dashboardDisconnectOrgGitHub(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}
	slug := r.PathValue("slug")
	back := "/dashboard/orgs/" + url.PathEscape(slug)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, orgGitHubDisconnectAction, acct.ID, orgGitHubDisconnectCookie); err != nil {
		http.Redirect(w, r, back+"?github=forbidden", http.StatusSeeOther)
		return
	}
	installID, err := strconv.ParseInt(r.PathValue("installationID"), 10, 64)
	if err != nil || installID <= 0 {
		http.NotFound(w, r)
		return
	}
	org, err := s.store.OrgBySlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	owner, problem := s.githubOrgAdmin(r.Context(), acct, org)
	if problem != nil {
		if problem.Status == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, back+"?github=forbidden", http.StatusSeeOther)
		return
	}
	if err := s.disconnectOrgGitHub(r.Context(), org, owner, installID); err != nil {
		s.log.Error("dashboard org GitHub: disconnect", "org_id", org.ID, "install_id", installID, "err", err)
		http.Redirect(w, r, back+"?github=error", http.StatusSeeOther)
		return
	}
	ownerID := owner
	s.audit.Emit(r.Context(), "org.github.disconnected", &ownerID, map[string]any{
		"org_id": org.ID, "installation_id": installID, "disconnected_by": acct.ID,
	})
	http.Redirect(w, r, back+"?github=disconnected", http.StatusSeeOther)
}

// disconnectOrgGitHub deletes the organization's installation row (which
// unlinks projects and apps stored in the organization's namespace) and
// unlinks company projects still stored under a creator's namespace.
func (s *server) disconnectOrgGitHub(ctx context.Context, org state.Org, owner string, installID int64) error {
	store, ok := s.store.(githubconnect.AccountStore)
	if !ok {
		return errors.New("GitHub connection management is unavailable")
	}
	if err := store.DisconnectGitHubAccountInstallation(ctx, owner, installID); err != nil {
		return err
	}
	owners, ok := s.store.(state.ProjectOwnershipStore)
	if !ok {
		return nil
	}
	projects, err := owners.ListProjectsForOrg(ctx, org.ID)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.InstallID != installID || project.AccountID == owner {
			continue
		}
		if _, err := s.store.UpdateProjectBinding(ctx, project.AccountID, project.ID, "", project.ProductionBranch, 0); err != nil {
			return err
		}
	}
	return nil
}
