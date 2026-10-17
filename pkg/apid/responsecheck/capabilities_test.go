package responsecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeploymentResponseCheckCapabilitiesMixedGateways(t *testing.T) {
	for _, outcome := range []string{"ready", "old gateway", "method missing", "not implemented", "unconfigured", "new protocol", "missing protocol", "contradictory", "outage", "malformed", "oversized", "redirect", "cancelled", "no gateway"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != api.DeploymentResponseCheckCapabilitiesControlPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("capability check changed the control route or forwarded account credentials")
				}
				result := api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: true}
				switch outcome {
				case "old gateway":
					w.WriteHeader(http.StatusNotFound)
					return
				case "method missing":
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				case "not implemented":
					w.WriteHeader(http.StatusNotImplemented)
					return
				case "unconfigured":
					result.Available, result.UnavailableReason = false, api.DeploymentResponseCheckGatewayUnconfigured
				case "new protocol":
					result.ProtocolVersion++
				case "missing protocol":
					result.ProtocolVersion = 0
				case "contradictory":
					result.UnavailableReason = "private-runtime-detail"
				case "outage":
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				case "malformed":
					_, _ = w.Write([]byte("not JSON private-runtime-detail"))
					return
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", api.DeploymentResponseCheckMaxWireBytes+1)))
					return
				case "redirect":
					http.Redirect(w, r, "/must-not-follow", http.StatusFound)
					return
				case "cancelled":
					cancel()
					<-r.Context().Done()
					return
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer gateway.Close()
			service := Service{GatewayURL: gateway.URL} // nil store/app loader proves this path needs no target.
			if outcome == "no gateway" {
				service.GatewayURL = ""
			}
			request := httptest.NewRequest(http.MethodGet, "/v1/deployments/response-check/capabilities", nil)
			request = request.WithContext(ctx)
			request.Header.Set("Authorization", "Bearer owner-token")
			request.Header.Set("Cookie", "session=private")
			response := httptest.NewRecorder()
			service.ServeCapabilities(response, request)
			var result api.DeploymentResponseCheckCapabilities
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.SupportsCurrentProtocol() != (outcome == "ready") || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("invalid capability result: code=%d result=%+v", response.Code, result)
			}
			wantReason := api.DeploymentResponseCheckGatewayUnavailable
			switch outcome {
			case "ready":
				wantReason = ""
			case "old gateway", "method missing", "not implemented", "new protocol", "missing protocol":
				wantReason = api.DeploymentResponseCheckGatewayUnsupported
			case "unconfigured", "no gateway":
				wantReason = api.DeploymentResponseCheckGatewayUnconfigured
			}
			if result.UnavailableReason != wantReason || result.ProtocolVersion != api.DeploymentResponseCheckProtocolVersion || strings.Contains(response.Body.String(), "private-runtime-detail") {
				t.Fatalf("unsafe or misleading result: %+v", result)
			}
			if (calls.Load() == 0) != (outcome == "no gateway") || calls.Load() > 1 {
				t.Fatalf("unexpected control calls: %d", calls.Load())
			}
		})
	}
}
