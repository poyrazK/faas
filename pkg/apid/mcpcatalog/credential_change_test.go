package mcpcatalog

import (
	"context"
	"errors"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCredentialChangeMemory(t *testing.T) { testCredentialChange(t, state.NewMemStore()) }
func TestCredentialChangePostgres(t *testing.T) {
	testCredentialChange(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testCredentialChange(t *testing.T, store state.Store) {
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@credential.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "credential-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	put := func(value string) {
		t.Helper()
		sealed, e := secretbox.Seal(id.Recipient(), secretbox.Envelope{"REFRESH": value})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.UpsertAppSecret(ctx, acct.ID, app.ID, "REFRESH", sealed); e != nil {
			t.Fatal(e)
		}
	}
	put("OLD_REFRESH")
	monitors := store.(state.MCPCatalogMonitorStore)
	observers := store.(state.MCPCatalogObserverStore)
	retirements := store.(state.MCPOAuthRetirementStore)
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", RevocationEndpoint: "https://provider.example/revoke", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	if _, err = monitors.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	claim := func() state.MCPCatalogObserverJob {
		t.Helper()
		jobs, e := observers.ListDueMCPCatalogObservers(ctx, 4)
		if e != nil || len(jobs) != 1 {
			t.Fatal("missing job", e)
		}
		job, e := observers.ClaimMCPCatalogObserver(ctx, jobs[0])
		if e != nil {
			t.Fatal(e)
		}
		return job
	}
	job := claim()
	calls, revocations := 0, 0
	race := false
	runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{id} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/revoke" {
			revocations++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		calls++
		if race {
			put("RACED_REFRESH")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"ACCESS","refresh_token":"ROTATED","token_type":"Bearer","expires_in":3600}`))}, nil
	})}}
	if _, err = runner.oauthToken(ctx, job); err != nil {
		t.Fatal(err)
	}
	put("NEW_REFRESH")
	root, _ := monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if view.ObserverStatus.OAuth != nil || view.Config.Revision == job.Snapshot.Config.Revision || view.ObserverStatus.LeaseID != "" || view.OAuthLifecycle.CredentialState != "credential_changed" {
		t.Fatal("credential mutation failed to discard and fence old grant")
	}
	records, err := retirements.ListMCPOAuthRetirements(ctx, acct.ID, 0)
	if err != nil || len(records.Items) != 1 || records.Items[0].Reason != "credential_changed" {
		t.Fatal("old grant not retained", err)
	}
	if err = runner.TickRevocations(ctx); err != nil || revocations != 0 {
		t.Fatal("replacement intent did not protect old grant", err)
	}
	records, _ = retirements.ListMCPOAuthRetirements(ctx, acct.ID, 0)
	if records.Items[0].State != "held" {
		t.Fatal("replacement retirement was not held")
	}
	job = claim()
	if _, err = runner.oauthToken(ctx, job); err != nil || calls != 2 {
		t.Fatal("replacement renewal failed", err)
	}
	// Host-key maintenance must preserve the generation and installed grant.
	secret, err := store.GetAppSecret(ctx, acct.ID, app.ID, "REFRESH")
	if err != nil {
		t.Fatal(err)
	}
	source := state.MCPCatalogOAuthSecretSource(*secret)
	sealed, _ := secretbox.Seal(id.Recipient(), secretbox.Envelope{"REFRESH": "NEW_REFRESH"})
	if err = store.ResealAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "default", "REFRESH", "", "", sealed); err != nil {
		t.Fatal(err)
	}
	secret, _ = store.GetAppSecret(ctx, acct.ID, app.ID, "REFRESH")
	if state.MCPCatalogOAuthSecretSource(*secret) != source {
		t.Fatal("reseal changed generation")
	}
	root, _ = monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	job.Snapshot, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	if _, err = runner.oauthToken(ctx, job); err != nil || calls != 2 {
		t.Fatal("resealing reset the grant", err)
	}
	// Rotate while an exchange is underway: preserve its late grant for retirement.
	// A fresh generation forces a new exchange.
	put("BEFORE_RACE")
	if err = runner.TickRevocations(ctx); err != nil {
		t.Fatal(err)
	}
	job = claim()
	race = true
	if _, err = runner.oauthToken(ctx, job); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("old generation installed late result", err)
	}
	records, _ = retirements.ListMCPOAuthRetirements(ctx, acct.ID, 0)
	found := false
	for _, r := range records.Items {
		if r.Reason == "credential_changed" && (r.State == "pending" || r.State == "manual_required") {
			found = true
		}
	}
	if !found {
		t.Fatal("late observed grant lost retirement intent")
	}
	if err = store.DeleteAppSecret(ctx, acct.ID, app.ID, "REFRESH"); err != nil {
		t.Fatal(err)
	}
	root, _ = monitors.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	view, _ = api.CatalogMonitorRoleSnapshot(root, "reader")
	if view.OAuthLifecycle.CredentialState != "reconnect_required" {
		t.Fatal("deletion omitted reconnect status")
	}
}

func TestCredentialGenerationLegacyBackfillPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@legacy.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "legacy-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	cipher := []byte("legacy sealed refresh")
	if err = store.UpsertAppSecret(ctx, acct.ID, app.ID, "REFRESH", cipher); err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	if _, err = store.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueMCPCatalogObservers(ctx, 4)
	if err != nil || len(due) != 1 {
		t.Fatal(err)
	}
	job, err := store.ClaimMCPCatalogObserver(ctx, due[0])
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	expiry := at.Add(time.Hour)
	status := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: digest([]byte(digest(cipher))), Ciphertext: []byte("preserved grant"), LastRenewalAt: &at, ExpiresAt: &expiry}
	if err = store.SaveMCPCatalogObserverOAuth(ctx, job, status); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../migrations/20261014140500014_mcp_oauth_credential_generation.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	start := strings.Index(text, "DO $$ DECLARE row record;")
	end := strings.Index(text, "CREATE FUNCTION mcp_oauth_secret_credential_change()")
	if _, err = pool.Exec(ctx, text[start:end]); err != nil {
		t.Fatal("legacy source backfill failed", err)
	}
	root, err := store.GetMCPCatalogMonitor(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	secret, _ := store.GetAppSecret(ctx, acct.ID, app.ID, "REFRESH")
	if view.ObserverStatus.OAuth.SourceSHA256 != digest([]byte(state.MCPCatalogOAuthSecretSource(*secret))) || string(view.ObserverStatus.OAuth.Ciphertext) != "preserved grant" || view.Config.Revision != job.Snapshot.Config.Revision {
		t.Fatal("backfill reset grant or produced wrong generation identity")
	}
}
