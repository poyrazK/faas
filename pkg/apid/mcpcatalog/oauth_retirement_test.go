package mcpcatalog

import (
	"context"
	"encoding/json"
	"filippo.io/age"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"testing"
)

type retirementFixture struct {
	store    state.Store
	runner   ObserverRunner
	acct     state.Account
	app      state.App
	identity *age.X25519Identity
	tokens   int
	revoked  []string
	mutate   func()
}

func newRetirementFixture(t *testing.T, store state.Store) *retirementFixture {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "durable-retirement@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "durable-retirement", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := age.GenerateX25519Identity()
	f := &retirementFixture{store: store, acct: acct, app: app, identity: identity}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, Endpoint: "/mcp", IntervalSeconds: 30, TimeoutSeconds: 1, OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", RevocationEndpoint: "https://provider.example/revoke", ClientID: "reader", Scopes: []string{"read"}}}}
	if _, err = store.(state.MCPCatalogMonitorStore).ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	f.runner = ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		_ = req.ParseForm()
		if req.URL.Path == "/revoke" {
			f.revoked = append(f.revoked, req.Form.Get("token"))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		f.tokens++
		if f.mutate != nil {
			f.mutate()
		}
		body := fmt.Sprintf(`{"access_token":"ACCESS_%d","refresh_token":"REFRESH_%d","token_type":"Bearer","expires_in":3600,"scope":"read"}`, f.tokens, f.tokens)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	return f
}
func (f *retirementFixture) view(t *testing.T) api.MCPCatalogMonitorSnapshot {
	t.Helper()
	root, err := f.store.(state.MCPCatalogMonitorStore).GetMCPCatalogMonitor(t.Context(), f.acct.ID, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	return view
}
func (f *retirementFixture) connect(t *testing.T) error {
	t.Helper()
	view := f.view(t)
	begun, err := f.runner.connectOAuth(t.Context(), f.acct.ID, f.app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: view.Config.Revision, RedirectURI: "http://127.0.0.1:34567/callback"})
	if err != nil {
		return err
	}
	_, err = f.runner.connectOAuth(t.Context(), f.acct.ID, f.app.ID, api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Code: "PRIVATE_CODE"})
	return err
}
func (f *retirementFixture) history(t *testing.T) []api.MCPOAuthRetirement {
	t.Helper()
	out, err := f.store.(state.MCPOAuthRetirementStore).ListMCPOAuthRetirements(t.Context(), f.acct.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "private_") || strings.Contains(string(encoded), "PRIVATE_CODE") {
		t.Fatal("public history disclosed private data")
	}
	return out.Items
}
func TestDurableRetirementMemory(t *testing.T) { testDurableRetirement(t, state.NewMemStore()) }
func TestDurableRetirementPostgres(t *testing.T) {
	testDurableRetirement(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testDurableRetirement(t *testing.T, store state.Store) {
	f := newRetirementFixture(t, store)
	if err := f.connect(t); err != nil {
		t.Fatal(err)
	}
	if err := f.connect(t); err != nil {
		t.Fatal(err)
	}
	if err := f.runner.TickRevocations(t.Context()); err != nil {
		t.Fatal(err)
	}
	held := ""
	for _, r := range f.history(t) {
		if r.State == "held" {
			held = r.ID
		}
	}
	if held == "" || len(f.revoked) != 0 {
		t.Fatal("replacement retirement was not held")
	}
	view := f.view(t)
	envelope, err := secretbox.Open(f.identity, view.ObserverStatus.OAuth.Ciphertext)
	if err != nil || envelope["ACCESS_TOKEN"] != "ACCESS_2" {
		t.Fatal("new grant did not replace old atomically", err)
	}
	if _, err = store.(state.MCPOAuthRetirementStore).RecoverMCPOAuthRetirement(t.Context(), f.acct.ID, held, api.MCPOAuthRetirementAction{Action: "retry", AllowActiveGrantRevocation: true}); err != nil {
		t.Fatal(err)
	}
	if err = f.runner.TickRevocations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 2 || f.revoked[0] != "REFRESH_1" || f.revoked[1] != "ACCESS_1" {
		t.Fatal("explicit recovery did not retire old grant", f.revoked)
	}
	if err = store.DeleteApp(t.Context(), f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.runner.TickRevocations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 4 || f.revoked[2] != "REFRESH_2" || f.revoked[3] != "ACCESS_2" {
		t.Fatal("deleted app lost grant retirement", f.revoked)
	}
	for _, r := range f.history(t) {
		if r.State != "provider_acknowledged" && r.State != "installed" {
			t.Fatal("unexpected terminal history", r.State)
		}
	}
	other, err := store.CreateAccount(t.Context(), "retirement-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.(state.MCPOAuthRetirementStore).RecoverMCPOAuthRetirement(t.Context(), other.ID, held, api.MCPOAuthRetirementAction{Action: "retry"}); err == nil {
		t.Fatal("other account recovered retirement")
	}
}
func TestIssuedGrantRetainedAfterConfigurationRaceMemory(t *testing.T) {
	testIssuedRace(t, state.NewMemStore())
}
func TestIssuedGrantRetainedAfterConfigurationRacePostgres(t *testing.T) {
	testIssuedRace(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testIssuedRace(t *testing.T, store state.Store) {
	f := newRetirementFixture(t, store)
	f.mutate = func() {
		cfg := f.view(t).Config
		cfg.Revision = uuid.NewString()
		if _, err := store.(state.MCPCatalogMonitorStore).ConfigureMCPCatalogMonitor(t.Context(), f.acct.ID, f.app.ID, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.connect(t); err == nil {
		t.Fatal("superseded exchange installed")
	}
	items := f.history(t)
	if len(items) != 1 || items[0].State != "pending" {
		t.Fatal("issued grant was lost", items)
	}
	id := items[0].ID
	// Explicit retry removes the installation grace period without waiting.
	if _, err := store.(state.MCPOAuthRetirementStore).RecoverMCPOAuthRetirement(t.Context(), f.acct.ID, id, api.MCPOAuthRetirementAction{Action: "retry"}); err != nil {
		t.Fatal(err)
	}
	if err := f.runner.TickRevocations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 2 {
		t.Fatal("orphan grant was not retired")
	}
}
func TestConfigurationReplacementRetiresGrantMemory(t *testing.T) {
	f := newRetirementFixture(t, state.NewMemStore())
	if err := f.connect(t); err != nil {
		t.Fatal(err)
	}
	cfg := f.view(t).Config
	cfg.Revision = uuid.NewString()
	observer := *cfg.Observer
	oauth := *observer.OAuth
	oauth.ClientID = "different-client"
	observer.OAuth = &oauth
	cfg.Observer = &observer
	if _, err := f.store.(state.MCPCatalogMonitorStore).ConfigureMCPCatalogMonitor(t.Context(), f.acct.ID, f.app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if f.view(t).ObserverStatus != nil && f.view(t).ObserverStatus.OAuth != nil {
		t.Fatal("configuration change retained unbound grant")
	}
	if err := f.runner.TickRevocations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 2 {
		t.Fatal("configuration replacement did not retire grant")
	}
}
