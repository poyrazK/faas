package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type githubConnectionFake struct {
	stubGithubdClient
	repos      []Repo
	listErr    error
	unbindErr  error
	unbindCall int
	store      state.Store
}

func (f *githubConnectionFake) ListInstallableRepos(context.Context, string, int64) ([]Repo, error) {
	return f.repos, f.listErr
}

func (f *githubConnectionFake) UnbindAppRepo(ctx context.Context, appID, _ string) error {
	f.unbindCall++
	if f.store != nil && f.unbindErr == nil {
		return f.store.DeleteGithubInstallBinding(ctx, appID)
	}
	return f.unbindErr
}

func newGitHubConnectionTestServer(t *testing.T, gh GithubdClient, repos []Repo) (http.Handler, *session.Manager, *state.MemStore, state.Account, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "myapp", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 42, DefaultBranch: "main",
		SealedToken: []byte("sealed"), AuditGithubLogin: "alice",
		TokenExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertGitHubInstall: %v", err)
	}
	if err := store.UpsertGithubInstallBinding(t.Context(), state.GitHubBinding{
		AppID: app.ID, AccountID: acct.ID, BindingID: "bind-myapp-acme/api",
		InstallID: 42, RepoFullName: "acme/api", ProductionBranch: "main",
	}); err != nil {
		t.Fatalf("UpsertGithubInstallBinding: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("NewEphemeralManager: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("Issue session: %v", err)
	}
	if fake, ok := gh.(*githubConnectionFake); ok {
		fake.repos = repos
		fake.store = store
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, gh, mgr, nil, 15*time.Minute, "").handler()
	return h, mgr, store, acct, cookie
}

func githubStatusRequest(method, path, cookie, csrf string) *http.Request {
	var body io.Reader
	if csrf != "" {
		body = strings.NewReader(`{"csrf_token":"` + csrf + `"}`)
	}
	r := httptest.NewRequest(method, path, body)
	if csrf != "" {
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: githubInstallManageCSRFCookie, Value: csrf})
	}
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	return r
}

func githubStatusBody(t *testing.T, rec *httptest.ResponseRecorder) githubInstallStatusResponse {
	t.Helper()
	var got githubInstallStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, rec.Body.String())
	}
	return got
}

func TestGitHubInstallStatusIncludesHealthAndCSRF(t *testing.T) {
	gh := &githubConnectionFake{}
	h, _, _, _, cookie := newGitHubConnectionTestServer(t, gh, []Repo{{FullName: "acme/api"}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, githubStatusRequest(http.MethodGet, "/v1/apps/myapp/install", cookie, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody=%s", rec.Code, rec.Body.String())
	}
	got := githubStatusBody(t, rec)
	if !got.Connected || got.State != "bound" || got.RepoFullName != "acme/api" {
		t.Fatalf("unexpected connection status: %+v", got)
	}
	if got.Health != "unknown" {
		t.Errorf("health = %q, want unknown before first sync", got.Health)
	}
	if got.CSRFToken == "" {
		t.Fatal("GET status did not issue a CSRF token")
	}
	var csrfCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == githubInstallManageCSRFCookie {
			csrfCookie = c
		}
	}
	if csrfCookie == nil {
		t.Fatal("GET status did not set the named CSRF cookie")
	}
}

func TestSyncGitHubAppRecordsHealthyPass(t *testing.T) {
	gh := &githubConnectionFake{}
	h, _, store, acct, cookie := newGitHubConnectionTestServer(t, gh, []Repo{{FullName: "ACME/API"}})
	get := httptest.NewRecorder()
	h.ServeHTTP(get, githubStatusRequest(http.MethodGet, "/v1/apps/myapp/install", cookie, ""))
	token := githubStatusBody(t, get).CSRFToken
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, githubStatusRequest(http.MethodPost, "/v1/apps/myapp/install/sync", cookie, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody=%s", rec.Code, rec.Body.String())
	}
	got := githubStatusBody(t, rec)
	if got.SyncResult == nil || got.SyncResult.Detached || got.SyncResult.RemoteRepositoryCount != 1 {
		t.Fatalf("unexpected sync result: %+v", got.SyncResult)
	}
	if got.Health != "healthy" || got.LastReconciledAt == nil {
		t.Fatalf("sync did not project healthy state: %+v", got)
	}
	inst, err := store.GitHubInstallForAccount(context.Background(), acct.ID)
	if err != nil || inst.LastReconciledAt == nil {
		t.Fatalf("sync metadata missing: %+v %v", inst, err)
	}
}

func TestSyncGitHubAppDetachesRepositoryRemovedFromGitHub(t *testing.T) {
	gh := &githubConnectionFake{}
	h, _, store, acct, cookie := newGitHubConnectionTestServer(t, gh, nil)
	get := httptest.NewRecorder()
	h.ServeHTTP(get, githubStatusRequest(http.MethodGet, "/v1/apps/myapp/install", cookie, ""))
	token := githubStatusBody(t, get).CSRFToken
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, githubStatusRequest(http.MethodPost, "/v1/apps/myapp/install/sync", cookie, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody=%s", rec.Code, rec.Body.String())
	}
	got := githubStatusBody(t, rec)
	if got.SyncResult == nil || !got.SyncResult.Detached || got.Connected {
		t.Fatalf("expected detached app after repository removal: %+v", got)
	}
	if _, err := store.GetGithubInstallBindingForApp(context.Background(), "myapp", acct.ID); err == nil {
		t.Fatal("binding remains after sync detached it")
	}
}

func TestUnbindGitHubAppRequiresCSRFAndClearsBinding(t *testing.T) {
	gh := &githubConnectionFake{}
	h, _, store, acct, cookie := newGitHubConnectionTestServer(t, gh, []Repo{{FullName: "acme/api"}})
	withoutCSRF := httptest.NewRecorder()
	h.ServeHTTP(withoutCSRF, githubStatusRequest(http.MethodDelete, "/v1/apps/myapp/install/bind", cookie, ""))
	if withoutCSRF.Code != http.StatusBadRequest {
		t.Fatalf("without CSRF status = %d, want 400", withoutCSRF.Code)
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, githubStatusRequest(http.MethodGet, "/v1/apps/myapp/install", cookie, ""))
	token := githubStatusBody(t, get).CSRFToken
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, githubStatusRequest(http.MethodDelete, "/v1/apps/myapp/install/bind", cookie, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody=%s", rec.Code, rec.Body.String())
	}
	got := githubStatusBody(t, rec)
	if got.Connected || got.State != "installed" {
		t.Fatalf("unexpected post-unbind status: %+v", got)
	}
	if gh.unbindCall != 1 {
		t.Fatalf("unbind calls = %d, want 1", gh.unbindCall)
	}
	if _, err := store.GetGithubInstallBindingForApp(context.Background(), "myapp", acct.ID); err == nil {
		t.Fatal("binding remains after unbind")
	}
}

func TestDashboardGitHubSyncRedirectsWithFlash(t *testing.T) {
	gh := &githubConnectionFake{}
	h, _, store, acct, cookie := newGitHubConnectionTestServer(t, gh, []Repo{{FullName: "ACME/API"}})
	get := httptest.NewRecorder()
	h.ServeHTTP(get, githubStatusRequest(http.MethodGet, "/v1/apps/myapp/install", cookie, ""))
	token := githubStatusBody(t, get).CSRFToken

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, githubStatusRequest(http.MethodPost, "/dashboard/apps/myapp/github/sync", cookie, token))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/apps/myapp?github=synced" {
		t.Fatalf("location = %q, want synced flash", got)
	}
	inst, err := store.GitHubInstallForAccount(context.Background(), acct.ID)
	if err != nil || inst.LastReconciledAt == nil {
		t.Fatalf("dashboard sync did not record health: %+v %v", inst, err)
	}
}

func TestDashboardGitHubDisconnectRedirectsWithFlash(t *testing.T) {
	gh := &githubConnectionFake{}
	h, _, store, acct, cookie := newGitHubConnectionTestServer(t, gh, []Repo{{FullName: "acme/api"}})
	get := httptest.NewRecorder()
	h.ServeHTTP(get, githubStatusRequest(http.MethodGet, "/v1/apps/myapp/install", cookie, ""))
	token := githubStatusBody(t, get).CSRFToken

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, githubStatusRequest(http.MethodPost, "/dashboard/apps/myapp/github/disconnect", cookie, token))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/apps/myapp?github=disconnected" {
		t.Fatalf("location = %q, want disconnected flash", got)
	}
	if _, err := store.GetGithubInstallBindingForApp(context.Background(), "myapp", acct.ID); err == nil {
		t.Fatal("dashboard disconnect left binding behind")
	}
}
