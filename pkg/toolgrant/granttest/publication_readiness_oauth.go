package granttest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func ConfigurePublicationReadinessOAuth(t *testing.T, p *PublicationFixture, policy api.MCPReadinessPolicy, mode string) *atomic.Int32 {
	t.Helper()
	exchanges := &atomic.Int32{}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil || r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "readiness-role-refresh" || r.Form.Get("client_id") != "readiness-client" {
			t.Error("role renewal forwarded unrelated authority or changed its provider request")
			w.WriteHeader(400)
			return
		}
		exchanges.Add(1)
		if mode == "lost" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"readiness-reader-renewed","refresh_token":"readiness-role-rotated","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(provider.Close)
	target, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := p.Runtime.Client()
	client.Transport = readinessProviderTransport{next: client.Transport, provider: provider.Client().Transport, target: target}
	views := p.RoleViews.Load().(map[string][]mcphosting.Tool)
	copyViews := map[string][]mcphosting.Tool{}
	for key, value := range views {
		copyViews[key] = value
	}
	copyViews["readiness-reader-renewed"] = views["readiness-reader-token"]
	p.RoleViews.Store(copyViews)
	cfg := api.MCPCatalogMonitorConfig{Scope: api.DefaultEnvScope, Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 300, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://readiness-provider.example/authorize", TokenEndpoint: "https://readiness-provider.example/token", ClientID: "readiness-client"}}}
	store := p.Base.Store
	if _, err := store.(state.MCPCatalogMonitorStore).ConfigureMCPCatalogMonitor(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, cfg); err != nil {
		t.Fatal(err)
	}
	observers := store.(state.MCPCatalogObserverStore)
	job, err := observers.ClaimMCPCatalogObserver(t.Context(), state.MCPCatalogObserverJob{AccountID: p.Base.Account.ID, AppID: p.Base.AppA.ID, Role: "reader", Snapshot: api.MCPCatalogMonitorSnapshot{Config: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secretbox.Seal(p.Identity.Recipient(), secretbox.Envelope{"ACCESS_TOKEN": "readiness-reader-token", "REFRESH_TOKEN": "readiness-role-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	if mode != "cached" {
		expires = now.Add(10 * time.Second)
	}
	source := sha256.Sum256([]byte("guided"))
	if err = observers.SaveMCPCatalogObserverOAuth(t.Context(), job, api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: hex.EncodeToString(source[:]), Ciphertext: cipher, ExpiresAt: &expires, LastRenewalAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err = store.(state.MCPReadinessOAuthStore).ReleaseMCPReadinessOAuth(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	policy.Roles[0].Monitor.SecretRef = ""
	policy.Roles[0].Monitor.OAuthConnection = &api.MCPReadinessOAuthConnection{Scope: api.DefaultEnvScope, Role: "reader", Revision: cfg.Revision}
	policy, err = store.(state.MCPReadinessStore).SetMCPReadinessPolicy(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.SetMCPReadinessPolicyRequest{ExpectedRevision: policy.Revision, Enabled: true, Endpoint: policy.Endpoint, MaxAgeSeconds: policy.MaxAgeSeconds, Roles: policy.Roles, Reason: "require the connected reader grant"})
	if err != nil {
		t.Fatal(err)
	}
	in := p.Publication.Intent
	in.Readiness = toolpublication.ReadinessReview(policy)
	newID := toolpublication.Digest([]byte(uuid.NewString()))
	record, err := p.Publisher.BeginToolPublication(t.Context(), newID, in)
	if err != nil {
		t.Fatal(err)
	}
	p.Publication = record.Record
	return exchanges
}

// Keep production's explicit HTTPS URL validation while routing the synthetic
// provider through its TLS test server.
type readinessProviderTransport struct {
	next     http.RoundTripper
	provider http.RoundTripper
	target   *url.URL
}

func (r readinessProviderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "readiness-provider.example" {
		return r.next.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	target := *req.URL
	target.Host = r.target.Host
	clone.URL = &target
	return r.provider.RoundTrip(clone)
}
