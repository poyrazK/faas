package e2e_test

// This is a CI-safe end-to-end test for the raw TCP ingress data path. It
// keeps the control-plane store and vmmd transport real while replacing only
// the guest-side bridge with an in-process vmmd gRPC server. The test therefore
// exercises the same composition used by gatewayd-public:
//
//   public TCP socket -> tcpd supervisor/resolvers -> gateway TCPForwarder
//   -> vmmd ForwardTCPStream -> guest bytes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const tcpIngressBufSize = 1 << 20

// TestTCPIngressE2E proves the public listener, durable route lookup,
// running-instance selection, protocol-neutral vmmd stream, and byte
// round-trip as one path. It does not require KVM, Firecracker, or Postgres.
func TestTCPIngressE2E(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tcp-ingress-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "tcp-ingress-" + uuid.NewString(),
		Status:    state.AppActive,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "deployment-1", string(state.StateRunning), 256, "node-1", "wake-1"); err != nil {
		t.Fatalf("create running instance: %v", err)
	}

	publicListener, publicPort := listenTCPIngressPort(t)
	t.Cleanup(func() { _ = publicListener.Close() })
	if _, err := store.CreateTCPListener(ctx, state.TCPListener{
		AccountID:    account.ID,
		AppID:        app.ID,
		ListenerName: "echo",
		GuestPort:    5432,
		PublicPort:   publicPort,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create TCP listener: %v", err)
	}

	vmmdListener := bufconn.Listen(tcpIngressBufSize)
	vmmdServer := grpc.NewServer()
	guest := &tcpIngressVMMDServer{init: make(chan *vmmdpb.ForwardTCPRequestInit, 1)}
	vmmdpb.RegisterVmmdServer(vmmdServer, guest)
	go func() { _ = vmmdServer.Serve(vmmdListener) }()
	t.Cleanup(func() {
		vmmdServer.Stop()
		_ = vmmdListener.Close()
	})

	vmmdConn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return vmmdListener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial fake vmmd: %v", err)
	}
	t.Cleanup(func() { _ = vmmdConn.Close() })

	supervisor := &tcpd.Supervisor{
		BindHost: "127.0.0.1",
		Source:   store,
		Routes:   tcpd.ListenerStoreResolver{Store: store},
		Targets:  &tcpd.StoreTargetResolver{Instances: store},
		Forwarder: gateway.TCPForwarder{
			Nodes: tcpIngressNodeLookup{client: vmmdpb.NewVmmdClient(vmmdConn)},
		},
		RefreshInterval: 10 * time.Millisecond,
		Listen: func(string, string) (net.Listener, error) {
			// Keep the selected 40xxx listener open while the supervisor
			// reconciles durable state, then hand that exact socket to it.
			return publicListener, nil
		},
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- supervisor.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("supervisor shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("supervisor did not stop")
		}
	})

	conn, err := net.DialTimeout("tcp", publicListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial public TCP listener: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	payload := []byte("startup packet: raw TCP survives HTTP framing\n")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write public TCP payload: %v", err)
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.CloseWrite(); err != nil {
			t.Fatalf("half-close public TCP connection: %v", err)
		}
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read echoed TCP payload: %v", err)
	}
	if diff := cmp.Diff(payload, got); diff != "" {
		t.Fatalf("echoed payload mismatch (-want +got):\n%s", diff)
	}

	select {
	case init := <-guest.init:
		if init.GetInstance() == "" || init.GetInstance() == "unknown" {
			t.Fatalf("vmmd init instance = %q, want selected running instance", init.GetInstance())
		}
		if got := init.GetPort(); got != 5432 {
			t.Fatalf("vmmd init guest port = %d, want 5432", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fake vmmd did not receive TCP init")
	}

	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("supervisor stopped unexpectedly: %v", err)
		}
	default:
	}
}

// listenTCPIngressPort reserves an allowed durable public port before the
// supervisor starts. Keeping the socket open avoids a race with other tests.
func listenTCPIngressPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	for port := state.TCPListenerPublicPortMin; port <= state.TCPListenerPublicPortMax; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return listener, port
		}
	}
	t.Fatal("no free TCP listener port in the durable 40000..49999 range")
	return nil, 0
}

type tcpIngressNodeLookup struct {
	client vmmdpb.VmmdClient
}

func (l tcpIngressNodeLookup) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	return l.client, nil, l.client != nil
}

type tcpIngressVMMDServer struct {
	vmmdpb.UnimplementedVmmdServer
	init chan *vmmdpb.ForwardTCPRequestInit
}

func (s *tcpIngressVMMDServer) ForwardTCPStream(stream vmmdpb.Vmmd_ForwardTCPStreamServer) error {
	frame, err := stream.Recv()
	if err != nil {
		return err
	}
	init := frame.GetInit()
	if init == nil {
		return errors.New("first TCP frame was not init")
	}
	s.init <- init
	if err := stream.Send(&vmmdpb.ForwardTCPResponse{
		Frame: &vmmdpb.ForwardTCPResponse_Init{Init: &vmmdpb.ForwardTCPResponseInit{}},
	}); err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if body := frame.GetBodyChunk(); len(body) > 0 {
			if err := stream.Send(&vmmdpb.ForwardTCPResponse{
				Frame: &vmmdpb.ForwardTCPResponse_BodyChunk{BodyChunk: append([]byte(nil), body...)},
			}); err != nil {
				return err
			}
		}
	}
}
