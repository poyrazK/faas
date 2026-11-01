// adr: 20261031105055
package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// The forwarder names the target's node on the response only for an edge
// that asked, and a guest's own copy never survives.
func TestForwardingReverseProxyNamesTheInstanceNode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		asked bool
		want  string
	}{
		{name: "edge asked", asked: true, want: "node-7"},
		{name: "no request", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &fakeBidiStream{Responses: []*vmmdpb.ForwardHTTPStreamResponse{
				{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
					Status:  http.StatusOK,
					Headers: []*vmmdpb.Header{{Name: gateway.InstanceNodeHeader, Value: "guest-forged"}},
				}}},
			}}
			proxy := gateway.ForwardingReverseProxy(&fakeNodeLookup{cli: &fakeVmmdClient{Stream: stream}}, nil)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("x-faas-instance", "i-test")
			if tc.asked {
				r.Header.Set(gateway.EdgePlacementHeader, "1")
			}
			r = gateway.TakeEdgePlacement(r)
			rec := httptest.NewRecorder()

			proxy(gateway.Target{NodeID: "node-7", InstanceID: "i-test"}).ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get(gateway.InstanceNodeHeader); got != tc.want {
				t.Fatalf("%s = %q, want %q", gateway.InstanceNodeHeader, got, tc.want)
			}
		})
	}
}
