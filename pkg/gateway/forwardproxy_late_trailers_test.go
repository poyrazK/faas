package gateway_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// H2 transports can learn trailer names only at EOF. Exercise the actual H1
// server/client boundary, rather than treating late headers as a wire receipt.
// adr: 126 — preserve gRPC terminal status through the forwarding stream.
func TestForwardingReverseProxyLateTrailersOnHTTPWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty", nil}, {"protobuf", []byte{0, 0, 0, 0, 2, 8, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			stream := &fakeBidiStream{Responses: []*vmmdpb.ForwardHTTPStreamResponse{
				{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
					Status: http.StatusOK, Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/grpc"}, {Name: "Content-Length", Value: strconv.Itoa(len(body))}},
				}}},
				{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: body}},
				{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
					Trailers: []*vmmdpb.Header{
						{Name: "Grpc-Status", Value: "0"}, {Name: "X-Audit-Final", Value: "one"}, {Name: "X-Audit-Final", Value: "two"},
						{Name: "Connection", Value: "keep-alive"}, {Name: api.DeploymentIDHeader, Value: "forged-deployment"},
					},
				}}},
			}}
			lookup := &fakeNodeLookup{cli: &fakeVmmdClient{Stream: stream}}
			proxy := gateway.ForwardingReverseProxy(lookup, nil)(gateway.Target{NodeID: "node-1", InstanceID: "i-test"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Header.Set("x-faas-instance", "i-test")
				proxy.ServeHTTP(w, r)
			}))
			defer server.Close()
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			got, err := io.ReadAll(response.Body)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("body = %x, err = %v; want %x", got, err, body)
			}
			if response.Trailer.Get("Grpc-Status") != "0" {
				t.Fatalf("missing late grpc-status: trailers=%v headers=%v", response.Trailer, response.Header)
			}
			values := response.Trailer.Values("X-Audit-Final")
			if len(values) != 2 || values[0] != "one" || values[1] != "two" {
				t.Fatalf("trailer values = %v", values)
			}
			if response.Trailer.Get("Connection") != "" || response.Trailer.Get(api.DeploymentIDHeader) != "" {
				t.Fatalf("guest bypassed trailer filtering: %v", response.Trailer)
			}
		})
	}
}
