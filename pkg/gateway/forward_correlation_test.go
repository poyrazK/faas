// adr: 570
package gateway_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type forwardCorrelationReceiver struct {
	vmmdpb.UnimplementedVmmdServer
	fields chan wire.CorrelationFields
	other  chan string
}

func (s *forwardCorrelationReceiver) capture(ctx context.Context) {
	fields, _ := wire.CorrelationFromIncoming(ctx)
	s.fields <- fields
	md, _ := metadata.FromIncomingContext(ctx)
	value := ""
	if values := md.Get("x-transport"); len(values) != 0 {
		value = values[0]
	}
	s.other <- value
}

func (s *forwardCorrelationReceiver) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	s.capture(stream.Context())
	return stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusNoContent}}})
}

func (s *forwardCorrelationReceiver) ForwardRawStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	s.capture(stream.Context())
	return stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: http.StatusForbidden}}})
}

func TestForwardCanonicalCorrelationReplacesPriorHopMetadata(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "upgrade_refusal"}[raw], func(t *testing.T) {
			server := &forwardCorrelationReceiver{fields: make(chan wire.CorrelationFields, 1), other: make(chan string, 1)}
			listener := bufconn.Listen(1024 * 1024)
			rpc := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(rpc, server)
			go func() { _ = rpc.Serve(listener) }()
			t.Cleanup(rpc.Stop)
			conn, err := grpc.NewClient("passthrough://fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
			lookup := &fakeNodeLookup{cli: vmmdpb.NewVmmdClient(conn)}
			factory := gateway.ForwardingReverseProxy(lookup, nil)
			if raw {
				factory = gateway.ForwardingRawReverseProxy(lookup, nil, nil)
			}
			request := httptest.NewRequest(http.MethodGet, "http://guest.test/work", nil)
			request.Header.Set(api.InstanceIDHeader, "selected")
			request.Header.Set(api.RequestIDHeader, "request")
			base := metadata.NewOutgoingContext(request.Context(), metadata.Pairs("x-faas-instance-id", "prior", "x-faas-image-digest", "prior-image", "x-faas-region", "prior-region", "x-transport", "retained"))
			want := wire.CorrelationFields{RequestID: "request", AppID: "target", TenantID: "account", DeploymentID: "revision", InstanceID: "selected", NodeID: "node", WakeID: "causal-wake", InvocationID: "invocation"}
			request = request.WithContext(wire.WithContext(base, want))
			response := httptest.NewRecorder()
			factory(gateway.Target{AppID: "target", NodeID: "node", InstanceID: "selected", Port: 8080}).ServeHTTP(response, request)
			wantStatus := http.StatusNoContent
			if raw {
				wantStatus = http.StatusForbidden
			}
			if response.Code != wantStatus {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			got := <-server.fields
			if !reflect.DeepEqual(got, want) {
				t.Errorf("RPC correlation=%+v want=%+v", got, want)
			}
			if <-server.other != "retained" {
				t.Error("unrelated transport metadata was lost")
			}
		})
	}
}
