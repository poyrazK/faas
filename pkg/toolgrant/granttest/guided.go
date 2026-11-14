package granttest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolconnection"
)

type guidedCode struct {
	challenge, redirect string
	used                bool
}

// GuidedFixture issues real local authorization codes, checks S256 PKCE and
// rotates refresh tokens. No application secret contains its credentials.
// Modes: 1 lost reply, 2 changed scopes, 3 denied consent, 4 blocked exchange,
// 5 omitted refresh token, 6 redirect, 7 oversized token response.
type GuidedFixture struct {
	Private          *PrivateFixture
	AccountIdentity  *age.X25519Identity
	Provider         *httptest.Server
	Connection       api.ToolOAuthConnectionRecord
	Mode             atomic.Int32
	ExpiresIn        atomic.Int64
	CodeExchanges    atomic.Int32
	RefreshExchanges atomic.Int32
	Started          chan struct{}
	Release          chan struct{}
	mu               sync.Mutex
	codes            map[string]guidedCode
	sequence         int
	refresh          string
	started          sync.Once
}

func NewGuided(t *testing.T, store state.Store, agentToken string) *GuidedFixture {
	t.Helper()
	f := New(t, store, agentToken, true)
	runtimeID, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	p := &PrivateFixture{Base: f, Identity: runtimeID, Runtime: NewRuntime(t, f, "not-issued")}
	m := &GuidedFixture{Private: p, AccountIdentity: accountID, Started: make(chan struct{}), Release: make(chan struct{}), codes: map[string]guidedCode{}}
	m.ExpiresIn.Store(3600)
	m.Provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.providerRequest(t, w, r) }))
	t.Cleanup(m.Provider.Close)
	m.RestartExecutor(t)
	m.Connection = m.CreateConnection(t)
	return m
}

func (m *GuidedFixture) CreateConnection(t *testing.T) api.ToolOAuthConnectionRecord {
	t.Helper()
	f := m.Private.Base
	c, err := f.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), api.ToolOAuthConnectionIntent{Guided: true, App: f.AppA.Slug, Scope: api.DefaultEnvScope, Issuer: m.Provider.URL + "/issuer", AuthorizationEndpoint: m.Provider.URL + "/authorize", TokenEndpoint: m.Provider.URL + "/token", ClientID: "execution-client", Resource: "https://tool.example/mcp", Scopes: []string{"tool:read"}, TokenAuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (m *GuidedFixture) RestartExecutor(t *testing.T) {
	t.Helper()
	f, p := m.Private.Base, m.Private
	if f.PrivateExecutor != nil {
		f.PrivateExecutor.Close()
	}
	f.PrivateExecutor = toolregistry.NewPrivateExecutor(f.Store, toolregistry.PrivateExecutionOptions{Enabled: true, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{p.Identity} }, ConnectionIdentities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{m.AccountIdentity} }, ConnectionRecipients: func() ([]*age.X25519Recipient, error) {
		return []*age.X25519Recipient{m.AccountIdentity.Recipient()}, nil
	}, Endpoint: func(context.Context, state.App, state.Deployment) (string, error) { return f.PreviewURL, nil }, VerifyPin: func(context.Context, api.ToolInvocationTarget, string) error { return nil }, HTTP: p.Runtime.Server.Client()})
	t.Cleanup(f.PrivateExecutor.Close)
}

func (m *GuidedFixture) Begin(t *testing.T) api.ToolOAuthAuthorizationResponse {
	t.Helper()
	out, err := m.Private.Base.Owner.AuthorizeToolOAuthConnection(t.Context(), m.Connection.ID, api.ToolOAuthAuthorizationRequest{Action: "begin", RedirectURI: "http://127.0.0.1:34567/callback"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (m *GuidedFixture) Callback(t *testing.T, begin api.ToolOAuthAuthorizationResponse) api.ToolOAuthAuthorizationRequest {
	t.Helper()
	client := *m.Provider.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Get(begin.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization failed: %d", resp.StatusCode)
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	return api.ToolOAuthAuthorizationRequest{Action: "complete", State: q.Get("state"), Code: q.Get("code"), Denied: q.Get("error") != "", Issuer: q.Get("iss")}
}

func (m *GuidedFixture) Connect(t *testing.T) {
	t.Helper()
	start := m.Begin(t)
	out, err := m.Private.Base.Owner.AuthorizeToolOAuthConnection(t.Context(), m.Connection.ID, m.Callback(t, start))
	if err != nil || out.State != "connected" {
		t.Fatal("guided authorization", err)
	}
}

func (m *GuidedFixture) Grant(t *testing.T) api.ToolGrantRecord {
	t.Helper()
	f := m.Private.Base
	g, err := f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour), CredentialConnectionID: m.Connection.ID})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (m *GuidedFixture) providerRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Header.Get("Authorization") != "" {
		t.Error("platform bearer forwarded to provider")
		w.WriteHeader(400)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/authorize" {
		m.authorize(t, w, r)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/token" || r.ParseForm() != nil || r.Form.Get("client_id") != "execution-client" || r.Form.Get("resource") != "https://tool.example/mcp" {
		t.Error("token request escaped provider binding")
		w.WriteHeader(400)
		return
	}
	m.mu.Lock()
	valid := false
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		x, ok := m.codes[r.Form.Get("code")]
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		valid = ok && !x.used && x.redirect == r.Form.Get("redirect_uri") && x.challenge == base64.RawURLEncoding.EncodeToString(sum[:])
		if valid {
			x.used = true
			m.codes[r.Form.Get("code")] = x
			m.CodeExchanges.Add(1)
		}
	case "refresh_token":
		valid = m.refresh != "" && r.Form.Get("refresh_token") == m.refresh && r.Form.Get("scope") == "tool:read"
		if valid {
			m.RefreshExchanges.Add(1)
		}
	}
	if !valid {
		m.mu.Unlock()
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	m.sequence++
	n := m.sequence
	m.refresh = "guided-refresh-" + strconv.Itoa(n)
	refresh := m.refresh
	m.mu.Unlock()
	m.started.Do(func() { close(m.Started) })
	if m.Mode.Load() == 4 {
		select {
		case <-m.Release:
		case <-r.Context().Done():
			return
		}
	}
	access := "guided-access-" + strconv.Itoa(n)
	m.Private.Runtime.Credential.Store(access)
	m.Private.Runtime.Reflections.Store([]string{refresh})
	if m.Mode.Load() == 1 {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	if m.Mode.Load() == 6 {
		w.Header().Set("Location", m.Provider.URL+"/token")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}
	if m.Mode.Load() == 7 {
		_, _ = w.Write([]byte(strings.Repeat("x", api.ToolOAuthProviderResponseMaxBytes+1)))
		return
	}
	scope := "tool:read"
	if m.Mode.Load() == 2 {
		scope = "tool:read tool:write"
	}
	if m.Mode.Load() == 5 {
		refresh = ""
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": m.ExpiresIn.Load(), "scope": scope})
}

func (m *GuidedFixture) authorize(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	q := r.URL.Query()
	if q.Get("client_id") != "execution-client" || q.Get("response_type") != "code" || q.Get("resource") != "https://tool.example/mcp" || q.Get("scope") != "tool:read" || q.Get("code_challenge_method") != "S256" || !toolconnection.ValidAuthorizationState(q.Get("code_challenge")) || !toolconnection.ValidAuthorizationState(q.Get("state")) || !toolconnection.ValidCallback(q.Get("redirect_uri")) {
		t.Error("unbound authorization request")
		w.WriteHeader(400)
		return
	}
	u, _ := url.Parse(q.Get("redirect_uri"))
	callback := u.Query()
	callback.Set("state", q.Get("state"))
	callback.Set("iss", m.Provider.URL+"/issuer")
	if m.Mode.Load() == 3 {
		callback.Set("error", "access_denied")
	} else {
		code := "guided-code-" + uuid.NewString()
		m.mu.Lock()
		m.codes[code] = guidedCode{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
		m.mu.Unlock()
		callback.Set("code", code)
	}
	u.RawQuery = callback.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (m *GuidedFixture) BrowserLaunch(raw string) error {
	resp, err := m.Provider.Client().Get(raw)
	if err != nil {
		return errors.New("test browser request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.New("test browser callback failed")
	}
	return nil
}
