package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostingAutomationAuth struct {
	Authenticator
	key     state.APIKey
	account state.Account
	err     error
	calls   int
}

func (f *hostingAutomationAuth) AuthenticateHostingAutomationToken(context.Context, []byte) (state.Account, state.APIKey, error) {
	f.calls++
	return f.account, f.key, f.err
}
func TestHostingAutomationExactPathsAndOperations(t *testing.T) {
	site := "11111111-1111-4111-8111-111111111111"
	id := "22222222-2222-4222-8222-222222222222"
	root := "/v1/internal/hosting/sites/" + site
	key := state.APIKey{HostingSiteID: site, HostingOperations: []string{"stage", "preview"}}
	allowed := []struct{ method, path string }{{"GET", "/v1/internal/hosting/capabilities"}, {"GET", root}, {"GET", root + "/status"}, {"GET", root + "/releases"}, {"GET", root + "/previews"}, {"POST", root + "/releases"}, {"POST", root + "/releases/" + id + "/uploads"}, {"POST", root + "/releases/" + id + "/finalize"}, {"PUT", root + "/ci/" + id}, {"GET", root + "/ci/" + id}, {"POST", root + "/ci/" + id + "/preview"}}
	for _, r := range allowed {
		if !HostingAutomationRequestAllowed(httptest.NewRequest(r.method, r.path, nil), key) {
			t.Fatal("denied", r)
		}
	}
	denied := []struct{ method, path string }{{"GET", root + "/runs"}, {"GET", root + "/runs?before=cursor"}, {"POST", root + "/ci-setup"}, {"GET", "/v1/account"}, {"GET", "/v1/apps"}, {"POST", root + "/promotions"}, {"PUT", root + "/suspension"}, {"DELETE", root}, {"DELETE", root + "/releases/" + id}, {"DELETE", root + "/previews/" + id}, {"POST", root + "/domains"}, {"POST", root + "/automation-credentials"}, {"GET", root + "/oidc-trusts"}, {"POST", root + "/oidc-trusts"}, {"DELETE", root + "/oidc-trusts/" + id}, {"POST", "/v1/internal/hosting/oidc/exchange"}, {"GET", root + "/history"}, {"GET", "/v1/internal/hosting/sites/" + id}, {"GET", root + "/releases/" + id}, {"GET", root + "/status/extra"}, {"POST", root + "/ci/" + id + "/preview/extra"}, {"GET", root + "/../" + id + "/status"}, {"GET", root + "/status%2fextra"}}
	for _, r := range denied {
		if HostingAutomationRequestAllowed(httptest.NewRequest(r.method, r.path, nil), key) {
			t.Fatal("escaped scope", r)
		}
	}
	key.HostingOperations = []string{"stage"}
	if HostingAutomationRequestAllowed(httptest.NewRequest("POST", root+"/ci/"+id+"/preview", nil), key) {
		t.Fatal("stage implies preview")
	}
	key.HostingOperations = []string{"preview"}
	if HostingAutomationRequestAllowed(httptest.NewRequest("PUT", root+"/ci/"+id, nil), key) {
		t.Fatal("preview implies staging")
	}
}
func TestHostingAutomationGateRevocationAndNoCookieFallback(t *testing.T) {
	token, _, err := api.GenerateHostingAutomationToken()
	if err != nil {
		t.Fatal(err)
	}
	auth := &hostingAutomationAuth{key: state.APIKey{ID: "credential", AccountID: "account", HostingSiteID: "11111111-1111-4111-8111-111111111111", HostingOperations: []string{"stage"}}, account: state.Account{ID: "account", Status: "active"}}
	m := &Middleware{Authn: auth}
	nextCalls := 0
	next := func(http.ResponseWriter, *http.Request, state.Account) { nextCalls++ }
	r := httptest.NewRequest("GET", "/v1/internal/hosting/capabilities", nil)
	w := httptest.NewRecorder()
	if !m.hostingAutomation(w, r, token, next) || w.Code != 503 || auth.calls != 0 {
		t.Fatal("default off", w.Code)
	}
	m.HostingAutomationEnabled = func() bool { return true }
	w = httptest.NewRecorder()
	m.hostingAutomation(w, r, token, next)
	if nextCalls != 1 {
		t.Fatal(w.Code, w.Body)
	}
	auth.err = state.ErrNotFound
	w = httptest.NewRecorder()
	if !m.hostingAutomation(w, r, token, next) || w.Code != 401 || nextCalls != 1 {
		t.Fatal("revocation did not stop next request")
	}
	auth.err = errors.New("database unavailable")
	w = httptest.NewRecorder()
	m.hostingAutomation(w, r, token, next)
	if w.Code != 503 || nextCalls != 1 {
		t.Fatal("lookup failure fell through", w.Code)
	}
	w = httptest.NewRecorder()
	if !m.hostingAutomation(w, r, api.HostingAutomationTokenPrefix+"malformed", next) || w.Code != 401 {
		t.Fatal("malformed prefix fell through")
	}
}

func TestHostingOIDCSessionKillSwitchDoesNotDisableManualCredentials(t *testing.T) {
	token, _, err := api.GenerateHostingAutomationToken()
	if err != nil {
		t.Fatal(err)
	}
	auth := &hostingAutomationAuth{key: state.APIKey{ID: "session", AccountID: "account", HostingSiteID: "11111111-1111-4111-8111-111111111111", HostingOperations: []string{"stage"}, HostingOIDCTrustID: "22222222-2222-4222-8222-222222222222"}, account: state.Account{ID: "account", Status: "active"}}
	m := &Middleware{Authn: auth, HostingAutomationEnabled: func() bool { return true }}
	next := func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(204) }
	request := func() int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/internal/hosting/capabilities", nil)
		m.hostingAutomation(w, r, token, next)
		return w.Code
	}
	if code := request(); code != 503 {
		t.Fatal("default off", code)
	}
	m.HostingOIDCEnabled = func() bool { return true }
	if code := request(); code != 204 {
		t.Fatal("enabled", code)
	}
	m.HostingOIDCEnabled = func() bool { return false }
	if code := request(); code != 503 {
		t.Fatal("kill switch", code)
	}
	auth.key.HostingOIDCTrustID = ""
	if code := request(); code != 204 {
		t.Fatal("manual credential blocked", code)
	}
}

func TestHostingDiagnosticsRequireBothSiteOperationsAndExactReadPath(t *testing.T) {
	site := "11111111-1111-4111-8111-111111111111"
	path := "/v1/internal/hosting/sites/" + site + "/diagnostics"
	for _, ops := range [][]string{{"stage"}, {"preview"}, {"stage", "preview"}} {
		key := state.APIKey{HostingSiteID: site, HostingOperations: ops}
		for _, test := range []struct {
			method, path string
			allowed      bool
		}{{"GET", path + "?run_id=22222222-2222-4222-8222-222222222222", len(ops) == 2}, {"POST", path, false}, {"GET", path + "/extra", false}, {"GET", path + "%2fextra", false}, {"GET", "/v1/internal/hosting/sites/22222222-2222-4222-8222-222222222222/diagnostics", false}} {
			if got := HostingAutomationRequestAllowed(httptest.NewRequest(test.method, test.path, nil), key); got != test.allowed {
				t.Fatal(ops, test, got)
			}
		}
	}
}
