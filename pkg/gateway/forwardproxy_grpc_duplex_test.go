package gateway_test

// adr: 126 — gRPC messages traverse both HTTP hops before request EOF.
// adr: 610

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"google.golang.org/grpc"
)

type duplexHTTPClient struct {
	*fakeVmmdClient
	stream *duplexHTTPStream
}

func (c *duplexHTTPClient) ForwardHTTPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	c.stream.ctx = ctx
	return c.stream, nil
}

type duplexHTTPStream struct {
	fakeBidiStream
	received     chan struct{}
	ended        chan struct{}
	receivedOnce sync.Once
	endedOnce    sync.Once
	step         int
}

func (s *duplexHTTPStream) Send(frame *vmmdpb.ForwardHTTPStreamRequest) error {
	if err := s.fakeBidiStream.Send(frame); err != nil {
		return err
	}
	if len(frame.GetBodyChunk()) > 0 {
		s.receivedOnce.Do(func() { close(s.received) })
	}
	return nil
}
func (s *duplexHTTPStream) CloseSend() error {
	err := s.fakeBidiStream.CloseSend()
	s.endedOnce.Do(func() { close(s.ended) })
	return err
}
func (s *duplexHTTPStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	step := s.step
	s.step++
	switch step {
	case 0:
		return &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200, Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/grpc"}}}}}, nil
	case 1:
		select {
		case <-s.received:
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
		return &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("pong")}}, nil
	case 2:
		select {
		case <-s.ended:
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
		return &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Trailers: []*vmmdpb.Header{{Name: "Grpc-Status", Value: "0"}}}}}, nil
	default:
		return nil, io.EOF
	}
}

func TestGRPCDuplexThroughPublicAndForwarderHTTPHops(t *testing.T) {
	stream := &duplexHTTPStream{received: make(chan struct{}), ended: make(chan struct{})}
	client := &duplexHTTPClient{fakeVmmdClient: &fakeVmmdClient{}, stream: stream}
	tracker, err := activity.New(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := gateway.Target{NodeID: "node-1", InstanceID: "i-test", AppID: uuid.NewString(), DeploymentID: uuid.NewString()}
	forwarder := gateway.WithDeploymentActivity(gateway.ForwardingReverseProxy(&fakeNodeLookup{cli: client}, nil), tracker)(target)
	finished := make(chan struct{})
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		r.Header.Set("x-faas-instance", "i-test")
		r.Header.Set("x-faas-protocol", "grpc")
		forwarder.ServeHTTP(w, r)
	}))
	defer internal.Close()
	edge := httptest.NewServer(gateway.NewInternalReverseProxy(gateway.NewTCPDialer(internal.Listener.Addr().String()), &url.URL{Scheme: "http", Host: "internal"}, nil, false))
	defer edge.Close()
	body, upload := io.Pipe()
	defer upload.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { _ = upload.CloseWithError(ctx.Err()) })
	defer stopClose()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, edge.URL+"/audit.Echo/Bidi", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/grpc")
	sent := make(chan error, 1)
	go func() { _, err := upload.Write([]byte("ping")); sent <- err }()
	response, err := edge.Client().Do(request)
	if err != nil {
		t.Fatalf("response blocked before request EOF: %v", err)
	}
	defer response.Body.Close()
	got := make([]byte, 4)
	if _, err := io.ReadFull(response.Body, got); err != nil || string(got) != "pong" {
		t.Fatalf("duplex reply=%q err=%v", got, err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 1 {
		t.Fatalf("duplex response before request EOF dropped activity: %+v", got)
	}
	if err := upload.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if response.Trailer.Get("Grpc-Status") != "0" {
		t.Fatalf("trailers=%v", response.Trailer)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("duplex forwarder did not return")
	}
	if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 0 || got.ActivityVersion != 3 {
		t.Fatalf("duplex EOF retained activity: %+v", got)
	}
}
