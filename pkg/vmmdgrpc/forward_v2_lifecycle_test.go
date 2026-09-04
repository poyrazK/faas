package vmmdgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// forwardV2IntegrationVMM supplies only the VmmdAPI method used by the
// forwarding handler. Embedding the interface keeps this test focused on the
// gRPC → manager → transport lifecycle without constructing a Firecracker VMM.
type forwardV2IntegrationVMM struct {
	VmmdAPI
}

func (forwardV2IntegrationVMM) NetnsFor(instance string) (string, bool) {
	if instance == "" {
		return "", false
	}
	return "fc-" + instance, true
}

// TestForwardHTTPStreamV2_ReusesBridgeAcrossRPCs drives the complete exported
// ForwardHTTPStream RPC twice. The first RPC is allowed to finish and its
// client context is then canceled; the second RPC must reuse the same
// manager-owned child instead of receiving an unavailable response from a
// stale socket or starting a replacement process.
func TestForwardHTTPStreamV2_ReusesBridgeAcrossRPCs(t *testing.T) {
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)

	savedSpawn := streamBridgeSpawn
	var starts atomic.Int32
	bridgeContextDone := make(chan struct{})
	var bridgeContextOnce sync.Once
	streamBridgeSpawn = func(ctx context.Context, _, _, _, _ string, _ uint32, _ string, _ []string) (*exec.Cmd, *bytes.Buffer, error) {
		go func() {
			<-ctx.Done()
			bridgeContextOnce.Do(func() { close(bridgeContextDone) })
		}()
		cmd := exec.CommandContext(ctx, "sleep", "60")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		starts.Add(1)
		return cmd, &bytes.Buffer{}, nil
	}
	t.Cleanup(func() { streamBridgeSpawn = savedSpawn })

	server := New(forwardV2IntegrationVMM{}, nil, "test", nil)
	server.streamBridges.waitSocket = func(string, time.Duration) error { return nil }
	server.streamBridges.reapInterval = time.Hour

	var bridgeRequests atomic.Int32
	var persistentHeadersOK atomic.Bool
	persistentHeadersOK.Store(true)
	var bodiesMu sync.Mutex
	var bodies []string
	var serverConnsMu sync.Mutex
	var serverConns []net.Conn
	h2Server := &http2.Server{}
	server.streamBridges.newTransport = func(string) *http2.Transport {
		clientConn, serverConn := net.Pipe()
		serverConnsMu.Lock()
		serverConns = append(serverConns, serverConn)
		serverConnsMu.Unlock()
		go func() {
			h2Server.ServeConn(serverConn, &http2.ServeConnOpts{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Faas-Bridge-Persistent") != "1" ||
						r.Header.Get("X-Faas-Bridge-Port") != "8080" {
						persistentHeadersOK.Store(false)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						persistentHeadersOK.Store(false)
						return
					}
					n := bridgeRequests.Add(1)
					bodiesMu.Lock()
					bodies = append(bodies, string(body))
					bodiesMu.Unlock()
					w.Header().Set("X-Faas-Bridge-Framing", "h1")
					w.Header().Set("Content-Type", "text/plain")
					w.WriteHeader(http.StatusOK)
					_, _ = fmt.Fprintf(w, "bridge-request-%d:%s", n, body)
				}),
			})
			_ = serverConn.Close()
		}()
		return &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					return clientConn, nil
				}
			},
		}
	}
	t.Cleanup(func() {
		serverConnsMu.Lock()
		defer serverConnsMu.Unlock()
		for _, conn := range serverConns {
			_ = conn.Close()
		}
	})

	grpcServer := grpc.NewServer()
	server.Register(grpcServer)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(func() { grpcServer.Stop(); _ = lis.Close() })
	t.Cleanup(func() { _ = server.Close(context.Background()) })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := vmmdpb.NewVmmdClient(conn)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	status, body, err := forwardV2LifecycleRoundTrip(firstCtx, client, "instance-lifecycle", "first")
	if err != nil {
		t.Fatalf("first ForwardHTTPStream: %v", err)
	}
	cancelFirst()
	if status != http.StatusOK || body != "bridge-request-1:first" {
		t.Fatalf("first response = (%d, %q), want (200, %q)", status, body, "bridge-request-1:first")
	}
	select {
	case <-bridgeContextDone:
		t.Fatal("persistent bridge context was canceled with the first RPC")
	case <-time.After(50 * time.Millisecond):
	}

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSecond()
	status, body, err = forwardV2LifecycleRoundTrip(secondCtx, client, "instance-lifecycle", "second")
	if err != nil {
		t.Fatalf("second ForwardHTTPStream: %v", err)
	}
	if status != http.StatusOK || body != "bridge-request-2:second" {
		t.Fatalf("second response = (%d, %q), want (200, %q)", status, body, "bridge-request-2:second")
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("bridge starts = %d, want one child reused across RPCs", got)
	}
	if got := bridgeRequests.Load(); got != 2 {
		t.Fatalf("bridge requests = %d, want 2", got)
	}
	if !persistentHeadersOK.Load() {
		t.Fatal("persistent bridge metadata was not preserved on the H2C request")
	}

	if err := server.Close(context.Background()); err != nil {
		t.Fatalf("server.Close: %v", err)
	}
	select {
	case <-bridgeContextDone:
	case <-time.After(time.Second):
		t.Fatal("manager shutdown did not cancel the persistent bridge context")
	}
}

func forwardV2LifecycleRoundTrip(
	ctx context.Context,
	client vmmdpb.VmmdClient,
	instance, body string,
) (int32, string, error) {
	stream, err := client.ForwardHTTPStream(ctx)
	if err != nil {
		return 0, "", err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{
		Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{
			Instance:   instance,
			Method:     http.MethodPost,
			RequestUri: "/lifecycle",
			Headers: []*vmmdpb.Header{
				{Name: "Content-Type", Value: "text/plain"},
				{Name: "Host", Value: "lifecycle.test"},
			},
			Port:   8080,
			Stream: true,
		}},
	}); err != nil {
		return 0, "", err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{
		Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: []byte(body)},
	}); err != nil {
		return 0, "", err
	}
	if err := stream.CloseSend(); err != nil {
		return 0, "", err
	}

	var status int32
	var response []byte
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return status, string(response), nil
		}
		if err != nil {
			return 0, "", err
		}
		if init := frame.GetInit(); init != nil {
			status = init.GetStatus()
		}
		if chunk := frame.GetBodyChunk(); len(chunk) != 0 {
			response = append(response, chunk...)
		}
	}
}
