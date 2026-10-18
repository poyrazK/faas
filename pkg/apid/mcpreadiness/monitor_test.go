package mcpreadiness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type monitorTransport func(*http.Request) (*http.Response, error)

func (f monitorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMCPReadinessMonitorCredentialsAndProbe(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "monitor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "monitor-app", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	contract, err := mcphosting.NewContract(api.MCPProtocolVersion, []mcphosting.Tool{{Name: "add", InputSchema: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := mcphosting.MarshalContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	request := api.SetMCPReadinessPolicyRequest{Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "monitor", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Monitor: &api.MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 2, SecretRef: "secret:MONITOR_TOKEN"}}}}
	policy, err := store.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	expectedToken := "ROTATED_SECRET"
	client := &http.Client{Transport: monitorTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "preview.example" || r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Error("incorrect probe target or token")
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
		case "tools/list":
			result = `{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}`
		default:
			t.Errorf("unexpected effectful method %s", msg.Method)
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}, "Mcp-Protocol-Version": []string{api.MCPProtocolVersion}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, msg.ID, result)))}, nil
	})}
	runner := MonitorRunner{Handler: &Handler{Store: store, Client: client, Endpoint: func(context.Context, state.App, state.Deployment) (string, error) {
		return "https://preview.example", nil
	}}, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }}
	if err = runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err := store.ListMCPReadinessMonitoring(ctx, account.ID, app.ID, "")
	if err != nil || len(statuses) != 1 || statuses[0].Reason != "credential_unavailable" || calls != 0 {
		t.Fatal("missing credential should fail without a request", statuses, err)
	}
	for _, token := range []string{"ROTATED_SECRET", "NEXT_SECRET"} {
		expectedToken = token
		sealed, e := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"MONITOR_TOKEN": token})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.UpsertAppSecret(ctx, account.ID, app.ID, "MONITOR_TOKEN", sealed); e != nil {
			t.Fatal(e)
		}
		request.ExpectedRevision = policy.Revision
		policy, e = store.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		if e = runner.Tick(ctx); e != nil {
			t.Fatal(e)
		}
		statuses, e = store.ListMCPReadinessMonitoring(ctx, account.ID, app.ID, "")
		if e != nil || statuses[0].State != "healthy" {
			t.Fatalf("probe did not recover: %+v %v", statuses, e)
		}
		encoded, _ := json.Marshal(statuses)
		if strings.Contains(string(encoded), token) {
			t.Fatal("credential leaked into retained monitoring history")
		}
	}
	if calls == 0 {
		t.Fatal("probe never ran")
	}
}
