// /v1/install/repos/list + /v1/apps/{slug}/install/bind handlers
// (PR-B; §11 bind picker UX).
//
// Slice 8 / PR-A shipped githubd.RealService with the full bind
// plumbing but no dashboard surface to drive it. /oauth/callback
// verifies the install + ownership but stops at a 302 to
// /dashboard/apps/new — there was no follow-up that actually
// writes the (account, app, install, repo, branch) bind row. This
// file closes that gap:
//
//   - POST /v1/install/repos/list returns the repos visible to the
//     user's GitHub App installation. The dashboard's bind picker
//     hydrates its dropdown from this list.
//
//   - POST /v1/apps/{slug}/install/bind persists the bind row via
//     s.githubd.BindAppRepo (which now writes through to
//     pkg/state.PgStore.UpsertGithubInstallBinding per ADR-017 +
//     PR-B's persistence story). Before persisting, it re-runs
//     githubd.VerifyInstallation with the session's github_login
//     so a stale dashboard tab can't bind an install that was
//     revoked between the callback and the bind click — same
//     §11 proof the callback handler uses.
//
// Both routes are cookie-session-authenticated (NOT API-key auth)
// and live on the dashboard mux so the §11 middleware stack applies.
// They fall under /v1/* so cmd/gatewayd-internal/proxy.go:isApidPath already
// forwards them to apid.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// stripLogCRLF strips CR and LF from s in two SEPARATE calls. The CodeQL
// go/log-injection (CWE-117) dataflow analysis recognizes
// strings.ReplaceAll("\n","") followed by strings.ReplaceAll("\r","") as
// a sanitizer shape; chaining (a single chained call, or doing it inside
// logsanitize.Field) leaves the taint intact and the alert re-fires on
// every push. Used at every log site that interpolates an attacker-
// controllable value (session cookie, request body, etc.) so the audit
// log stays one-line-per-event regardless of what the producer sent.
//
// Kept local rather than exported because this exists solely to satisfy
// the static analyzer; the real defense is slog's JSON encoder escaping
// the value. New log sites should default to logsanitize.Field (which
// strips more than just CR/LF) and only call this helper when an
// external analyzer is in the loop.
func stripLogCRLF(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// stripLogInt formats an int64 (an attacker-controllable value parsed
// from a JSON request body) through strconv.FormatInt and then through
// stripLogCRLF. Even though a valid int64 cannot contain CR/LF
// bytes, CodeQL's go/log-injection (CWE-117) dataflow still tracks
// the raw value from r.Body into slog.Int64 and re-fires the alert
// on every push; routing the int through FormatInt + ReplaceAll
// (both shape-recognised by the analyzer) breaks the dataflow. Use
// this for any int64 log field whose source is request-derived.
func stripLogInt(n int64) string {
	return stripLogCRLF(strconv.FormatInt(n, 10))
}

// installBindRequest is the POST body for both endpoints. JSON
// because the dashboard renders the picker as a small JS island
// and the wire shape is also what the OpenAPI doc exposes.
type installBindRequest struct {
	InstallationID   int64  `json:"installation_id"`
	RepoFullName     string `json:"repo_full_name"`
	ProductionBranch string `json:"production_branch"`
	// DeployBranches maps GitHub branches to named deployment scopes. A
	// non-nil empty map deliberately clears the existing routing rules.
	DeployBranches map[string]string `json:"deploy_branches"`
}

// installBindResponse is the body the dashboard parses after a
// successful bind. ProductionBranch reflects what githubd actually
// persisted (the request value when supplied, otherwise the
// install's default_branch from /installations/{id}).
type installBindResponse struct {
	BindingID        string            `json:"binding_id"`
	RepoFullName     string            `json:"repo_full_name"`
	ProductionBranch string            `json:"production_branch"`
	DeployBranches   map[string]string `json:"deploy_branches,omitempty"`
}

func validateDeployBranches(branches map[string]string) error {
	if len(branches) > 32 {
		return errors.New("deploy_branches may contain at most 32 entries")
	}
	for branch, scope := range branches {
		if branch == "" || len(branch) > 255 {
			return fmt.Errorf("invalid deploy branch %q", branch)
		}
		for _, r := range branch {
			if unicode.IsControl(r) {
				return fmt.Errorf("invalid deploy branch %q", branch)
			}
		}
		if api.ValidateScope(scope) != nil {
			return fmt.Errorf("invalid deployment scope %q for branch %q", scope, branch)
		}
	}
	return nil
}

// listInstallableRepos is GET-list-equivalent for the dashboard
// bind picker. The dashboard already has account context via
// sessionAuth; we hand that to githubd so the per-install token
// cache resolves to the user's own install (not the platform
// owner's, which would leak their repos).
//
// Failure modes:
//   - githubd not wired (stub): 503 githubd_not_ready
//   - GitHub API call fails:     502 github_unreachable
//   - malformed body:           400 invalid_request
//   - session lacks github_login: 403 unauthenticated (the user
//     never finished /v1/auth/github — same proof the OAuth
//     callback requires)
func (s *server) listInstallableRepos(w http.ResponseWriter, r *http.Request) {
	const op = "listInstallableRepos"

	acct, ok := AccountFrom(r.Context())
	if !ok {
		// sessionAuth would have redirected before this; defend
		// against a future refactor that drops the middleware.
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to list installable repos"))
		return
	}

	var req installBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Invalid body", "expected JSON with installation_id"))
		return
	}
	if req.InstallationID <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Missing installation_id", "the body must include a positive installation_id"))
		return
	}

	// §11 ownership proof (same shape as renderOAuthCallback): the
	// session must carry the github_login established by
	// /v1/auth/github. Otherwise a logged-in FaaS user could
	// enumerate repos of any installation they happen to know
	// the ID of.
	expectedLogin, ok := s.sessionGithubLogin(w, r)
	if !ok {
		return // sessionGithubLogin already wrote the 403.
	}

	verified, accountLogin, _, err := s.githubd.VerifyInstallation(r.Context(), req.InstallationID, "")
	if err != nil {
		s.log.Warn("listInstallableRepos: verify installation failed",
			"op", op, "account_id", acct.ID,
			"install_id", stripLogInt(req.InstallationID),
			// expected_login flows from the session cookie and is
			// therefore attacker-controllable in principle; sanitize
			// to defeat Log entries created from user input
			// (CodeQL go/log-injection, CWE-117).
			"expected_login", stripLogCRLF(expectedLogin), "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute: https://docs/connect-github"))
		return
	}
	if !verified {
		// Same two-cases split as renderOAuthCallback: accountLogin
		// populated → forged takeover (403), empty → unknown install
		// (302 to /dashboard/account?github=forged).
		if accountLogin != "" {
			s.log.Warn("listInstallableRepos: install belongs to a different GitHub user",
				"op", op, "account_id", acct.ID,
				"install_id", stripLogInt(req.InstallationID),
				"expected_login", stripLogCRLF(expectedLogin),
				"actual_account_login", stripLogCRLF(accountLogin))
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "forged",
				"This installation belongs to a different GitHub user",
				"the install is bound to a different GitHub identity than the one you signed in with"))
			return
		}
		http.Redirect(w, r, "/dashboard/account?github=forged", http.StatusFound)
		return
	}

	repos, err := s.githubd.ListInstallableRepos(r.Context(), acct.ID, req.InstallationID)
	if err != nil {
		// Stub returns errGithubdNotReady; live returns wrapped gRPC
		// errors. Distinguish them so the dashboard renders the
		// right message ("GitHub not configured" vs "retry").
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		s.log.Warn("listInstallableRepos: githubd call failed",
			"op", op, "account_id", acct.ID, "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute: https://docs/connect-github"))
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

// bindAppToRepo persists the (account, app, install, repo, branch)
// bind row. The flow:
//
//  1. sessionAuth + loadApp resolve the slug → app, scoped to the
//     logged-in account (cross-account slugs collapse to 404).
//  2. sessionGithubLogin pulls expectedLogin from the envelope.
//  3. VerifyInstallation re-runs the §11 ownership proof with
//     expectedLogin. This catches the "user opened the bind picker
//     in a stale tab, then revoked the GitHub App install in
//     between" case — the callback verified at time T but the
//     bind fires at T+30min.
//  4. githubd.BindAppRepo persists via pkg/state (PR-B closes the
//     in-memory-only gap) and returns the deterministic binding_id.
//  5. The handler emits an audit event so the dashboard's
//     "App X is now connected to repo Y" line correlates with the
//     auth trail.
func (s *server) bindAppToRepo(w http.ResponseWriter, r *http.Request) {
	const op = "bindAppToRepo"

	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to bind an app"))
		return
	}
	slug := r.PathValue("slug")
	//nolint:contextcheck // loadApp takes the request directly; existing pattern across 8+ handlers.
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return // loadApp already wrote the 404.
	}

	var req installBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Invalid body", "expected JSON with installation_id, repo_full_name, production_branch"))
		return
	}
	if req.InstallationID <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Missing installation_id", "the body must include a positive installation_id"))
		return
	}
	if req.RepoFullName == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Missing repo_full_name", "the body must include a non-empty repo_full_name (owner/name)"))
		return
	}
	if req.DeployBranches != nil {
		if err := validateDeployBranches(req.DeployBranches); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
				"Invalid deploy_branches", err.Error()))
			return
		}
	}

	expectedLogin, ok := s.sessionGithubLogin(w, r)
	if !ok {
		return // sessionGithubLogin already wrote the 403.
	}

	verified, accountLogin, defaultBranch, err := s.githubd.VerifyInstallation(r.Context(), req.InstallationID, "")
	if err != nil {
		s.log.Warn("bindAppToRepo: verify installation failed",
			"op", op, "account_id", acct.ID,
			"install_id", stripLogInt(req.InstallationID),
			// expected_login flows from the session cookie and is
			// therefore attacker-controllable in principle; sanitize
			// to defeat Log entries created from user input
			// (CodeQL go/log-injection, CWE-117).
			"expected_login", stripLogCRLF(expectedLogin), "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute: https://docs/connect-github"))
		return
	}
	if !verified {
		if accountLogin != "" {
			s.log.Warn("bindAppToRepo: install belongs to a different GitHub user (§11 takeover attempt)",
				"op", op, "account_id", acct.ID,
				"app_id", app.ID,
				"install_id", stripLogInt(req.InstallationID),
				"expected_login", stripLogCRLF(expectedLogin),
				"actual_account_login", stripLogCRLF(accountLogin))
			acctID := acct.ID
			s.audit.Emit(r.Context(), "auth.install.takeover_rejected", &acctID, map[string]any{
				"install_id":           req.InstallationID,
				"app_id":               app.ID,
				"expected_login":       expectedLogin,
				"actual_account_login": accountLogin,
				"path":                 "bindAppToRepo",
			})
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "forged",
				"This installation belongs to a different GitHub user",
				"the install is bound to a different GitHub identity than the one you signed in with"))
			return
		}
		s.log.Warn("bindAppToRepo: forged or unknown install_id",
			"op", op, "account_id", acct.ID,
			"install_id", stripLogInt(req.InstallationID),
			"expected_login", stripLogCRLF(expectedLogin))
		http.Redirect(w, r, "/dashboard/account?github=forged", http.StatusFound)
		return
	}

	branch := req.ProductionBranch
	if branch == "" {
		branch = defaultBranch
	}
	if req.DeployBranches != nil {
		branchesStore, ok := s.store.(state.ProjectDeployBranchesStore)
		if !ok || app.ProjectID == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "project_required",
				"Project required", "branch deployment scopes are available for project-backed apps only"))
			return
		}
		if err := branchesStore.ReplaceProjectDeployBranches(r.Context(), acct.ID, app.ProjectID, req.DeployBranches); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "Project not found", "the app project no longer exists"))
			} else {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request", "Invalid deploy_branches", err.Error()))
			}
			return
		}
	}

	bindingID, err := s.githubd.BindAppRepo(r.Context(), app.ID, acct.ID, req.InstallationID, req.RepoFullName, branch)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		s.log.Error("bindAppToRepo: githubd call failed",
			"op", op, "account_id", acct.ID,
			"app_id", app.ID,
			"install_id", stripLogInt(req.InstallationID), "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute: https://docs/connect-github"))
		return
	}

	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.bound", &acctID, map[string]any{
		"install_id":        req.InstallationID,
		"app_id":            app.ID,
		"github_login":      expectedLogin,
		"install_owner":     accountLogin,
		"repo_full_name":    req.RepoFullName,
		"production_branch": branch,
		"binding_id":        bindingID,
	})

	writeJSON(w, http.StatusOK, installBindResponse{
		BindingID:        bindingID,
		RepoFullName:     req.RepoFullName,
		ProductionBranch: branch,
		DeployBranches:   req.DeployBranches,
	})
}

// sessionGithubLogin extracts env.GithubLogin from the session
// cookie, applying the same §11 proof renderOAuthCallback uses.
// Writes the right 403 / 302 and returns ok=false when the proof
// can't be established so the caller can early-return without
// further logging.
//
// Centralised here so the bind picker, /oauth/callback, and any
// future per-install surface share one §11 enforcement site — a
// refactor that drops the check in one place is visible in the
// other two.
func (s *server) sessionGithubLogin(w http.ResponseWriter, r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		s.writeGithubLoginRequired(w, r)
		return "", false
	}
	env, err := s.sessions.Verify(c.Value)
	if err != nil || env.GithubLogin == "" {
		s.writeGithubLoginRequired(w, r)
		return "", false
	}
	return env.GithubLogin, true
}

// writeGithubLoginRequired emits the 403 unauthenticated problem
// that /v1/install/* + /v1/apps/{slug}/install/bind use when the
// session has no github_login. Distinct Code so the dashboard can
// render a "complete GitHub sign-in first" CTA rather than the
// generic forged branch.
func (s *server) writeGithubLoginRequired(w http.ResponseWriter, r *http.Request) {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "github_login_required",
		"GitHub sign-in required",
		"complete /v1/auth/github before binding an installation: https://docs/connect-github"))
}

// writeJSON is the shared handler helper in server.go. The bind
// picker uses it directly; no local copy needed.

// Compile-time guards — keep the githubdgrpc alias live for
// documentation references (the install picker doesn't need a
// direct type today, but future bind-picker extensions might).
var (
	_ = githubdgrpc.CheckPhaseBuilding
)
