package mcpreadiness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessOAuthProbes(t *testing.T) {
	for _, mode := range []string{"manual", "scheduled", "changed", "reconnect", "header", "staging", "staging-scheduled", "missing-environment"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			scope := ""
			if strings.HasPrefix(mode, "staging") || mode == "missing-environment" {
				scope = "staging"
			}
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "managed-readiness@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "managed-readiness", Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{Scope: scope, AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			connectionScope := scope
			if mode == "missing-environment" {
				connectionScope = "default"
			}
			cfg := api.MCPCatalogMonitorConfig{Scope: connectionScope, Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 300, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader-client"}}}
			if _, err = store.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); err != nil {
				t.Fatal(err)
			}
			catalogJob, err := store.ClaimMCPCatalogObserver(ctx, state.MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Role: "reader", Snapshot: api.MCPCatalogMonitorSnapshot{Config: cfg}})
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"ACCESS_TOKEN": "MANAGED_ACCESS", "REFRESH_TOKEN": "PRIVATE_REFRESH"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			expiry := now.Add(time.Hour)
			authState := "connected"
			if mode == "reconnect" {
				authState = "reauthorization_required"
			}
			if err = store.SaveMCPCatalogObserverOAuth(ctx, catalogJob, api.MCPCatalogObserverOAuthStatus{State: authState, SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("guided"))), Ciphertext: sealed, ExpiresAt: &expiry, LastRenewalAt: &now}); err != nil {
				t.Fatal(err)
			}
			if err = store.ReleaseMCPReadinessOAuth(ctx, catalogJob); err != nil {
				t.Fatal(err)
			}
			contract, err := mcphosting.NewContract(api.MCPProtocolVersion, []mcphosting.Tool{{Name: "add", InputSchema: map[string]any{"type": "object"}}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := mcphosting.MarshalContract(contract)
			if err != nil {
				t.Fatal(err)
			}
			request := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Scope: scope, Reason: "managed OAuth probe", Roles: []api.MCPReadinessRole{{Name: "probe", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Monitor: &api.MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 1, OAuthConnection: &api.MCPReadinessOAuthConnection{Role: "reader", Revision: cfg.Revision}}}}}
			if scope != "" {
				monitor := request.Roles[0].Monitor
				ref := *monitor.OAuthConnection
				monitor.OAuthConnection = nil
				monitor.OAuthConnections = map[string]api.MCPReadinessOAuthConnection{api.MCPConnectionScope(connectionScope): ref}
			}
			if mode == "missing-environment" {
				if _, e := store.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request); e == nil {
					t.Fatal("cross-environment policy binding accepted")
				}
				return
			}
			if _, err = store.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request); err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: monitorTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "preview.example" || r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer MANAGED_ACCESS" {
					t.Fatal("wrong probe target or credential")
				}
				var msg struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
					t.Fatal(err)
				}
				result := `{}`
				code := 200
				switch msg.Method {
				case "initialize":
					result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, api.MCPProtocolVersion)
				case "notifications/initialized":
					code = 202
				case "ping":
					if mode == "changed" {
						cfg.Revision = uuid.NewString()
						if _, e := store.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); e != nil {
							t.Fatal(e)
						}
					}
				case "tools/list":
					result = `{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}`
				default:
					t.Errorf("unexpected method %s", msg.Method)
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}, "Mcp-Protocol-Version": []string{api.MCPProtocolVersion}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, msg.ID, result)))}, nil
			})}
			identities := func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }
			h := &Handler{Store: store, Client: client, Identities: identities, Endpoint: func(context.Context, state.App, state.Deployment) (string, error) {
				return "https://preview.example", nil
			}}
			if mode == "scheduled" || mode == "staging-scheduled" || mode == "reconnect" {
				runner := MonitorRunner{Handler: h, Identities: identities}
				if err = runner.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				statuses, e := store.ListMCPReadinessMonitoring(ctx, account.ID, app.ID, "")
				if e != nil || len(statuses) != 1 {
					t.Fatal(statuses, e)
				}
				if mode == "reconnect" {
					if statuses[0].Reason != "oauth_reconnect_required" || statuses[0].RecoveryAction != "reconnect" || calls != 0 {
						t.Fatal("missing reconnect guidance", statuses)
					}
				} else if statuses[0].State != "healthy" || calls == 0 {
					t.Fatal("scheduled OAuth probe failed", statuses)
				}
				payload, _ := json.Marshal(statuses)
				if strings.Contains(string(payload), "MANAGED_ACCESS") || strings.Contains(string(payload), "PRIVATE_REFRESH") {
					t.Fatal("credentials leaked into monitor metadata")
				}
				return
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/deployments/"+dep.ID+"/mcp-readiness", strings.NewReader(`{"role":"probe"}`))
			req.SetPathValue("id", dep.ID)
			if mode == "header" {
				req.Header.Set(api.MCPReadinessTokenHeader, "CALLER_TOKEN")
			}
			rec := httptest.NewRecorder()
			h.Deployment(rec, req, account)
			want := 200
			if mode == "changed" {
				want = 409
			}
			if mode == "header" || mode == "missing-environment" {
				want = 400
			}
			if rec.Code != want {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if (mode == "manual" || mode == "staging") && !strings.Contains(rec.Body.String(), `"passed":true`) {
				t.Fatal("managed manual probe did not pass", rec.Body.String())
			}
			if (mode == "header" || mode == "missing-environment") && calls != 0 {
				t.Fatal("caller credential reached the MCP endpoint")
			}
			if strings.Contains(rec.Body.String(), "MANAGED_ACCESS") || strings.Contains(rec.Body.String(), "PRIVATE_REFRESH") {
				t.Fatal("credentials leaked into evidence")
			}
		})
	}
}
