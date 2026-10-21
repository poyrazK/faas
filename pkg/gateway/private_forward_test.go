package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type privateMarkerVmmd struct {
	fakeVmmdClient
	ctx context.Context
}

func (v *privateMarkerVmmd) ForwardHTTPStream(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	v.ctx = ctx
	return v.fakeVmmdClient.ForwardHTTPStream(ctx, opts...)
}

func TestPrivateForwardHTTPHeaderCannotBypassEgress(t *testing.T) {
	for _, private := range []bool{false, true} {
		stream := &fakeBidiStream{Responses: []*vmmdpb.ForwardHTTPStreamResponse{{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK}}}}}
		client := &privateMarkerVmmd{fakeVmmdClient: fakeVmmdClient{Stream: stream}}
		proxy := gateway.ForwardingReverseProxy(&fakeNodeLookup{cli: client}, nil)(gateway.Target{NodeID: "node", InstanceID: "instance", Port: 8080})
		request := httptest.NewRequest(http.MethodGet, "https://app.example/", nil)
		request.Header.Set("x-faas-instance", "instance")
		request.Header.Set("Gregale-Private-Forward", "1")
		request = request.WithContext(metadata.NewOutgoingContext(request.Context(), metadata.Pairs("gregale-private-forward", "1")))
		if private {
			request = request.WithContext(wire.WithPrivateForward(request.Context()))
		}
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request)
		if response.Code != http.StatusOK || client.ctx == nil {
			t.Fatalf("forward status=%d", response.Code)
		}
		md, _ := metadata.FromOutgoingContext(client.ctx)
		if got := wire.IsPrivateForward(metadata.NewIncomingContext(t.Context(), md)); got != private {
			t.Fatalf("private=%t forwarded route marker=%v", private, md)
		}
	}
}
