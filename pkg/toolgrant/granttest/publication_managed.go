package granttest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type ManagedPublicationFixture struct {
	Publication     *PublicationFixture
	Connection      api.ToolOAuthConnectionRecord
	Provider        *httptest.Server
	AccountIdentity *age.X25519Identity
	Exchanges       atomic.Int32
	Mode            atomic.Int32 // 1: lost response, 2: changed scopes, 3: invalid grant, 4: blocked provider
	ExpiresIn       atomic.Int64
	Started         chan struct{}
	Release         chan struct{}
	mu              sync.Mutex
	refresh         string
}

func NewManagedPublication(t *testing.T, store state.Store) *ManagedPublicationFixture {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	m := &ManagedPublicationFixture{AccountIdentity: identity, Started: make(chan struct{}), Release: make(chan struct{}), refresh: "publication-refresh-initial"}
	m.ExpiresIn.Store(3600)
	m.Provider = httptest.NewTLSServer(http.HandlerFunc(m.serveProvider))
	t.Cleanup(m.Provider.Close)
	m.Publication = NewPublication(t, store, func(opts *toolregistry.PrivateExecutionOptions) {
		opts.ConnectionIdentities = func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }
		opts.ConnectionRecipients = func() ([]*age.X25519Recipient, error) { return []*age.X25519Recipient{identity.Recipient()}, nil }
	})
	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		select {
		case <-m.Release:
		default:
			close(m.Release)
		}
	})
	p := m.Publication
	cipher, err := secretbox.SealOne(p.Identity.Recipient(), "MCP_PUBLICATION_REFRESH", m.refresh, api.MCPCatalogOAuthMaxTokenBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertAppSecretWithKidInScope(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.DefaultEnvScope, "MCP_PUBLICATION_REFRESH", p.Identity.Recipient().String(), cipher); err != nil {
		t.Fatal(err)
	}
	auth := p.Publication.Intent.Auth
	m.Connection, err = p.Owner.PutToolOAuthConnection(t.Context(), uuid.NewString(), api.ToolOAuthConnectionIntent{App: p.Base.AppA.Slug, Scope: api.DefaultEnvScope, Issuer: auth.Issuer, TokenEndpoint: m.Provider.URL + "/token", ClientID: "publication-client", Resource: auth.Resource, Scopes: auth.Scopes, RefreshTokenSecretRef: "secret:MCP_PUBLICATION_REFRESH", TokenAuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *ManagedPublicationFixture) Approve(t *testing.T) api.ToolPublicationVerification {
	t.Helper()
	p := m.Publication
	v, err := p.Publisher.VerifyToolPublication(t.Context(), p.Publication.ID, p.Request)
	if err != nil {
		t.Fatal(err)
	}
	v, err = p.Owner.DecideToolPublicationVerification(t.Context(), p.Publication.ID, api.ToolPublicationVerificationDecision{RequestSHA256: v.RequestSHA256, CredentialConnectionID: m.Connection.ID, ApproveReadiness: p.Publication.Intent.Readiness != nil})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (m *ManagedPublicationFixture) serveProvider(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil || r.Method != http.MethodPost || r.URL.Path != "/token" || r.Header.Get("Authorization") != "" || r.Form.Get("client_id") != "publication-client" || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("resource") != "https://private.example/mcp" || r.Form.Get("scope") != "tool:read" {
		w.WriteHeader(http.StatusBadRequest)
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
	valid := r.Form.Get("refresh_token") == m.refresh
	m.refresh = fmt.Sprintf("publication-refresh-%d", n)
	m.mu.Unlock()
	if !valid || m.Mode.Load() == 3 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"private provider details"}`))
		return
	}
	access := fmt.Sprintf("publication-access-%d", n)
	m.Publication.Credential.Store(access)
	if m.Mode.Load() == 1 {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	scope := "tool:read"
	if m.Mode.Load() == 2 {
		scope += " tool:write"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": fmt.Sprintf("publication-refresh-%d", n), "token_type": "Bearer", "scope": scope, "expires_in": m.ExpiresIn.Load()})
}
