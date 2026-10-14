package mcpcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGuidedOAuthMemory(t *testing.T) { testGuidedOAuth(t, state.NewMemStore()) }
func TestGuidedOAuthPostgres(t *testing.T) {
	testGuidedOAuth(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testGuidedOAuth(t *testing.T, store state.Store) {
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "guided@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "guided-oauth", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	config := api.MCPCatalogMonitorConfig{Enabled: true, Role: "reader", Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Profile: "google", Resource: "https://guided-oauth.apps.example/mcp", CallbackPort: 34567, Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader", Scopes: []string{"read"}}}}
	monitor := store.(state.MCPCatalogMonitorStore)
	flows := store.(state.MCPCatalogOAuthStore)
	if _, err = monitor.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, config); err != nil {
		t.Fatal(err)
	}
	identity, _ := age.GenerateX25519Identity()
	exchanges := 0
	challenge := ""
	reconfigure := false
	runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		exchanges++
		if e := req.ParseForm(); e != nil {
			t.Fatal(e)
		}
		if req.Form.Get("resource") != config.Observer.OAuth.Resource {
			t.Fatal("resource indicator missing from token request")
		}
		if req.Form.Get("grant_type") == "refresh_token" {
			if req.Form.Get("refresh_token") != "PRIVATE_REFRESH" {
				t.Fatal("guided renewal lost sealed refresh token")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"PRIVATE_RENEWED_ACCESS","token_type":"Bearer","expires_in":3600,"scope":"read"}`))}, nil
		}
		sum := sha256.Sum256([]byte(req.Form.Get("code_verifier")))
		if req.Form.Get("grant_type") != "authorization_code" || req.Form.Get("code") != "PRIVATE_CODE" || req.Form.Get("redirect_uri") != "http://127.0.0.1:34567/callback" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			t.Fatal("PKCE grant was not bound to the browser request")
		}
		if reconfigure {
			config.Revision = uuid.NewString()
			if _, e := monitor.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, config); e != nil {
				t.Fatal(e)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"PRIVATE_ACCESS","refresh_token":"PRIVATE_REFRESH","token_type":"Bearer","expires_in":3600,"scope":"read"}`))}, nil
	})}}
	begin := func(revision string) api.MCPCatalogOAuthResponse {
		t.Helper()
		out, e := runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: revision, RedirectURI: "http://127.0.0.1:34567/callback"})
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(out.AuthorizationURL)
		challenge = u.Query().Get("code_challenge")
		if u.Query().Get("resource") != config.Observer.OAuth.Resource || u.Query().Get("access_type") != "offline" || u.Query().Get("prompt") != "consent" {
			t.Fatal("Google consent or resource indicator missing")
		}
		if u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("scope") != "read" || u.Query().Get("state") != out.CallbackState {
			t.Fatal("invalid authorization URL")
		}
		return out
	}
	if _, e := runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: config.Revision, RedirectURI: "http://127.0.0.1:34568/callback"}); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("configured callback port bypassed")
	}
	begun := begin(config.Revision)
	request := api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Code: "PRIVATE_CODE"}
	wrong := request
	wrong.State = strings.Repeat("X", 43)
	if _, e := runner.connectOAuth(ctx, acct.ID, app.ID, wrong); !errors.Is(e, state.ErrMCPCatalogMonitorChanged) || exchanges != 0 {
		t.Fatal("wrong state reached token endpoint")
	}
	wrong = request
	wrong.Role = "writer"
	if _, e := runner.connectOAuth(ctx, acct.ID, app.ID, wrong); e == nil || exchanges != 0 {
		t.Fatal("wrong role consumed state")
	}
	wrong = request
	if _, e := runner.connectOAuth(ctx, uuid.NewString(), app.ID, wrong); e == nil || exchanges != 0 {
		t.Fatal("cross-account callback accepted")
	}
	connected, e := runner.connectOAuth(ctx, acct.ID, app.ID, request)
	if e != nil || connected.State != "connected" {
		t.Fatal("authorization failed", e)
	}
	if _, e = runner.connectOAuth(ctx, acct.ID, app.ID, request); e == nil || exchanges != 1 {
		t.Fatal("replayed state exchanged twice")
	}
	root, _ := monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if len(view.ObserverStatus.OAuth.GrantedScopes) != 1 || view.ObserverStatus.OAuthFlow != nil {
		t.Fatal("connection state incomplete")
	}
	public, _ := json.Marshal(api.CatalogObserverStatus(view, time.Now()))
	if strings.Contains(string(public), "ciphertext") || strings.Contains(string(public), "oauth_flow") || strings.Contains(string(public), "PRIVATE") {
		t.Fatal("credentials leaked")
	}
	job := state.MCPCatalogObserverJob{AccountID: acct.ID, AppID: app.ID, Role: "reader", Snapshot: view}
	token, e := runner.token(ctx, job)
	if e != nil || token != "PRIVATE_ACCESS" || exchanges != 1 {
		t.Fatal("guided connection did not supply cached token", e)
	}
	jobs, e := store.(state.MCPCatalogObserverStore).ListDueMCPCatalogObservers(ctx, 4)
	if e != nil || len(jobs) != 1 {
		t.Fatal("connected role not due", e)
	}
	claimed, e := store.(state.MCPCatalogObserverStore).ClaimMCPCatalogObserver(ctx, jobs[0])
	if e != nil {
		t.Fatal(e)
	}
	soon := time.Now().Add(30 * time.Second)
	status := *claimed.Snapshot.ObserverStatus.OAuth
	status.ExpiresAt = &soon
	if e = store.(state.MCPCatalogObserverStore).SaveMCPCatalogObserverOAuth(ctx, claimed, status); e != nil {
		t.Fatal(e)
	}
	root, _ = monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	claimed.Snapshot, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	if token, e = runner.token(ctx, claimed); e != nil || token != "PRIVATE_RENEWED_ACCESS" || exchanges != 2 {
		t.Fatal("guided connection did not renew without an initial secret", e)
	}
	disconnected, e := runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "disconnect", Role: "reader", Revision: connected.Revision})
	if e != nil {
		t.Fatal(e)
	}
	root, _ = monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	job.Snapshot = view
	if _, e = runner.token(ctx, job); e == nil {
		t.Fatal("disconnected role used a token")
	}
	begun = begin(disconnected.Revision)
	request.Revision = begun.Revision
	request.State = begun.CallbackState
	reconfigure = true
	if _, e = runner.connectOAuth(ctx, acct.ID, app.ID, request); !errors.Is(e, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("in-flight grant survived reconfiguration", e)
	}
	// A denied grant is consumed even though no token exchange takes place.
	root, _ = monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	begun = begin(view.Config.Revision)
	denied := api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Denied: true}
	before := exchanges
	if _, e = runner.connectOAuth(ctx, acct.ID, app.ID, denied); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("denied callback accepted")
	}
	if _, e = runner.connectOAuth(ctx, acct.ID, app.ID, denied); !errors.Is(e, state.ErrMCPCatalogMonitorChanged) || exchanges != before {
		t.Fatal("consumed denial replayed")
	}
	// Expired and consumed grants fail before any provider request.
	root, _ = monitor.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	expired := api.MCPCatalogOAuthFlow{StateSHA256: digest([]byte(strings.Repeat("Y", 43))), Ciphertext: []byte("sealed"), ExpiresAt: time.Now().Add(-time.Second)}
	if _, e = flows.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", view.Config.Revision, "begin", "", &expired, nil); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("expired flow admitted")
	}
}
func TestGuidedOAuthRedirectAndScopes(t *testing.T) {
	for _, bad := range []string{"https://evil.example/callback", "http://localhost:123/callback", "http://127.0.0.1:0/callback", "http://127.0.0.1:abc/callback", "http://127.0.0.1:65536/callback", "http://127.0.0.1:123/callback?x=1", "http://user@127.0.0.1:123/callback"} {
		if oauthLoopbackRedirect(bad) {
			t.Fatal("unsafe redirect accepted", bad)
		}
	}
	if _, e := grantedOAuthScopes([]string{"read"}, "read admin"); e == nil {
		t.Fatal("broader grants accepted")
	}
}

func TestGuidedOAuthIncompatibleMetadataDoesNotCreateFlow(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, e := store.CreateAccount(ctx, "preflight@example.com", api.PlanHobby)
	if e != nil {
		t.Fatal(e)
	}
	app, e := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "preflight-oauth", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if e != nil {
		t.Fatal(e)
	}
	cfg := api.MCPCatalogMonitorConfig{Enabled: true, Role: "reader", Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", Issuer: "https://provider.example", DiscoveryURL: "https://provider.example/metadata", ClientID: "reader"}}}
	if _, e = store.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); e != nil {
		t.Fatal(e)
	}
	id, _ := age.GenerateX25519Identity()
	calls := 0
	runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{id} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" || req.Header.Get("Authorization") != "" {
			t.Fatal("preflight sent credentials or attempted token exchange")
		}
		body := `{"issuer":"https://provider.example","authorization_endpoint":"https://provider.example/authorize","token_endpoint":"https://provider.example/token","code_challenge_methods_supported":["plain"],"token_endpoint_auth_methods_supported":["none"],"grant_types_supported":["authorization_code","refresh_token"],"response_types_supported":["code"]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	out, e := runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: cfg.Revision, RedirectURI: "http://127.0.0.1:34567/callback"})
	if e != nil || out.State != "unsupported" || out.AuthorizationURL != "" || out.CallbackState != "" || calls != 1 {
		t.Fatal("incompatible provider created consent flow", e)
	}
	root, e := store.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if view.Config.Revision != cfg.Revision || view.ObserverStatus != nil {
		t.Fatal("preflight mutated role intent")
	}
	out, e = runner.connectOAuth(ctx, acct.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "check", Role: "reader", Revision: cfg.Revision})
	if e != nil || out.State != "unsupported" || calls != 2 {
		t.Fatal("explicit provider check failed", e)
	}
}
