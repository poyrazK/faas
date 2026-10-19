package mcpcatalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReadinessOAuthManagedRenewal(t *testing.T) {
	for _, mode := range []string{"rotate", "staging", "revoked", "uncertain", "changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "oauth-readiness@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "oauth-readiness", Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			scope := ""
			if mode == "staging" {
				scope = "staging"
			}
			cfg := api.MCPCatalogMonitorConfig{Scope: scope, Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 300, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader-client"}}}
			if _, err = store.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); err != nil {
				t.Fatal(err)
			}
			job, err := store.ClaimMCPCatalogObserver(ctx, state.MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Role: "reader", Snapshot: api.MCPCatalogMonitorSnapshot{Config: cfg}})
			if err != nil {
				t.Fatal(err)
			}
			blob, err := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"ACCESS_TOKEN": "OLD_ACCESS", "REFRESH_TOKEN": "OLD_REFRESH"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			expiry := now.Add(10 * time.Second)
			auth := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("guided"))), Ciphertext: blob, ExpiresAt: &expiry, LastRenewalAt: &now}
			if err = store.SaveMCPCatalogObserverOAuth(ctx, job, auth); err != nil {
				t.Fatal(err)
			}
			if err = store.ReleaseMCPReadinessOAuth(ctx, job); err != nil {
				t.Fatal(err)
			}
			calls := 0
			runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != "https://provider.example/token" || req.Method != http.MethodPost {
					t.Fatal("unexpected token target")
				}
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if req.Form.Get("refresh_token") != "OLD_REFRESH" || req.Form.Get("grant_type") != "refresh_token" {
					t.Fatal("incorrect refresh grant")
				}
				if mode == "uncertain" {
					return nil, fmt.Errorf("controlled token response loss")
				}
				if mode == "changed" {
					cfg.Revision = uuid.NewString()
					if _, e := store.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); e != nil {
						t.Fatal(e)
					}
				}
				code := 200
				body := `{"access_token":"NEW_ACCESS","refresh_token":"NEW_REFRESH","token_type":"Bearer","expires_in":3600}`
				if mode == "revoked" {
					code = 400
					body = `{"error":"invalid_grant","error_description":"PRIVATE_PROVIDER_TEXT"}`
				}
				return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			ref := api.MCPReadinessOAuthConnection{Scope: scope, Role: "reader", Revision: job.Snapshot.Config.Revision}
			credential, err := runner.AcquireReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp")
			if mode != "rotate" && mode != "staging" {
				if err == nil || credential != nil {
					t.Fatal("unsafe renewal returned credential")
				}
				_, _ = runner.AcquireReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp")
				if calls != 1 {
					t.Fatal("unsafe exchange was repeated")
				}
				return
			}
			if err != nil || credential.Token != "NEW_ACCESS" {
				t.Fatalf("renewal failed: %v", err)
			}
			if _, err = runner.AcquireReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp"); !errors.Is(err, state.ErrMCPReadinessOAuthBusy) {
				t.Fatal("shared connection lease duplicated", err)
			}
			if err = credential.Release(ctx); err != nil {
				t.Fatal(err)
			}
			cached, err := runner.AcquireReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp")
			if err != nil || cached.Token != "NEW_ACCESS" || calls != 1 {
				t.Fatal("persisted token was not reused", err)
			}
			if err = cached.Release(ctx); err != nil {
				t.Fatal(err)
			}
			root, err := store.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			view, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
			saved, err := secretbox.OpenMulti([]*age.X25519Identity{identity}, view.ObserverStatus.OAuth.Ciphertext)
			if err != nil || saved["REFRESH_TOKEN"] != "NEW_REFRESH" {
				t.Fatal("rotated refresh token was lost", err)
			}
		})
	}
}

func TestEnvironmentOAuthSecretIsolation(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, e := store.CreateAccount(ctx, "scoped-token@example.com", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	app, e := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "scoped-token", Type: state.AppTypeApp, Status: state.AppActive})
	if e != nil {
		t.Fatal(e)
	}
	identity, e := age.GenerateX25519Identity()
	if e != nil {
		t.Fatal(e)
	}
	runner := ObserverRunner{Store: store, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }}
	for _, scope := range []string{"default", "staging"} {
		sealed, e := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"REFRESH": scope + "_REFRESH"})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.UpsertAppSecretInScope(ctx, account.ID, app.ID, scope, "REFRESH", sealed); e != nil {
			t.Fatal(e)
		}
	}
	for _, scope := range []string{"", "staging", "preview"} {
		job := state.MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Snapshot: api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Scope: scope}}}
		token, _, e := runner.oauthSecret(ctx, job, "secret:REFRESH")
		if scope == "preview" {
			if e == nil || token != "" {
				t.Fatal("missing environment used default secret")
			}
			continue
		}
		if e != nil || token != api.MCPConnectionScope(scope)+"_REFRESH" {
			t.Fatal("wrong scoped credential", scope, e)
		}
	}
}
