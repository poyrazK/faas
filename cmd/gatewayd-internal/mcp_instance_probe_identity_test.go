package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// Production-us rc.294: every MCP replica probe failed. The forwarder sends
// vmmd the instance named by the x-faas-instance header, the probes never set
// it, vmmd answered "instance is required", and the probe reported HTTP 502,
// so no MCP deployment passed qualification (Hunt 14 H14-3).
func TestMCPInstanceProbesForwardTheTargetInstance(t *testing.T) {
	target := gateway.Target{AppID: "app-1", DeploymentID: "dep-1", InstanceID: "ins-1", NodeID: "node-1", WakeID: "wake-1"}
	resolve := func(context.Context, string, string, string) (gateway.Target, bool, error) { return target, true, nil }
	lookup := func(string) (string, bool) { return target.AppID, true }
	tests := []struct {
		name    string
		handler func(forward func(gateway.Target) http.Handler) http.HandlerFunc
		request func() *http.Request
	}{
		{
			name: "health",
			handler: func(forward func(gateway.Target) http.Handler) http.HandlerFunc {
				return internalMCPInstanceProbeHandler(lookup, resolve, forward, nil)
			},
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/v1/internal/apps/h14-mcp/mcp-health?deployment_id=dep-1&instance_id=ins-1&host=deploy-1-h14-mcp.gregale.dev", nil)
			},
		},
		{
			name: "protocol",
			handler: func(forward func(gateway.Target) http.Handler) http.HandlerFunc {
				return internalMCPInstanceProtocolProbeHandler(lookup, resolve, forward, nil)
			},
			request: func() *http.Request {
				body := `{"host":"deploy-1-h14-mcp.gregale.dev","endpoint":"/mcp","protocol_version":"` + api.MCPProtocolVersion +
					`","rpc_request":{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}}`
				return httptest.NewRequest(http.MethodPost, "/v1/internal/apps/h14-mcp/mcp-probe?deployment_id=dep-1&instance_id=ins-1", strings.NewReader(body))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var forwarded http.Header
			forward := func(gateway.Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded = r.Header.Clone()
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("MCP-Protocol-Version", api.MCPProtocolVersion)
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
				})
			}
			rec := httptest.NewRecorder()
			tt.handler(forward).ServeHTTP(rec, tt.request())
			if rec.Code != http.StatusOK || forwarded == nil {
				t.Fatalf("probe status %d, forwarded=%v: %s", rec.Code, forwarded != nil, rec.Body.String())
			}
			// gateway's forwarder reads x-faas-instance for the vmmd stream.
			if got := forwarded.Get("x-faas-instance"); got != target.InstanceID {
				t.Fatalf("forwarded x-faas-instance = %q, want %q: vmmd refuses a forward without the instance", got, target.InstanceID)
			}
			if got := forwarded.Get("x-faas-wake-id"); got != target.WakeID {
				t.Errorf("forwarded x-faas-wake-id = %q, want %q", got, target.WakeID)
			}
		})
	}
}
