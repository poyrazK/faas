package granttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// ManagedFixture uses independent TLS provider and MCP endpoints. Modes are
// success, lost response after consumption, scope escalation, invalid_grant,
// and blocking exchange. It never contacts an external provider.
type ManagedFixture struct {
	Private         *PrivateFixture
	Connection      api.ToolOAuthConnectionRecord
	Grant           api.ToolGrantRecord
	Provider        *httptest.Server
	AccountIdentity *age.X25519Identity
	BasicSecret     atomic.Value
	Exchanges       atomic.Int32
	Mode            atomic.Int32
	ExpiresIn       atomic.Int64
	Started         chan struct{}
	Release         chan struct{}
	mu              sync.Mutex
	nextRefresh     string
}

func NewManaged(t *testing.T, store state.Store, agentToken string) *ManagedFixture {
	t.Helper()
	p := NewPrivate(t, store, agentToken)
	f := p.Base
	m := &ManagedFixture{Private: p, Started: make(chan struct{}), Release: make(chan struct{}), nextRefresh: "provider-exec-refresh-initial"}
	m.ExpiresIn.Store(3600)
	m.BasicSecret.Store("")
	accountIdentity, keyErr := age.GenerateX25519Identity()
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	m.AccountIdentity = accountIdentity
	p.Runtime.Reflections.Store([]string{"provider-exec-refresh-initial"})
	m.Provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parseErr := r.ParseForm()
		authOK := r.Header.Get("Authorization") == "" && r.Form.Get("client_id") == "execution-client"
		if secret := m.BasicSecret.Load().(string); secret != "" {
			user, password, ok := r.BasicAuth()
			u, ue := url.QueryUnescape(user)
			p, pe := url.QueryUnescape(password)
			authOK = ok && ue == nil && pe == nil && u == "execution-client" && p == secret && r.Form.Get("client_id") == ""
		}
		if r.Method != http.MethodPost || r.URL.Path != "/token" || !authOK || parseErr != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("resource") != "https://tool.example/mcp" || r.Form.Get("scope") != "tool:read" {
			t.Error("execution refresh escaped its exact provider request or forwarded native authority")
			w.WriteHeader(400)
			return
		}
		n := m.Exchanges.Add(1)
		if n == 1 {
			close(m.Started)
		}
		if m.Mode.Load() == 4 {
			select {
			case <-m.Release:
			case <-r.Context().Done():
				return
			}
		}
		m.mu.Lock()
		valid := r.Form.Get("refresh_token") == m.nextRefresh
		m.nextRefresh = "provider-exec-refresh-" + strconv.Itoa(int(n))
		m.mu.Unlock()
		if !valid || m.Mode.Load() == 3 {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"must never be surfaced"}`))
			return
		}
		access := "provider-exec-access-" + strconv.Itoa(int(n))
		p.Runtime.Credential.Store(access)
		if m.Mode.Load() == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		scopes := "tool:read"
		if m.Mode.Load() == 2 {
			scopes = "tool:read tool:write"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "provider-exec-refresh-" + strconv.Itoa(int(n)), "token_type": "Bearer", "expires_in": m.ExpiresIn.Load(), "scope": scopes})
	}))
	t.Cleanup(m.Provider.Close)
	SealManagedSource(t, m, "MCP_EXECUTION_REFRESH", "provider-exec-refresh-initial")
	if _, err := f.Owner.RevokeToolGrant(t.Context(), p.Grant.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	m.Connection, err = f.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), api.ToolOAuthConnectionIntent{App: f.AppA.Slug, Scope: api.DefaultEnvScope, Issuer: m.Provider.URL + "/issuer", TokenEndpoint: m.Provider.URL + "/token", ClientID: "execution-client", Resource: "https://tool.example/mcp", Scopes: []string{"tool:read"}, RefreshTokenSecretRef: "secret:MCP_EXECUTION_REFRESH", TokenAuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	m.Grant, err = f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour), CredentialConnectionID: m.Connection.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.RestartExecutor(t)
	return m
}

func SealManagedSource(t *testing.T, m *ManagedFixture, key, value string) {
	t.Helper()
	p, f := m.Private, m.Private.Base
	sealed, err := secretbox.SealOne(p.Identity.Recipient(), key, value, api.MCPCatalogOAuthMaxTokenBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Store.UpsertAppSecretWithKidInScope(t.Context(), f.Account.ID, f.AppA.ID, api.DefaultEnvScope, key, p.Identity.Recipient().String(), sealed); err != nil {
		t.Fatal(err)
	}
}

func (m *ManagedFixture) RestartExecutor(t *testing.T) {
	t.Helper()
	p, f := m.Private, m.Private.Base
	f.PrivateExecutor.Close()
	f.PrivateExecutor = toolregistry.NewPrivateExecutor(f.Store, toolregistry.PrivateExecutionOptions{Enabled: true, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{p.Identity} }, ConnectionIdentities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{m.AccountIdentity} }, ConnectionRecipients: func() ([]*age.X25519Recipient, error) {
		return []*age.X25519Recipient{m.AccountIdentity.Recipient()}, nil
	}, Endpoint: func(context.Context, state.App, state.Deployment) (string, error) { return f.PreviewURL, nil }, VerifyPin: func(context.Context, api.ToolInvocationTarget, string) error { return nil }, HTTP: p.Runtime.Server.Client()})
	t.Cleanup(f.PrivateExecutor.Close)
}

func (m *ManagedFixture) Request(raw string) api.PrivateToolInvocationRequest {
	return api.PrivateToolInvocationRequest{GrantID: m.Grant.ID, ArgumentsJSON: raw, Intent: api.ToolInvocationIntent{Version: m.Private.Base.PublicationA.ID, Name: "visible", AccessMode: "external-oauth", ArgumentsSHA256: toolpublication.Digest([]byte(raw)), CredentialBindingSHA256: m.Grant.Credential.SHA256, TimeoutSeconds: 15, AllowToolExecution: true}}
}
