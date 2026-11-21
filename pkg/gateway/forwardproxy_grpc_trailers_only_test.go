package gateway_test

// adr: 126 — gRPC status reaches the client as a trailer on every hop.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// A gRPC server answers an error it can decide before reading the request
// (unknown method, missing te header, auth) with one HEADERS frame carrying
// END_STREAM: "Trailers-Only". The bridge folds that into the response init
// frame, so grpc-status arrives as a header. Re-emitted as an ordinary header
// block, the client sees headers and then a bare end of stream, and reports
// "server closed the stream without sending trailers" instead of the status.
func TestGRPCTrailersOnlyResponseKeepsStatusInTrailers(t *testing.T) {
	stream := &fakeBidiStream{Responses: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200, Headers: []*vmmdpb.Header{
			{Name: "Content-Type", Value: "application/grpc"},
			{Name: "Grpc-Status", Value: "12"},
			{Name: "Grpc-Message", Value: "Method not found"},
		}}}},
	}}
	client := &fakeVmmdClient{Stream: stream}
	target := gateway.Target{NodeID: "node-1", InstanceID: "i-test"}
	forwarder := gateway.ForwardingReverseProxy(&fakeNodeLookup{cli: client}, nil)(target)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("x-faas-instance", "i-test")
		r.Header.Set("x-faas-protocol", "grpc")
		forwarder.ServeHTTP(w, r)
	}))
	defer internal.Close()
	edge := httptest.NewServer(gateway.NewInternalReverseProxy(gateway.NewTCPDialer(internal.Listener.Addr().String()), &url.URL{Scheme: "http", Host: "internal"}, nil, false))
	defer edge.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, edge.URL+"/hipstershop.Missing/Call", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/grpc")
	response, err := edge.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if got := response.Header.Get("Grpc-Status"); got != "" {
		t.Fatalf("grpc-status left in the header block (%q); clients wait for a trailer", got)
	}
	if got := response.Trailer.Get("Grpc-Status"); got != "12" {
		t.Fatalf("grpc-status trailer = %q, want 12 (trailers=%v)", got, response.Trailer)
	}
	if got := response.Trailer.Get("Grpc-Message"); got != "Method not found" {
		t.Fatalf("grpc-message trailer = %q", got)
	}
}
