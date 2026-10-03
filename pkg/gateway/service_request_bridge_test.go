// adr: 429
package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceEvidenceProvider struct {
	snapshot gateway.ServiceEndpointsSnapshot
}

func (p serviceEvidenceProvider) ServiceEndpoints(context.Context, string) (gateway.ServiceEndpointsSnapshot, error) {
	return p.snapshot, nil
}

func TestServiceRequestEvidenceUsesActualBridgeResponse(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status           int
		raw, unavailable bool
	}{
		{"HTTP success", 200, false, false},
		{"HTTP guest 503", 503, false, false},
		{"HTTP transport unavailable", 503, false, true},
		{"raw guest rejection", 403, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appID, accountID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
			client := &fakeVmmdClient{}
			if tc.raw {
				client.RawStream = &fakeRawBidiStream{Responses: []*vmmdpb.ForwardRawResponse{{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: int32(tc.status)}}}}}
			} else if tc.unavailable {
				client.StreamErr = status.Error(codes.Unavailable, "upstream disconnected")
			} else {
				client.Stream = &fakeBidiStream{Responses: []*vmmdpb.ForwardHTTPStreamResponse{{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: int32(tc.status)}}}}}
			}
			lookup := &fakeNodeLookup{cli: client}
			var observed []gateway.ServiceRequestObservation
			proxy := gateway.NewServiceProxy(gateway.ServiceProxyConfig{
				Provider: serviceEvidenceProvider{gateway.ServiceEndpointsSnapshot{AppID: appID, Endpoints: []gateway.ServiceEndpoint{{InstanceID: "selected-instance", NodeID: "node-1", DeploymentID: deploymentID, Port: 8080}}}},
				Resolve: func(context.Context, string, string) (gateway.ServiceTarget, bool, error) {
					return gateway.ServiceTarget{AppID: appID, ScenarioTestRunID: "verified-run", WebSocketEnabled: true}, true, nil
				},
				Authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
					return gateway.ServiceCaller{AppID: "caller", AccountID: accountID}, nil
				},
				Forward: gateway.ForwardingReverseProxy(lookup, nil), RawForward: gateway.ForwardingRawReverseProxy(lookup, nil, nil),
				ObserveRequest: func(_ *http.Request, o gateway.ServiceRequestObservation) { observed = append(observed, o) },
			})
			req := httptest.NewRequest("GET", "/v1/internal/services/worker/healthz", nil)
			req.Header.Set(gateway.ServiceProxyCallerAppHeader, "caller")
			if tc.raw {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, req)
			if tc.unavailable {
				if len(observed) != 0 {
					t.Fatalf("bridge failure created handled evidence: %+v", observed)
				}
				return
			}
			if response.Code != tc.status || len(observed) != 1 {
				t.Fatalf("response=%d observations=%+v", response.Code, observed)
			}
			o := observed[0]
			if o.AccountID != accountID || o.Target.AppID != appID || o.Target.DeploymentID != deploymentID || o.Target.InstanceID != "selected-instance" || o.Status != tc.status || o.ColdBoot || o.ScenarioTestRunID != "verified-run" {
				t.Fatalf("wrong bridge identity: %+v", o)
			}
		})
	}
}
