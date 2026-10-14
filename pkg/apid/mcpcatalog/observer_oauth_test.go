package mcpcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type oauthRoundTripper func(*http.Request) (*http.Response, error)

func (f oauthRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestObserverOAuthMemory(t *testing.T) { testObserverOAuth(t, state.NewMemStore()) }
func TestObserverOAuthPostgres(t *testing.T) {
	testObserverOAuth(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testObserverOAuth(t *testing.T, store state.Store) {
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "oauth-observer@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "oauth-observer", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	put := func(token string) {
		t.Helper()
		blob, e := secretbox.Seal(id.Recipient(), secretbox.Envelope{"REFRESH": token})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.UpsertAppSecret(ctx, acct.ID, app.ID, "REFRESH", blob); e != nil {
			t.Fatal(e)
		}
	}
	put("INITIAL_PRIVATE_REFRESH")
	monitors := store.(state.MCPCatalogMonitorStore)
	observers := store.(state.MCPCatalogObserverStore)
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader-client", RefreshTokenSecretRef: "secret:REFRESH"}}}
	configure := func() {
		t.Helper()
		cfg.Revision = uuid.NewString()
		if _, e := monitors.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); e != nil {
			t.Fatal(e)
		}
	}
	configure()
	other := cfg
	other.Role = "writer"
	other.Revision = uuid.NewString()
	if _, e := monitors.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, other); !errors.Is(e, state.ErrInvalidArgument) {
		t.Fatal("shared refresh grant reference accepted")
	}
	jobs, e := observers.ListDueMCPCatalogObservers(ctx, 4)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %v %v", jobs, e)
	}
	job, e := observers.ClaimMCPCatalogObserver(ctx, jobs[0])
	if e != nil {
		t.Fatal(e)
	}
	reload := func() {
		t.Helper()
		root, e := monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		job.Snapshot, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	}
	calls := 0
	revoked := false
	fence := false
	runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{id} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != cfg.Observer.OAuth.TokenEndpoint {
			t.Fatal("wrong endpoint")
		}
		if e := req.ParseForm(); e != nil {
			t.Fatal(e)
		}
		expected := "INITIAL_PRIVATE_REFRESH"
		if calls == 2 || calls == 3 || calls == 5 {
			expected = "ROTATED_PRIVATE_REFRESH"
		}
		if calls == 4 {
			expected = "NEW_PRIVATE_REFRESH"
		}
		if req.Form.Get("grant_type") != "refresh_token" || req.Form.Get("refresh_token") != expected || req.Form.Get("client_id") != "reader-client" {
			t.Fatalf("wrong refresh grant at call %d", calls)
		}
		if fence {
			configure()
		}
		code := 200
		body := `{"access_token":"PRIVATE_ACCESS","refresh_token":"ROTATED_PRIVATE_REFRESH","token_type":"Bearer","expires_in":3600}`
		if revoked {
			code = 400
			body = `{"error":"invalid_grant","error_description":"PRIVATE_PROVIDER_ERROR"}`
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	token, e := runner.token(ctx, job)
	if e != nil || token != "PRIVATE_ACCESS" || calls != 1 {
		t.Fatalf("initial renewal: %v calls=%d", e, calls)
	}
	reload()
	exposed := api.CatalogObserverStatus(job.Snapshot, time.Now())
	public, _ := json.Marshal(exposed)
	if strings.Contains(string(public), "ciphertext") || strings.Contains(string(public), "source_sha256") || strings.Contains(string(public), "PRIVATE") {
		t.Fatal("private state leaked")
	}
	if len(job.Snapshot.ObserverStatus.OAuth.Ciphertext) == 0 {
		t.Fatal("projection mutated storage")
	}
	if _, e = runner.token(ctx, job); e != nil || calls != 1 {
		t.Fatal("fresh token was refreshed")
	}
	expire := func() {
		t.Helper()
		reload()
		status := *job.Snapshot.ObserverStatus.OAuth
		soon := time.Now().Add(30 * time.Second)
		status.ExpiresAt = &soon
		if e := observers.SaveMCPCatalogObserverOAuth(ctx, job, status); e != nil {
			t.Fatal(e)
		}
		reload()
	}
	current, _ := age.GenerateX25519Identity()
	runner.Identities = func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{current, id} }
	expire()
	if _, e = runner.token(ctx, job); e != nil || calls != 2 {
		t.Fatal("rotated refresh failed", e)
	}
	reload()
	if _, e := secretbox.Open(current, job.Snapshot.ObserverStatus.OAuth.Ciphertext); e != nil {
		t.Fatal("renewal did not seal under current host key", e)
	}
	expire()
	revoked = true
	if _, e = runner.token(ctx, job); e == nil {
		t.Fatal("revoked token accepted")
	}
	reload()
	if job.Snapshot.ObserverStatus.OAuth.State != "reauthorization_required" {
		t.Fatal("revocation not persisted")
	}
	if _, e = runner.token(ctx, job); e == nil || calls != 3 {
		t.Fatal("revoked grant retried")
	}
	put("NEW_PRIVATE_REFRESH")
	jobs, e = observers.ListDueMCPCatalogObservers(ctx, 4)
	if e != nil || len(jobs) != 1 {
		t.Fatal("changed credential did not reschedule observer", e)
	}
	job, e = observers.ClaimMCPCatalogObserver(ctx, jobs[0])
	if e != nil {
		t.Fatal(e)
	}
	revoked = false
	if _, e = runner.token(ctx, job); e != nil || calls != 4 {
		t.Fatal("reauthorization failed", e)
	}
	reload()
	// Lifecycle changes retire old jobs but preserve rotated tokens for the same connection.
	configure()
	root, _ := monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if view.ObserverStatus == nil || view.ObserverStatus.OAuth == nil || view.ObserverStatus.LeaseID != "" {
		t.Fatal("connection lost on restart")
	}
	if e = observers.SaveMCPCatalogObserverOAuth(ctx, job, *job.Snapshot.ObserverStatus.OAuth); !errors.Is(e, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("old revision wrote tokens")
	}
	jobs, _ = observers.ListDueMCPCatalogObservers(ctx, 4)
	job, e = observers.ClaimMCPCatalogObserver(ctx, jobs[0])
	if e != nil {
		t.Fatal(e)
	}
	expire()
	fence = true
	if _, e = runner.token(ctx, job); !errors.Is(e, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("in-flight reconfiguration did not fence refresh", e)
	}
}

func TestOAuthRefreshRejectsUnsafeResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"redirect", `{}`, 302}, {"missing_expiry", `{"access_token":"secret","token_type":"Bearer"}`, 200},
		{"wrong_type", `{"access_token":"secret","token_type":"MAC","expires_in":3600}`, 200},
		{"short_lifetime", `{"access_token":"secret","token_type":"Bearer","expires_in":30}`, 200},
		{"oversized", strings.Repeat("X", api.MCPCatalogOAuthMaxResponseBytes+1), 200},
		{"private_error", `{"error":"invalid_client","error_description":"PRIVATE"}`, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ObserverRunner{Client: &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			_, _, e := r.refreshOAuth(t.Context(), &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "public"}, "refresh", "")
			if e == nil || strings.Contains(e.Error(), "PRIVATE") {
				t.Fatal("unsafe response accepted or exposed")
			}
		})
	}
}

func TestOAuthRefreshConfidentialClient(t *testing.T) {
	runner := ObserverRunner{Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		user, password, ok := req.BasicAuth()
		if !ok || user != "client%3Aid" || password != "secret%3Avalue" {
			t.Fatal("client_secret_basic encoding incorrect")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"ACCESS","token_type":"Bearer","expires_in":3600}`))}, nil
	})}}
	result, revoked, err := runner.refreshOAuth(t.Context(), &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "client:id", ClientSecretRef: "secret:CLIENT_SECRET"}, "REFRESH", "secret:value")
	if err != nil || revoked || result.RefreshToken != "" {
		t.Fatal("confidential refresh failed")
	}
}

func TestOAuthRevocationReportsUnavailable(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	acct, _ := store.CreateAccount(ctx, "revoked@example.com", api.PlanHobby)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "revoked-observer", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	blob, _ := secretbox.Seal(id.Recipient(), secretbox.Envelope{"REFRESH": "PRIVATE_REFRESH"})
	if err = store.UpsertAppSecret(ctx, acct.ID, app.ID, "REFRESH", blob); err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	if _, err = store.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	runner := ObserverRunner{Store: store, Domain: "apps.example", Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{id} }, Client: &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"PRIVATE_PROVIDER_ERROR"}`))}, nil
	})}, Probe: func(context.Context, string, string, string) ([]byte, error) {
		t.Fatal("revoked role probed anonymously")
		return nil, nil
	}}
	if err = runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := store.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if view.Latest == nil || view.Latest.State != "unavailable" || view.ObserverStatus.FailureReason != "credential_unavailable" || view.ObserverStatus.OAuth.State != "reauthorization_required" {
		t.Fatal("revocation did not mark role unavailable")
	}
	view.ObserverStatus = api.CatalogObserverStatus(view, time.Now())
	public, _ := json.Marshal(view)
	if strings.Contains(string(public), "PRIVATE") || strings.Contains(string(public), "ciphertext") {
		t.Fatal("credential or provider error exposed")
	}
}
