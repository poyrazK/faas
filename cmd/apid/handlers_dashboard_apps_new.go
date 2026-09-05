// handlers_dashboard_apps_new.go — Issue #961 / Mega-B PR-3.
//
// renderAppNew is the dashboard-side hand-off for the
// `gregale connect repo <owner>/<name>` CLI verb (PR-1) and the
// /oauth/callback redirect target (PR-B / slice 8). Three states:
//
//	env.GithubLogin empty                    → render Connect GitHub CTA
//	env.GithubLogin set, githubd unreachable → render retry banner
//	env.GithubLogin set, githubd reachable    → render install/repo/template form
//
// The wizard does NOT call bindAppToRepo server-side. The form
// POSTs to the existing /v1/apps/{new-slug}/install/bind endpoint —
// the §11 trust root is the dashboard cookie session, not a new
// wizard-internal bind API. See ADR-116 for the rationale.
//
// §11 ownership proof (sessionGithubLogin) is the load-bearing gate:
// no install_id / repo / template is shown to a session that hasn't
// completed /v1/auth/github. The Connect GitHub CTA is the only
// affordance a first-time visitor sees.
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderAppNew serves GET /dashboard/apps/new.
//
// Query params honoured:
//
//	?repo=owner/name      — pre-fill the repo <select> (CLI deep-link)
//	?install=<id>         — pre-fill the installation picker
//	?default_branch=<b>   — pre-fill the production_branch input
//
// All three are advisory; bad input is silently dropped so a stale
// bookmark can't 500 the page.
func (s *server) renderAppNew(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	q := r.URL.Query()

	prefilledRepo := q.Get("repo")
	prefilledInstall := q.Get("install")
	prefilledBranch := q.Get("default_branch")

	view := views.AppsNewView{
		PreFilledRepo:      prefilledRepo,
		PreFilledInstallID: prefilledInstall,
		PreFilledBranch:    prefilledBranch,
	}

	// Templates — populated up-front so the post-connect redirect
	// can pick up where the customer left off. The form section
	// itself is gated by NeedsGithubConnect in the template.
	view.Templates = projectAppsNewTemplates(fetchTemplatesForWizard(log))

	// §11 ownership proof. Empty env.GithubLogin → render the
	// "Connect GitHub first" CTA only; the install/repo/template
	// dropdowns would 403 anyway when the customer submitted.
	//
	// We do NOT call sessionGithubLogin here — that helper writes
	// a JSON 403 problem, which would defeat the dashboard's
	// server-rendered Connect CTA. Instead we peek the cookie
	// directly so the wizard can render the right state.
	if _, hasLogin := peekSessionGithubLogin(s, r); !hasLogin {
		view.NeedsGithubConnect = true
		tok, err := middleware.IssueForAuthenticated(s.sessions, "connect_github", acct.ID)
		if err != nil {
			log.Error("renderAppNew: csrf issue connect_github", "account_id", acct.ID, "err", err)
			renderProblem(w, log, err)
			return
		}
		view.ConnectGithubConfirmToken = tok
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieNameAuthenticated,
			Value:    tok,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.domain != "",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
		})
		renderAppsNewPage(w, log, r, view, acct)
		return
	}

	// Bind-app CSRF envelope — minted at GET time so the form submit
	// carries a fresh sealed token. Same shape as renderAccount's
	// delete + restore envelope pattern (handlers_dashboard.go:1049).
	bindTok, err := middleware.IssueForAuthenticated(s.sessions, "bind_app_to_repo", acct.ID)
	if err != nil {
		log.Error("renderAppNew: csrf issue bind_app_to_repo", "account_id", acct.ID, "err", err)
		renderProblem(w, log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.CookieNameAuthenticated,
		Value:    bindTok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
	view.BindAppConfirmToken = bindTok

	// Repos — githubd is the source of truth for what's installed
	// under this account. Best-effort: a 502 from githubd renders
	// the retry banner rather than 500ing the page.
	selectedInstallationID := int64(0)
	if prefilledInstall != "" {
		selectedInstallationID, _ = strconv.ParseInt(prefilledInstall, 10, 64)
	}
	installs, err := s.githubd.ListInstallableRepos(ctx, acct.ID, selectedInstallationID)
	if err != nil {
		// Same degradation path as listInstallableRepos: distinguish
		// the not-ready stub from the unreachable GitHub case so the
		// dashboard renders the right message.
		var problem *api.Problem
		if errors.As(err, &problem) {
			view.GitHubDegraded = true
			view.GitHubDegradedMessage = problem.Detail
			log.Warn("renderAppNew: list installable repos (problem)", "account_id", acct.ID, "err", err)
		} else {
			view.GitHubDegraded = true
			view.GitHubDegradedMessage = "Could not reach GitHub — retry in a minute."
			log.Warn("renderAppNew: list installable repos (transport)", "account_id", acct.ID, "err", err)
		}
		renderAppsNewPage(w, log, r, view, acct)
		return
	}

	repoViews := make([]views.AppsNewRepoView, 0, len(installs))
	for _, repo := range installs {
		repoViews = append(repoViews, views.AppsNewRepoView{
			RepoFullName:  repo.FullName,
			DefaultBranch: repo.DefaultBranch,
		})
	}
	sort.SliceStable(repoViews, func(i, j int) bool {
		return repoViews[i].RepoFullName < repoViews[j].RepoFullName
	})
	view.Repos = repoViews

	// Installations — at minimum, surface one synthetic "your repos"
	// install so the form has a non-empty selection. Multi-install
	// support (personal + work) lands when pkg/githubdgrpc.Repo
	// carries an InstallID.
	if prefilledInstall != "" {
		if id, err := strconv.ParseInt(prefilledInstall, 10, 64); err == nil && id > 0 {
			view.Installations = []views.AppsNewInstallView{{ID: id, AccountLogin: "your install", RepoCount: len(repoViews)}}
		}
	}
	if len(view.Installations) == 0 {
		view.Installations = []views.AppsNewInstallView{{ID: 0, AccountLogin: "your GitHub App install", RepoCount: len(repoViews)}}
	}

	renderAppsNewPage(w, log, r, view, acct)
}

// renderAppsNewPage assembles the dashboard.Page and writes it via
// the standard Render helper. Extracted so the three branches in
// renderAppNew stay readable; the helper itself is a thin shim.
func renderAppsNewPage(w http.ResponseWriter, log *slog.Logger, r *http.Request, view views.AppsNewView, acct state.Account) {
	dview, _ := AccountFrom(r.Context())
	page := dashboard.Page{
		Title:   "New app",
		Body:    "apps_new",
		Account: dashboardAccountView(dview, 0),
		Data:    view,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// fetchTemplatesForWizard is a tiny adapter that calls the in-process
// listTemplates logic without going through the HTTP wire. Same sort
// + filter logic, no JSON marshal round-trip.
//
// Re-derives from templates.Names; this avoids a second HTTP
// round-trip through /v1/templates. The handler is the
// canonical sort/filter path; a future refactor that hoists
// listTemplates into a service method will let both surfaces
// share one implementation.
func fetchTemplatesForWizard(log *slog.Logger) []templateView {
	_ = log
	rows := make([]templateView, 0, len(templates.Names))
	for _, name := range templates.Names {
		cat := templates.CategoryFor(name)
		desc := templateDescription(name)
		if cat == "" || desc == "" {
			continue
		}
		rows = append(rows, templateView{Name: name, Category: cat, Description: desc})
	}
	catOrder := map[string]int{}
	for i, c := range templates.CategoryOrder {
		catOrder[c] = i
	}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := catOrder[rows[i].Category], catOrder[rows[j].Category]
		if oi != oj {
			return oi < oj
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// Compile-time check: githubdgrpc.Repo is the wire type referenced
// from the wizard's repo projection.
var _ = githubdgrpc.Repo{}

// peekSessionGithubLogin is the side-effect-free sibling of
// sessionGithubLogin: returns (login, true) when the cookie carries
// a non-empty env.GithubLogin; ("", false) otherwise. Used by the
// dashboard's renderAppNew so the wizard can render the Connect
// GitHub CTA instead of the 403 problem sessionGithubLogin emits.
// The §11 proof is still enforced at bind time (bindAppToRepo
// re-runs sessionGithubLogin), so the wizard's "peek" path is
// read-only by design.
func peekSessionGithubLogin(s *server, r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	env, err := s.sessions.Verify(c.Value)
	if err != nil || env.GithubLogin == "" {
		return "", false
	}
	return env.GithubLogin, true
}
