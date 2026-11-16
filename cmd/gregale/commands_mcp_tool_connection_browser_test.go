package main_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestGuidedExecutionConnectionCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	m := granttest.NewGuided(t, state.NewMemStore(), "")
	f := m.Private.Base
	t.Setenv("FAAS_API", f.Native.URL)
	t.Setenv("FAAS_TOKEN", f.Owner.Token())
	run := func(args ...string) api.ToolOAuthAuthorizationResponse {
		t.Helper()
		out, restore := main.CaptureStdout(t)
		code := main.RunToolConnectionBrowserCommandForTest(args, m.BrowserLaunch)
		restore()
		var status api.ToolOAuthAuthorizationResponse
		if code != 0 || json.Unmarshal([]byte(out.String()), &status) != nil || status.State != "connected" {
			t.Fatalf("guided CLI: %d %s", code, out.String())
		}
		for _, secret := range []string{"guided-code-", "guided-access-", "guided-refresh-", "code_verifier", "authorization_url", "callback_state"} {
			if strings.Contains(out.String(), secret) {
				t.Fatal("CLI result exposed an exchange credential")
			}
		}
		return status
	}
	status := run("connect", "--id", m.Connection.ID, "--timeout", "10s")
	if status.ConnectionID != m.Connection.ID || m.CodeExchanges.Load() != 1 {
		t.Fatal("connect did not complete one exchange")
	}
	g := m.Grant(t)
	run("connect", "--id", m.Connection.ID, "--timeout", "10s")
	if m.CodeExchanges.Load() != 1 {
		t.Fatal("repeated connect exchanged credentials again")
	}
	next := uuid.NewString()
	status = run("reconnect", "--id", next, "--from", m.Connection.ID, "--timeout", "10s")
	if status.ConnectionID != next || m.CodeExchanges.Load() != 2 {
		t.Fatal("reconnect reused old authority")
	}
	oldRecord, err := f.Owner.GetToolOAuthConnection(t.Context(), m.Connection.ID)
	if err != nil || oldRecord.State != "revoked" {
		t.Fatal("reconnect did not revoke old local authority", err)
	}
	if _, err = f.Agent.GetDelegatedToolConnection(t.Context(), g.ID); err == nil {
		t.Fatal("old grant survived reconnect")
	}
	c, err := f.Owner.GetToolOAuthConnection(t.Context(), next)
	if err != nil || c.BindingSHA256 == m.Connection.BindingSHA256 || c.Intent.RefreshTokenSecretRef != "" {
		t.Fatal("reconnect did not create a new direct credential binding", err)
	}
}

func TestGuidedExecutionDeniedConsentCLI(t *testing.T) {
	main.SetupConnectionProfiles(t)
	m := granttest.NewGuided(t, state.NewMemStore(), "")
	m.Mode.Store(3)
	t.Setenv("FAAS_API", m.Private.Base.Native.URL)
	t.Setenv("FAAS_TOKEN", m.Private.Base.Owner.Token())
	out, restore := main.CaptureStdout(t)
	code := main.RunToolConnectionBrowserCommandForTest([]string{"connect", "--id", m.Connection.ID, "--timeout", "10s"}, m.BrowserLaunch)
	restore()
	var status api.ToolOAuthAuthorizationResponse
	if code == 0 || json.Unmarshal([]byte(out.String()), &status) != nil || status.State != "authorization_denied" || m.CodeExchanges.Load() != 0 {
		t.Fatalf("denied consent: %d %s", code, out.String())
	}
	if code = main.Run([]string{"mcp", "server", "tool-connection", "connect", "--id", "invalid"}); code == 0 {
		t.Fatal("connect dispatcher accepted an invalid identity")
	}
}

func TestGuidedToolLoopbackRejectsUnboundAndRepeatedCallbacks(t *testing.T) {
	stateValue := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	callback := make(chan api.ToolOAuthAuthorizationRequest, 1)
	h := main.ToolAuthorizationCallbackForTest("127.0.0.1:34567", stateValue, "https://identity.example", callback)
	call := func(raw string) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, raw, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	base := "http://127.0.0.1:34567/callback?state=" + stateValue
	for _, bad := range []string{"http://localhost:34567/callback?state=" + stateValue + "&code=x", base + "&state=" + stateValue + "&code=x", base + "&code=x&code=y", base + "&code=x&error=denied", base + "&code=x&iss=https://other.example"} {
		if got := call(bad); got != 400 {
			t.Fatalf("unbound callback: %d", got)
		}
	}
	valid := base + "&code=code&iss=" + url.QueryEscape("https://identity.example")
	if call(valid) != 200 {
		t.Fatal("valid callback refused")
	}
	<-callback // A drained channel must not permit a second callback.
	if call(valid) != 409 {
		t.Fatal("callback replay accepted after delivery")
	}
}

func TestGuidedToolBrowserRejectsUnboundAuthorizationURL(t *testing.T) {
	m := granttest.NewGuided(t, state.NewMemStore(), "")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in api.ToolOAuthAuthorizationRequest
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("invalid browser begin request")
			w.WriteHeader(400)
			return
		}
		nonce := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		u, _ := url.Parse(m.Connection.Intent.AuthorizationEndpoint)
		u.Host = "attacker.example"
		q := url.Values{"response_type": {"code"}, "client_id": {m.Connection.Intent.ClientID}, "resource": {m.Connection.Intent.Resource}, "scope": {"tool:read"}, "state": {nonce}, "redirect_uri": {in.RedirectURI}, "code_challenge_method": {"S256"}, "code_challenge": {nonce}}
		u.RawQuery = q.Encode()
		expires := time.Now().Add(time.Minute)
		_ = json.NewEncoder(w).Encode(api.ToolOAuthAuthorizationResponse{ConnectionID: m.Connection.ID, State: "authorization_pending", AuthorizationURL: u.String(), CallbackState: nonce, ExpiresAt: &expires})
	}))
	t.Cleanup(proxy.Close)
	called := false
	_, err := main.RunToolBrowserConnectionForTest(t.Context(), api.NewClient(proxy.URL, "owner"), m.Connection, 0, func(string) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("unbound authorization URL opened a browser")
	}
}
