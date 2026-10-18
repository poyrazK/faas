package mcpcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOAuthRevocationMemory(t *testing.T) { testOAuthRevocation(t, state.NewMemStore(), false) }
func TestOAuthRevocationPostgres(t *testing.T) {
	testOAuthRevocation(t, state.NewPgStore(pgtest.OpenMigrated(t)), false)
}
func testOAuthRevocation(t *testing.T, store state.Store, success bool) {
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "revocation@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "revocation", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Enabled: true, Role: "reader", Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", RevocationEndpoint: "https://provider.example/revoke", ClientID: "reader", ClientSecretRef: "secret:CLIENT"}}}
	monitor := store.(state.MCPCatalogMonitorStore)
	flows := store.(state.MCPCatalogOAuthStore)
	jobs := store.(state.MCPCatalogOAuthRevocationStore)
	identity, _ := age.GenerateX25519Identity()
	clientSealed, _ := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"CLIENT": "PRIVATE_CLIENT"})
	if err = store.UpsertAppSecret(ctx, acct.ID, app.ID, "CLIENT", clientSealed); err != nil {
		t.Fatal(err)
	}
	if _, err = monitor.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	sealed, _ := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"ACCESS_TOKEN": "PRIVATE_ACCESS", "REFRESH_TOKEN": "PRIVATE_REFRESH"})
	flow := api.MCPCatalogOAuthFlow{StateSHA256: strings.Repeat("a", 64), Ciphertext: []byte("sealed"), ExpiresAt: time.Now().Add(time.Minute)}
	view, err := flows.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", cfg.Revision, "begin", "", &flow, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err = flows.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", view.Config.Revision, "consume", flow.StateSHA256, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	expiry := at.Add(time.Hour)
	status := api.MCPCatalogObserverOAuthStatus{State: "connected", Ciphertext: sealed, SourceSHA256: strings.Repeat("b", 64), ExpiresAt: &expiry, LastRenewalAt: &at}
	view, err = flows.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", view.Config.Revision, "finish", flow.StateSHA256, nil, &status)
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != cfg.Observer.OAuth.RevocationEndpoint {
			t.Error("wrong immutable endpoint")
		}
		_ = req.ParseForm()
		id, secret, basic := req.BasicAuth()
		if !basic || id != "reader" || secret != "PRIVATE_CLIENT" {
			t.Error("captured client credential missing")
		}
		if req.Form.Get("resource") != "" || req.Form.Get("grant_type") != "" {
			t.Error("revocation included grant parameters")
		}
		calls = append(calls, req.Form.Get("token_type_hint"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("PRIVATE_RESPONSE"))}, nil
	})}}
	out, err := runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "disconnect", Role: "reader", Revision: view.Config.Revision})
	if err != nil || out.ProviderRevocation != "pending" {
		t.Fatal(out, err)
	}
	if err = store.DeleteAppSecret(ctx, acct.ID, app.ID, "CLIENT"); err != nil {
		t.Fatal(err)
	}
	root, err := monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	if view.ObserverStatus.OAuth != nil || view.OAuthLifecycle.LocalState != "disconnected" {
		t.Fatal("local grant survived disconnect")
	}
	if _, err = flows.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", view.Config.Revision, "begin", "", &flow, nil); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("reconnect raced pending revocation", err)
	}
	if success {
		changed := view.Config
		changed.Revision = uuid.NewString()
		observer := *changed.Observer
		observer.Enabled = false
		changed.Observer = &observer
		changed.Enabled = false
		if _, err = monitor.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, changed); err != nil {
			t.Fatal(err)
		}
		if err = runner.TickRevocations(ctx); err != nil {
			t.Fatal(err)
		}
		root, err = monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
		job := view.OAuthLifecycle.Revocation
		if job.State != "provider_acknowledged" || job.Ciphertext != nil || job.Config != nil || job.ClientCiphertext != nil || len(calls) != 2 || calls[0] != "refresh_token" || calls[1] != "access_token" {
			t.Fatal("worker failed to retire both tokens", job, calls)
		}
		if err = runner.TickRevocations(ctx); err != nil || len(calls) != 2 {
			t.Fatal("terminal job replayed", err)
		}
		if _, err = runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "disconnect", Role: "reader", Revision: view.Config.Revision}); err != nil {
			t.Fatal(err)
		}
		if err = runner.TickRevocations(ctx); err != nil || len(calls) != 2 {
			t.Fatal("repeat disconnect replayed", err)
		}
		return
	}

	due, err := jobs.ListDueMCPCatalogOAuthRevocations(ctx, 4)
	if err != nil || len(due) != 1 {
		t.Fatal(due, err)
	}
	first, err := jobs.ChangeMCPCatalogOAuthRevocation(ctx, due[0], "claim")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.ChangeMCPCatalogOAuthRevocation(ctx, due[0], "claim"); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("duplicate claim", err)
	}
	// Acknowledgement remains valid through pause and endpoint changes, using the frozen binding.
	changed := view.Config
	changed.Revision = uuid.NewString()
	observer := *changed.Observer
	oauth := *observer.OAuth
	oauth.RevocationEndpoint = "https://other.example/revoke"
	observer.OAuth = &oauth
	observer.Paused = true
	changed.Observer = &observer
	if _, err = monitor.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, changed); err != nil {
		t.Fatal(err)
	}
	// Release through durable failure; next attempt stays queued and sealed.
	if _, err = jobs.ChangeMCPCatalogOAuthRevocation(ctx, first, "provider_unavailable"); err != nil {
		t.Fatal(err)
	}
	root, _ = monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	pending := view.OAuthLifecycle.Revocation
	if pending.ClientCiphertext != nil || pending.Config != nil || pending.State != "pending" || pending.Attempts != 1 || !pending.NextAttemptAt.After(time.Now()) {
		t.Fatal("retry lost durable intent")
	}
	encoded, _ := json.Marshal(api.CatalogMonitorSummary(root, app.Slug, time.Now()))
	for _, private := range []string{"private_client_ciphertext", "private_ciphertext", "private_config", "private_lease", "PRIVATE_ACCESS", "PRIVATE_REFRESH"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("public summary disclosed private data", private)
		}
	}
	if len(calls) != 0 {
		t.Fatal("unexpected requests")
	}
}

func TestOAuthRevocationWorkerMemory(t *testing.T) { testOAuthRevocation(t, state.NewMemStore(), true) }
func TestOAuthRevocationWorkerPostgres(t *testing.T) {
	testOAuthRevocation(t, state.NewPgStore(pgtest.OpenMigrated(t)), true)
}
func TestOAuthRevocationAuthenticationAndRedirectRefusal(t *testing.T) {
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			cfg := &api.MCPCatalogObserverOAuthConfig{RevocationEndpoint: "https://provider.example/revoke", RevocationAuthMethod: method, ClientID: "client", ClientSecretRef: "secret:CLIENT"}
			runner := ObserverRunner{Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
				_ = req.ParseForm()
				if req.Form.Get("token") != "PRIVATE_TOKEN" || req.Form.Get("token_type_hint") != "refresh_token" {
					t.Error("invalid RFC 7009 form")
				}
				id, secret, basic := req.BasicAuth()
				if method == "client_secret_basic" && (!basic || id != "client" || secret != "PRIVATE_CLIENT") {
					t.Error("missing basic authentication")
				}
				if method == "client_secret_post" && (basic || req.Form.Get("client_secret") != "PRIVATE_CLIENT") {
					t.Error("missing post authentication")
				}
				if method == "none" && (basic || req.Form.Get("client_secret") != "") {
					t.Error("unexpected client secret")
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://leak.example"}}, Body: io.NopCloser(strings.NewReader("PRIVATE_ERROR"))}, nil
			})}}
			err := runner.revokeToken(t.Context(), cfg, "PRIVATE_TOKEN", "refresh_token", "PRIVATE_CLIENT")
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("redirect acknowledged or private error exposed", err)
			}
		})
	}
}

func TestOAuthRevocationWorkerShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() { RunManagedObservers(ctx, state.NewMemStore(), "apps.example", nil, nil); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("managed OAuth worker did not stop on cancellation")
	}
}
