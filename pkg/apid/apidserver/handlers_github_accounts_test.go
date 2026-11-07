package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f *bindPickerFake) GetInstallationAccess(context.Context, string, int64) (githubdgrpc.InstallationAccess, error) {
	return githubdgrpc.InstallationAccess{AccountType: "Organization", RepositorySelection: "selected", Permissions: map[string]string{"contents": "read", "checks": "write"}}, nil
}
func TestGitHubAccountConnectionsVerifyAndDisconnectIsolation(t *testing.T) {
	handler, _, first, cookie, store := newBindPickerTestServer(t, &bindPickerFake{repos: []apidserver.Repo{{FullName: "alice/api"}}})
	second, err := store.CreateAccount(t.Context(), "other-account@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: second.ID, InstallationID: 42, AuditGithubLogin: "alice", SealedToken: []byte("second-secret"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOAuthLink(t.Context(), first, "github", "same-github-user", "alice@example.com", true); err != nil {
		t.Fatal(err)
	}
	read := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/install/connections", nil)
		r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("list %d %s", w.Code, w.Body.String())
		}
		return w
	}
	mutate := func(method, path string, withCSRF bool) *httptest.ResponseRecorder {
		status := read()
		var body struct {
			Token string `json:"csrf_token"`
		}
		if err := json.Unmarshal(status.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
		if withCSRF {
			for _, c := range status.Result().Cookies() {
				r.AddCookie(c)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	listed := read()
	if strings.Contains(listed.Body.String(), "sealed") || strings.Contains(listed.Body.String(), "secret") {
		t.Fatal("credentials returned to browser")
	}
	rejected := mutate("DELETE", "/v1/install/connections/42", false)
	if rejected.Code != 400 {
		t.Fatalf("disconnect without cookie allowed %d", rejected.Code)
	}
	verified := mutate("POST", "/v1/install/connections/42/verify", true)
	if verified.Code != 200 || !strings.Contains(verified.Body.String(), `"checks":"write"`) {
		t.Fatalf("verify %d %s", verified.Code, verified.Body.String())
	}
	if !strings.Contains(read().Body.String(), `"account_type":"Organization"`) {
		t.Fatal("verification did not survive a new read")
	}
	foreign := mutate("POST", "/v1/install/connections/999/verify", true)
	if foreign.Code != 404 {
		t.Fatalf("foreign installation verification %d", foreign.Code)
	}
	removed := mutate("DELETE", "/v1/install/connections/42", true)
	if removed.Code != 204 {
		t.Fatalf("disconnect %d %s", removed.Code, removed.Body.String())
	}
	if _, err := store.GitHubInstallForAccountInstallation(t.Context(), first, 42); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("first credential retained: %v", err)
	}
	other, err := store.GitHubInstallForAccountInstallation(t.Context(), second.ID, 42)
	if err != nil || string(other.SealedToken) != "second-secret" {
		t.Fatalf("other connection changed: %v", err)
	}
	link, err := store.OAuthLinkByProviderSubject(t.Context(), "github", "same-github-user")
	if err != nil || link.AccountID != first {
		t.Fatalf("sign-in changed: %+v %v", link, err)
	}
	if err := store.UpsertOAuthLink(t.Context(), second.ID, "github", "same-github-user", "other@example.test", true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("login became shared: %v", err)
	}
	if !strings.Contains(read().Body.String(), `"connections":[]`) {
		t.Fatal("disconnected installation still listed")
	}
}
