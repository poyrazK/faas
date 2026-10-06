package vmmdgrpc

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/udpwire"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func udpGRPCFixture(t *testing.T) (vmmdpb.VmmdClient, *wire.OpsMetrics) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	ops := wire.NewOpsMetrics("vmmd")
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, New(stubVMM{}, ops, "1.10.0", nil))
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-done })
	connection, err := grpc.NewClient("passthrough:///udp-test", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return vmmdpb.NewVmmdClient(connection), ops
}

// Use the actual registered RPC and protobuf transport: invalid initialization
// must fail before helper launch and count as an error rather than success.
func TestUDPStreamGRPCRejectsInitAndReportsErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request *vmmdpb.ForwardUDPRequest
		want    codes.Code
	}{
		{"missing frame", &vmmdpb.ForwardUDPRequest{}, codes.InvalidArgument},
		{"datagram first", &vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Datagram{}}, codes.InvalidArgument},
		{"missing instance", udpInitRequest("", 5353, 0), codes.InvalidArgument},
		{"zero port", udpInitRequest("instance", 0, 0), codes.InvalidArgument},
		{"oversized port", udpInitRequest("instance", 65536, 0), codes.InvalidArgument},
		{"negative cap", udpInitRequest("instance", 5353, -1), codes.InvalidArgument},
		{"not live", udpInitRequest("instance", 5353, 0), codes.NotFound},
		{"EOF before init", nil, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, ops := udpGRPCFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := client.ForwardUDPStream(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if tc.request != nil {
				if err := stream.Send(tc.request); err != nil {
					t.Fatal(err)
				}
			} else if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != tc.want {
				t.Fatalf("RPC error=%v, want %s", err, tc.want)
			}
			waitUDPErrorMetric(t, ops)
		})
	}
}

func udpInitRequest(instance string, port uint32, maxBytes int64) *vmmdpb.ForwardUDPRequest {
	return &vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Init{Init: &vmmdpb.ForwardUDPRequestInit{Instance: instance, Port: port, MaxBytes: maxBytes}}}
}

func waitUDPErrorMetric(t *testing.T, ops *wire.OpsMetrics) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		body := scrapeOps(t, ops)
		if strings.Contains(body, `vmmd_ops_total{code="err",op="ForwardUDPStream"} 1`) {
			if strings.Contains(body, `vmmd_ops_total{code="ok",op="ForwardUDPStream"} 1`) {
				t.Fatal("failed RPC also counted as success")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed RPC not recorded: %s", body)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUDPStreamGRPCDeadlineBeforeInit(t *testing.T) {
	client, ops := udpGRPCFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.ForwardUDPStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Leave the server awaiting its first message. The caller deadline must
	// release the handler without launching a helper.
	if _, err := stream.Recv(); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("RPC error=%v", err)
	}
	waitUDPErrorMetric(t, ops)
}

type udpReceiveFailureStream struct {
	udpTestStream
	ctx context.Context
	err error
}

func (s *udpReceiveFailureStream) Context() context.Context                 { return s.ctx }
func (s *udpReceiveFailureStream) Recv() (*vmmdpb.ForwardUDPRequest, error) { return nil, s.err }

func TestUDPStreamPreservesInitialReceiveFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			ops := wire.NewOpsMetrics("vmmd")
			server := New(stubVMM{}, ops, "1.10.0", nil)
			stream := &udpReceiveFailureStream{ctx: context.Background(), err: status.Error(code, "transport failure")}
			if err := server.ForwardUDPStream(stream); status.Code(err) != code {
				t.Fatalf("status changed: %v", err)
			}
			waitUDPErrorMetric(t, ops)
		})
	}
}

// udpFlowControlServer uses the production duplex pump and real HTTP/2 flow
// control, with pipes in place of the privileged namespace helper. It exercises
// transport shutdown without claiming native namespace/process qualification.
type udpFlowControlServer struct {
	vmmdpb.UnimplementedVmmdServer
	writer      *io.PipeWriter
	reader      *io.PipeReader
	handlerDone chan error
	sendFailed  chan error
	inSend      atomic.Bool
	sends       atomic.Int64
}

func (s *udpFlowControlServer) ForwardUDPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Init{Init: &vmmdpb.ForwardUDPResponseInit{}}}); err != nil {
		return err
	}
	defer func() { _ = s.writer.Close() }()
	defer func() { _ = s.reader.Close() }()
	err = udpBridgeFrames(&udpFlowControlStream{BidiStreamingServer: stream, owner: s}, io.Discard, s.reader, first.GetInit())
	s.handlerDone <- err
	return err
}

type udpFlowControlStream struct {
	grpc.BidiStreamingServer[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse]
	owner *udpFlowControlServer
}

func (s *udpFlowControlStream) Send(frame *vmmdpb.ForwardUDPResponse) error {
	s.owner.inSend.Store(true)
	err := s.BidiStreamingServer.Send(frame)
	s.owner.inSend.Store(false)
	if err != nil {
		s.owner.sendFailed <- err
	} else {
		s.owner.sends.Add(1)
	}
	return err
}

func TestUDPStreamRequestFailureReleasesBlockedGRPCReply(t *testing.T) {
	reader, writer := io.Pipe()
	fixture := &udpFlowControlServer{reader: reader, writer: writer, handlerDone: make(chan error, 1), sendFailed: make(chan error, 1)}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, fixture)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = reader.Close(); _ = writer.Close(); _ = listener.Close(); <-done })
	connection, err := grpc.NewClient("passthrough:///udp-flow-control", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := vmmdpb.NewVmmdClient(connection).ForwardUDPStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(udpInitRequest("instance", 5353, 0)); err != nil {
		t.Fatal(err)
	}
	if ready, err := stream.Recv(); err != nil || ready.GetInit() == nil {
		t.Fatalf("ready=%v, error=%v", ready, err)
	}
	guestDone := make(chan error, 1)
	go func() {
		payload := make([]byte, api.UDPDatagramMaxBytes)
		for {
			if err := udpwire.Write(writer, payload); err != nil {
				guestDone <- err
				return
			}
		}
	}()
	t.Cleanup(func() { _ = writer.Close(); <-guestDone })
	// Stop receiving responses and wait until one actual Send is stalled.
	// Stable send count while in Send proves backpressure rather than merely
	// assuming that some fixed number of datagrams fills a transport window.
	deadline := time.Now().Add(2 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		count := fixture.sends.Load()
		if fixture.inSend.Load() {
			time.Sleep(20 * time.Millisecond)
			if fixture.inSend.Load() && fixture.sends.Load() == count {
				blocked = true
				break
			}
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("could not establish real gRPC reply backpressure")
	}
	if err := stream.Send(udpInitRequest("instance", 5353, 0)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fixture.handlerDone:
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("request error lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request failure could not terminate blocked reply handler")
	}
	select {
	case <-fixture.sendFailed:
	case <-time.After(time.Second):
		t.Fatal("gRPC did not release the blocked reply sender after handler return")
	}
}
