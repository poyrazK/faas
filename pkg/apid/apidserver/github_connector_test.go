// adr: 20261101160300
package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// connectorFake answers the githubd calls the bearer GitHub handlers make and
// writes binds to the MemStore, as githubd does to Postgres.
type connectorFake struct {
	apidserver.StubGithubdClient
	store     *state.MemStore
	repos     []apidserver.Repo
	bindErr   error
	binds     int
	gotBranch string
	listed    []int64
}

func (f *connectorFake) ListInstallableRepos(_ context.Context, _ string, installationID int64) ([]apidserver.Repo, error) {
	f.listed = append(f.listed, installationID)
	return f.repos, nil
}

func (f *connectorFake) BindAppRepo(ctx context.Context, appID, accountID string, installationID int64, repo, branch string) (string, error) {
	f.binds++
	f.gotBranch = branch
	if f.bindErr != nil {
		return "", f.bindErr
	}
	if branch == "" {
		branch = "main"
	}
	if err := f.store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{
		AppID: appID, AccountID: accountID, BindingID: "bind-" + appID, InstallID: installationID,
		RepoFullName: repo, ProductionBranch: branch,
	}); err != nil {
		return "", err
	}
	return "bind-" + appID, nil
}

type connectorEnv struct {
	srv   *apidserver.Server
	store *state.MemStore
	gh    *connectorFake
	acct  state.Account
}

func newConnectorEnv(t *testing.T, installations ...int64) *connectorEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range installations {
		if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
			AccountID: acct.ID, InstallationID: id, AuditGithubLogin: "login-" + string(rune('a'+id%26)), SealedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	gh := &connectorFake{store: store}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, gh, nil, nil, 0, "")
	return &connectorEnv{srv: srv, store: store, gh: gh, acct: acct}
}

func (e *connectorEnv) app(t *testing.T, slug, projectID string) state.App {
	t.Helper()
	app, err := e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID, ProjectID: projectID, Slug: slug, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func (e *connectorEnv) call(t *testing.T, handler func(http.ResponseWriter, *http.Request, state.Account), method, target, slug string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	if slug != "" {
		req.SetPathValue("slug", slug)
	}
	req = req.WithContext(apidserver.WithAccount(req.Context(), e.acct))
	rec := httptest.NewRecorder()
	handler(rec, req, e.acct)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// "The account's installation" was the row sealed most recently, and token
// rotation re-seals hourly, so a listing flipped between installations.
func TestListGitHubRepositoriesNamesTheInstallation(t *testing.T) {
	env := newConnectorEnv(t, 42, 43)
	rec, body := env.call(t, env.srv.ListGitHubRepositoriesForTest, http.MethodGet, "/v1/github/repos", "", nil)
	if rec.Code != http.StatusConflict || body["code"] != api.CodeGitHubInstallationRequired || !strings.Contains(rec.Body.String(), "42, 43") {
		t.Fatalf("several installations: %d %s, want 409 naming 42 and 43", rec.Code, rec.Body.String())
	}
	rec, _ = env.call(t, env.srv.ListGitHubRepositoriesForTest, http.MethodGet, "/v1/github/repos?installation_id=43", "", nil)
	if rec.Code != http.StatusOK || len(env.gh.listed) != 1 || env.gh.listed[0] != 43 {
		t.Fatalf("named installation: %d %s, listed %v", rec.Code, rec.Body.String(), env.gh.listed)
	}
	rec, body = env.call(t, env.srv.ListGitHubRepositoriesForTest, http.MethodGet, "/v1/github/repos?installation_id=99", "", nil)
	if rec.Code != http.StatusForbidden || body["code"] != api.CodeGitHubInstallNotOwned {
		t.Fatalf("foreign installation: %d %s, want 403", rec.Code, rec.Body.String())
	}

	single := newConnectorEnv(t, 42)
	if rec, _ := single.call(t, single.srv.ListGitHubRepositoriesForTest, http.MethodGet, "/v1/github/repos", "", nil); rec.Code != http.StatusOK || single.gh.listed[0] != 42 {
		t.Fatalf("only installation: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGitHubConnectionListsSeveralInstallations(t *testing.T) {
	env := newConnectorEnv(t, 42, 43)
	app := env.app(t, "api", "")
	rec, body := env.call(t, env.srv.GetGitHubConnectionForTest, http.MethodGet, "/v1/apps/api/github", app.Slug, nil)
	installs, _ := body["installations"].([]any)
	if rec.Code != http.StatusOK || body["installation_id"] != nil || len(installs) != 2 || body["state"] != "installed" {
		t.Fatalf("unbound app: %s, want two installations and no installation_id", rec.Body.String())
	}
	if _, err := env.gh.BindAppRepo(t.Context(), app.ID, env.acct.ID, 43, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	_, body = env.call(t, env.srv.GetGitHubConnectionForTest, http.MethodGet, "/v1/apps/api/github", app.Slug, nil)
	if body["installation_id"] != float64(43) || body["state"] != "bound" || body["github_login"] == nil {
		t.Fatalf("bound app: %v, want the bound installation", body)
	}
}

// An app without a project could not hand production deploys to GitHub
// Actions, so `gregale github setup` failed after binding.
func TestGitHubDeployPolicyForAppWithoutProject(t *testing.T) {
	env := newConnectorEnv(t, 42)
	app := env.app(t, "api", "")
	rec, body := env.call(t, env.srv.GetGitHubDeployPolicyForTest, http.MethodGet, "/", app.Slug, nil)
	if rec.Code != http.StatusOK || body["project_id"] != "" || body["production_trigger"] != "webhook" {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	rec, body = env.call(t, env.srv.PatchGitHubDeployPolicyForTest, http.MethodPatch, "/", app.Slug, map[string]any{"production_trigger": "actions", "preview_enabled": false})
	if rec.Code != http.StatusConflict || body["code"] != "project_required" {
		t.Fatalf("project setting: %d %s, want 409 project_required", rec.Code, rec.Body.String())
	}
	rec, body = env.call(t, env.srv.PatchGitHubDeployPolicyForTest, http.MethodPatch, "/", app.Slug, map[string]any{"production_trigger": "actions"})
	if rec.Code != http.StatusConflict || body["code"] != "github_not_bound" {
		t.Fatalf("unbound: %d %s, want 409 github_not_bound", rec.Code, rec.Body.String())
	}
	if _, err := env.gh.BindAppRepo(t.Context(), app.ID, env.acct.ID, 42, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	rec, body = env.call(t, env.srv.PatchGitHubDeployPolicyForTest, http.MethodPatch, "/", app.Slug, map[string]any{"production_trigger": "actions"})
	if rec.Code != http.StatusOK || body["production_trigger"] != "actions" {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	if binding, err := env.store.GetGithubInstallBindingForApp(t.Context(), app.ID, env.acct.ID); err != nil || binding.ProductionTrigger != state.ProductionTriggerActions {
		t.Fatalf("stored trigger = %q, %v", binding.ProductionTrigger, err)
	}
	if _, body = env.call(t, env.srv.GetGitHubDeployPolicyForTest, http.MethodGet, "/", app.Slug, nil); body["production_trigger"] != "actions" {
		t.Fatalf("GET after PATCH: %v", body)
	}
}

// A bind without a branch took the installation's "main"; it now takes the
// repository's default branch. Branch mappings used to change before a bind
// that then failed.
func TestBindGitHubConnectionDefaultBranchAndMappingOrder(t *testing.T) {
	env := newConnectorEnv(t, 42)
	env.gh.repos = []apidserver.Repo{{FullName: "acme/api", DefaultBranch: "trunk"}}
	project, err := env.store.CreateProject(t.Context(), state.Project{AccountID: env.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	app := env.app(t, "api", project.ID)
	env.gh.bindErr = api.NewProblem(http.StatusForbidden, api.CodeGitHubRepoNotAccessible, "Repository is not accessible", "gone")
	rec, _ := env.call(t, env.srv.BindGitHubConnectionForTest, http.MethodPost, "/", app.Slug, map[string]any{
		"installation_id": 42, "repo_full_name": "acme/api", "deploy_branches": map[string]string{"release": "default"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("failed bind: %d %s", rec.Code, rec.Body.String())
	}
	if branches, _ := env.store.ListProjectDeployBranches(t.Context(), project.ID); len(branches) != 0 {
		t.Fatalf("branch mappings = %v after a failed bind, want none", branches)
	}

	env.gh.bindErr = nil
	rec, body := env.call(t, env.srv.BindGitHubConnectionForTest, http.MethodPost, "/", app.Slug, map[string]any{
		"installation_id": 42, "repo_full_name": "acme/api", "deploy_branches": map[string]string{"release": "default"},
	})
	if rec.Code != http.StatusOK || env.gh.gotBranch != "trunk" || body["production_branch"] != "trunk" {
		t.Fatalf("bind: %d %s, githubd branch %q; want the repository's trunk", rec.Code, rec.Body.String(), env.gh.gotBranch)
	}
	if branches, _ := env.store.ListProjectDeployBranches(t.Context(), project.ID); branches["release"] != "default" {
		t.Fatalf("branch mappings = %v after the bind", branches)
	}
}

var choiceCSRF = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// Several connectable installations used to end in 502 "Could not reach
// GitHub". The chooser restarts Connect GitHub with the chosen one.
func TestConnectGitHubChooserRestartsWithChosenInstallation(t *testing.T) {
	t.Setenv("FAAS_GITHUB_APP_CLIENT_ID", "client-123")
	t.Setenv("FAAS_GITHUB_APP_REDIRECT_URI", "https://gregale.dev/oauth/code-callback")
	gh := &oauthCodeCallbackFake{exchangeErr: &githubdgrpc.InstallationChoiceError{Candidates: []githubdgrpc.InstallationCandidate{
		{ID: 77, Account: "octocat", AccountType: "User"},
		{ID: 88, Account: "acme", AccountType: "Organization"},
	}}}
	srv, mgr, accountID, sessionCookie := newOAuthCodeCallbackServer(t, gh)
	stateToken := mintStateToken(t, mgr, accountID)
	callback := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code&state="+stateToken, nil)
	callback.AddCookie(sessionCookie)
	callback.AddCookie(&http.Cookie{Name: apidserver.OauthCodeStateCookie, Value: stateToken, Path: apidserver.OauthCodeCallbackPath})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, callback)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "acme (organization)") || !strings.Contains(body, "octocat (personal account)") ||
		!strings.Contains(body, `name="installation_id" value="88"`) {
		t.Fatalf("chooser: %d\n%s", rec.Code, body)
	}
	match := choiceCSRF.FindStringSubmatch(body)
	csrfCookie := findCookie(rec.Result().Cookies(), apidserver.GithubConnectCSRFCookie)
	if match == nil || csrfCookie == nil {
		t.Fatalf("chooser has no Connect GitHub proof\n%s", body)
	}

	form := url.Values{"csrf_token": {match[1]}, "installation_id": {"88"}}
	connect := httptest.NewRequest(http.MethodPost, "/dashboard/install/connect", strings.NewReader(form.Encode()))
	connect.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	connect.AddCookie(sessionCookie)
	connect.AddCookie(csrfCookie)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, connect)
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusFound || loc.Host != "github.com" || !strings.HasSuffix(loc.Query().Get("state"), ".88") {
		t.Fatalf("connect: %d %s, want GitHub authorization bound to installation 88", rec.Code, rec.Header().Get("Location"))
	}
}

// A refusal githubd can explain is the customer's to act on, not a GitHub
// outage.
func TestRenderOAuthCodeCallbackRefusalIsActionable(t *testing.T) {
	gh := &oauthCodeCallbackFake{exchangeErr: api.ErrGitHubOrgAdminRequired("acme")}
	srv, mgr, accountID, sessionCookie := newOAuthCodeCallbackServer(t, gh)
	stateToken := mintStateToken(t, mgr, accountID)
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code&state="+stateToken, nil)
	r.Header.Set("Accept", "application/json")
	r.AddCookie(sessionCookie)
	r.AddCookie(&http.Cookie{Name: apidserver.OauthCodeStateCookie, Value: stateToken, Path: apidserver.OauthCodeCallbackPath})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeGitHubOrgAdminRequired) || strings.Contains(rec.Body.String(), "https://docs/") {
		t.Fatalf("refusal: %d %s, want 403 %s", rec.Code, rec.Body.String(), api.CodeGitHubOrgAdminRequired)
	}
}
