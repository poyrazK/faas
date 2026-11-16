package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestPrivatePublicationExactReplicaProtocol(t *testing.T) {
	env, server := apidserver.SetupChangePlan(t, api.PlanPro, "")
	store, acct := *env.Store(), *env.Acct()
	app, p := granttest.Publication(t, store, acct, "private-protocol", "", strings.Repeat("7", 64))
	instance, err := store.CreateInstance(t.Context(), app.ID, p.Outcome.DeploymentID, string(state.StateRunning), 256, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target := api.ToolInvocationTarget{App: app.Slug, AppID: app.ID, DeploymentID: p.Outcome.DeploymentID, EndpointPath: "/mcp"}
	var mode atomic.Value
	mode.Store("valid")
	var redirected atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(500) }))
	t.Cleanup(redirect.Close)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("deployment_id") != target.DeploymentID || r.URL.Query().Get("instance_id") != instance.ID || r.Header.Get("MCP-Probe-Authorization") != "synthetic-private-test-token" {
			t.Error("protocol probe escaped exact target or credential boundary")
		}
		if mode.Load().(string) == "redirect" {
			http.Redirect(w, r, redirect.URL, 302)
			return
		}
		var probe api.MCPInstanceProtocolProbeRequest
		if json.NewDecoder(r.Body).Decode(&probe) != nil {
			w.WriteHeader(400)
			return
		}
		if probe.Host != "preview.example" || probe.Endpoint != "/mcp" {
			t.Error("unexpected protocol endpoint")
		}
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(probe.RPCRequest, &rpc) != nil {
			w.WriteHeader(400)
			return
		}
		status := 200
		var value any = map[string]any{}
		switch rpc.Method {
		case "initialize":
			value = map[string]any{"protocolVersion": probe.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		case "notifications/initialized":
			status = 202
		case "server/discover":
			value = map[string]any{"supportedVersions": []string{probe.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			value = map[string]any{"tools": []mcphosting.Tool{{Name: "visible", InputSchema: map[string]any{"type": "object"}}}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": value})
		out := api.MCPInstanceProtocolProbeResponse{AppID: app.ID, DeploymentID: target.DeploymentID, InstanceID: instance.ID, HTTPStatus: status, ContentType: "application/json", ProtocolVersion: probe.ProtocolVersion, Body: string(body)}
		switch mode.Load().(string) {
		case "wrong identity":
			out.InstanceID = uuid.NewString()
		case "oversize":
			out.BodyTooLarge = true
		case "session":
			out.SessionIDPresent = true
		case "wrong version":
			out.ProtocolVersion = "unsupported"
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(gateway.Close)
	server.WithGatewaydControlURL(gateway.URL)
	for _, version := range []string{mcphosting.ProtocolVersion, mcphosting.LegacyProtocolVersion} {
		for _, test := range []string{"valid", "wrong identity", "oversize", "session", "wrong version", "redirect"} {
			t.Run(version+"/"+test, func(t *testing.T) {
				mode.Store(test)
				probe, err := server.PrivatePublicationReplicaClientForTest(t.Context(), target, instance.ID, "preview.example", "synthetic-private-test-token", version)
				if err != nil {
					t.Fatal(err)
				}
				result := mcphosting.ValidateCompatibility(t.Context(), probe, nil)
				if result.OK != (test == "valid") {
					t.Fatalf("protocol check: %+v", result)
				}
			})
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("credential-bearing gateway request followed a redirect")
	}
}
