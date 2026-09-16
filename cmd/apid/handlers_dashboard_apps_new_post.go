package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	githubWizardCreateAction     = "github_wizard_create_app"
	githubWizardCreateCSRFCookie = "faas_csrf_github_wizard_create"
	githubConnectAction          = "connect_github"
	githubConnectCSRFCookie      = "faas_csrf_github_connect"
	githubConnectReturnCookie    = "faas_github_connect_return"
	githubBindAction             = "bind_app_to_repo"
	githubBindCSRFCookie         = "faas_csrf_github_bind"
)

// issueConnectGithubToken mints the token used by the dashboard's
// Connect GitHub form. Keeping this in one helper lets the account page,
// the new-app wizard, and the "install the App" recovery state share the
// same CSRF contract.
func issueConnectGithubToken(s *server, w http.ResponseWriter, accountID string) (string, error) {
	tok, err := middleware.IssueForAuthenticatedNamed(s.sessions, githubConnectAction, accountID, githubConnectCSRFCookie)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     githubConnectCSRFCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
	return tok, nil
}

func dashboardGitHubReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/dashboard/account"
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/dashboard/") {
		return "/dashboard/account"
	}
	return u.RequestURI()
}

func setGitHubConnectReturn(w http.ResponseWriter, r *http.Request, returnTo string) {
	http.SetCookie(w, &http.Cookie{
		Name:     githubConnectReturnCookie,
		Value:    dashboardGitHubReturnTo(returnTo),
		Path:     "/oauth",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthCodeStateTTL.Seconds()),
	})
}

func githubConnectReturnTo(r *http.Request) string {
	c, err := r.Cookie(githubConnectReturnCookie)
	if err != nil {
		return "/dashboard/account"
	}
	return dashboardGitHubReturnTo(c.Value)
}

func clearGitHubConnectReturn(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     githubConnectReturnCookie,
		Value:    "",
		Path:     "/oauth",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func githubConnectedRedirect(returnTo string, values url.Values) string {
	u, err := url.Parse(dashboardGitHubReturnTo(returnTo))
	if err != nil {
		return "/dashboard/account"
	}
	query := u.Query()
	for key, items := range values {
		if len(items) > 0 {
			query.Set(key, items[0])
		}
	}
	u.RawQuery = query.Encode()
	return u.RequestURI()
}

// createAppFromGitHubWizard is the browser form adapter for the dashboard
// deploy flow. It creates the app first, then writes the same durable GitHub
// binding used by the API surface. The repository is checked against the
// selected installation before either mutation so a stale picker cannot
// bind an inaccessible repository.
func (s *server) createAppFromGitHubWizard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Redirect(w, r, loginPath, http.StatusFound)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, githubWizardCreateAction, acct.ID, githubWizardCreateCSRFCookie); err != nil {
		redirectGitHubWizardError(w, r, "This form expired. Reload the page and try again.", r.FormValue("repo_full_name"), r.FormValue("installation_id"), r.FormValue("production_branch"), r.FormValue("slug"))
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectGitHubWizardError(w, r, "The form could not be read. Reload the page and try again.", "", "", "", "")
		return
	}

	installationID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("installation_id")), 10, 64)
	if err != nil || installationID <= 0 {
		redirectGitHubWizardError(w, r, "Choose a GitHub installation first.", r.FormValue("repo_full_name"), r.FormValue("installation_id"), r.FormValue("production_branch"), r.FormValue("slug"))
		return
	}
	repo := strings.TrimSpace(r.FormValue("repo_full_name"))
	if !validProjectRepoFullName(repo) {
		redirectGitHubWizardError(w, r, "Choose a valid GitHub repository.", repo, strconv.FormatInt(installationID, 10), r.FormValue("production_branch"), r.FormValue("slug"))
		return
	}
	slug := strings.TrimSpace(r.FormValue("slug"))
	if !validSlug(slug) || api.IsReservedAppSlug(slug) {
		redirectGitHubWizardError(w, r, "Choose an available app slug using lowercase letters, digits, and dashes.", repo, strconv.FormatInt(installationID, 10), r.FormValue("production_branch"), slug)
		return
	}
	branch := strings.TrimSpace(r.FormValue("production_branch"))
	if branch != "" && !validProjectBranch(branch) {
		redirectGitHubWizardError(w, r, "Choose a valid production branch.", repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}

	expectedLogin, ok := s.sessionGithubLogin(w, r)
	if !ok {
		return
	}
	verified, accountLogin, defaultBranch, err := s.githubd.VerifyInstallation(r.Context(), installationID, "")
	if err != nil {
		redirectGitHubWizardError(w, r, "GitHub could not be reached. Retry in a minute.", repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}
	if !verified {
		if accountLogin != "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "forged", "This installation belongs to a different GitHub user", "reconnect GitHub with the identity that owns this installation"))
			return
		}
		redirectGitHubWizardError(w, r, "That GitHub installation is no longer available. Reconnect GitHub and try again.", repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}

	repos, err := s.githubd.ListInstallableRepos(r.Context(), acct.ID, installationID)
	if err != nil {
		redirectGitHubWizardError(w, r, "GitHub repositories could not be loaded. Retry in a minute.", repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}
	found := false
	for _, candidate := range repos {
		if canonicalGitHubRepo(candidate.FullName) == canonicalGitHubRepo(repo) {
			found = true
			if branch == "" {
				branch = candidate.DefaultBranch
			}
			break
		}
	}
	if !found {
		redirectGitHubWizardError(w, r, "That repository is not available to the selected GitHub installation.", repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}
	if branch == "" {
		branch = defaultBranch
	}
	if !validProjectBranch(branch) {
		redirectGitHubWizardError(w, r, "The selected repository has no valid default branch. Choose one manually.", repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}

	app, problem := s.createDashboardWizardApp(r.Context(), acct, slug)
	if problem != nil {
		redirectGitHubWizardError(w, r, wizardProblemMessage(problem), repo, strconv.FormatInt(installationID, 10), branch, slug)
		return
	}
	bindingID, err := s.githubd.BindAppRepo(r.Context(), app.ID, acct.ID, installationID, repo, branch)
	if err != nil {
		// Keep the app so the customer can retry the connection from its
		// detail page; creation itself is durable and quota-accounted.
		s.log.Warn("GitHub wizard: bind failed", "app_id", app.ID, "account_id", acct.ID, "err", err)
		dashboardGitHubRedirect(w, r, slug, "bind-error")
		return
	}

	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.bound", &acctID, map[string]any{
		"install_id":        installationID,
		"app_id":            app.ID,
		"github_login":      expectedLogin,
		"install_owner":     accountLogin,
		"repo_full_name":    repo,
		"production_branch": branch,
		"binding_id":        bindingID,
		"surface":           "dashboard_wizard",
	})
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"?github=connected", http.StatusSeeOther)
}

func (s *server) createDashboardWizardApp(ctx context.Context, acct state.Account, slug string) (state.App, *api.Problem) {
	limits := api.MustLimitsFor(acct.Plan)
	app, problem := s.buildApp(acct, api.CreateAppRequest{Slug: slug}, limits)
	if problem != nil {
		return state.App{}, problem
	}
	created, err := s.store.CreateAppIfUnderQuota(ctx, app, limits)
	if err != nil {
		var quotaErr *state.QuotaError
		switch {
		case errors.As(err, &quotaErr):
			return state.App{}, api.ErrPlanLimitApps(limits, quotaErr.Observed)
		case errors.Is(err, state.ErrConflict):
			return state.App{}, api.NewProblem(http.StatusConflict, api.CodeValidation, "Slug taken", fmt.Sprintf("app slug %q is already in use", slug))
		default:
			return state.App{}, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity, "Capacity", "could not create app")
		}
	}
	s.log.Info("app created from GitHub wizard", "app_id", created.ID, "account_id", acct.ID)
	s.audit.Emit(ctx, "app.created", &acct.ID, map[string]any{
		"app_id":  created.ID,
		"slug":    created.Slug,
		"type":    string(created.Type),
		"runtime": created.Runtime,
		"surface": "dashboard_wizard",
	})
	s.emitAppCreated(ctx, created)
	return created, nil
}

func wizardProblemMessage(problem *api.Problem) string {
	if problem == nil || problem.Detail == "" {
		return "The app could not be created. Check the form and try again."
	}
	return problem.Detail
}

func redirectGitHubWizardError(w http.ResponseWriter, r *http.Request, message, repo, installationID, branch, slug string) {
	q := url.Values{}
	q.Set("error", message)
	if repo != "" {
		q.Set("repo", repo)
	}
	if installationID != "" {
		q.Set("install", installationID)
	}
	if branch != "" {
		q.Set("default_branch", branch)
	}
	if slug != "" {
		q.Set("slug", slug)
	}
	http.Redirect(w, r, "/dashboard/apps/new?"+q.Encode(), http.StatusSeeOther)
}
