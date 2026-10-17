package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBrowserGitHubConnectedAccountWithoutGitHubLogin(t *testing.T) {
	for _, owner := range []string{"alice", "acme-org"} {
		t.Run(owner, func(t *testing.T) {
			gh := &bindPickerFake{verified: true, accountLogin: owner, defaultBranch: "master", repos: []apidserver.Repo{{FullName: "acme/api", DefaultBranch: "master"}}}
			srv, mgr, accountID, cookie, _ := newBindPickerTestServer(t, gh)
			env, err := mgr.Verify(cookie)
			if err != nil || env.GithubLogin != "" {
				t.Fatalf("expected ordinary browser session: %+v %v", env, err)
			}
			list := httptest.NewRequest(http.MethodPost, "/v1/install/repos/list", strings.NewReader(`{"installation_id":42}`))
			list.Header.Set("Content-Type", "application/json")
			list.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, list)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "acme/api") {
				t.Fatalf("list connected installation: %d %s", rec.Code, rec.Body)
			}
			bind := bindJSONRequest(t, mgr, accountID, `{"installation_id":42,"repo_full_name":"acme/api"}`)
			bind.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
			rec = httptest.NewRecorder()
			srv.ServeHTTP(rec, bind)
			if rec.Code != http.StatusOK || gh.bindCalls != 1 || gh.gotInstallID != 42 || gh.gotExpectedLogin != "" {
				t.Fatalf("bind connected installation: %d %s calls=%d install=%d login=%q", rec.Code, rec.Body, gh.bindCalls, gh.gotInstallID, gh.gotExpectedLogin)
			}
			var response map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response["production_branch"] != "master" {
				t.Fatalf("bind default branch: %s %v", rec.Body, err)
			}
		})
	}
}

func TestBrowserGitHubForeignAndUnknownInstallationsDeniedBeforeLookup(t *testing.T) {
	for _, action := range []string{"list", "bind"} {
		t.Run(action, func(t *testing.T) {
			gh := &bindPickerFake{verified: true, accountLogin: "bob", repos: []apidserver.Repo{{FullName: "bob/private"}}}
			srv, mgr, accountID, cookie, store := newBindPickerTestServer(t, gh)
			other, err := store.CreateAccount(t.Context(), "bob@example.com", "free")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: other.ID, InstallationID: 43, AuditGithubLogin: "bob", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			// Even a signed login claim matching the foreign installation's
			// owner must not grant access to another Gregale account's connection.
			cookie = stampCookie(t, mgr, cookie, "bob")
			var bodies []string
			for _, id := range []string{"43", "44"} {
				var req *http.Request
				if action == "bind" {
					req = bindJSONRequest(t, mgr, accountID, `{"installation_id":`+id+`,"repo_full_name":"bob/private"}`)
				} else {
					req = httptest.NewRequest(http.MethodPost, "/v1/install/repos/list", strings.NewReader(`{"installation_id":`+id+`}`))
					req.Header.Set("Content-Type", "application/json")
				}
				req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				apidserver.AssertProblem(t, rec, http.StatusForbidden, api.CodeGitHubInstallNotOwned)
				bodies = append(bodies, rec.Body.String())
			}
			if bodies[0] != bodies[1] || gh.gotInstallID != 0 || gh.listCalls != 0 || gh.bindCalls != 0 {
				t.Fatalf("unowned installation reached GitHub or disclosed ownership: verify=%d list=%d bind=%d bodies=%v", gh.gotInstallID, gh.listCalls, gh.bindCalls, bodies)
			}
		})
	}
}

func TestBrowserGitHubOwnedInstallationStillRequiresBindCSRF(t *testing.T) {
	gh := &bindPickerFake{verified: true, accountLogin: "alice"}
	srv, _, _, cookie, _ := newBindPickerTestServer(t, gh)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/myapp/install/bind", strings.NewReader(`{"installation_id":42,"repo_full_name":"acme/api"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	if gh.gotInstallID != 0 || gh.bindCalls != 0 {
		t.Fatal("binding without CSRF reached GitHub")
	}
}

func TestBrowserGitHubUnavailableInstallationReturnsProblem(t *testing.T) {
	for _, action := range []string{"list", "bind"} {
		t.Run(action, func(t *testing.T) {
			gh := &bindPickerFake{verified: false}
			srv, mgr, accountID, cookie, _ := newBindPickerTestServer(t, gh)
			req := httptest.NewRequest(http.MethodPost, "/v1/install/repos/list", strings.NewReader(`{"installation_id":42}`))
			req.Header.Set("Content-Type", "application/json")
			if action == "bind" {
				req = bindJSONRequest(t, mgr, accountID, `{"installation_id":42,"repo_full_name":"acme/api"}`)
			}
			req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			apidserver.AssertProblem(t, rec, http.StatusForbidden, "forged")
			if rec.Header().Get("Location") != "" || gh.listCalls != 0 || gh.bindCalls != 0 || gh.gotInstallID != 42 {
				t.Fatalf("unavailable installation redirected or continued: headers=%v verify=%d list=%d bind=%d", rec.Header(), gh.gotInstallID, gh.listCalls, gh.bindCalls)
			}
		})
	}
}
