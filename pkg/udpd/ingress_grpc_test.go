package udpd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/udpd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type udpGRPCNodes struct{ client vmmdpb.VmmdClient }

func (n udpGRPCNodes) ClientFor(_ context.Context, node string) (vmmdpb.VmmdClient, io.Closer, bool) {
	return n.client, nil, node == "node"
}

type udpGRPCGuest struct {
	vmmdpb.UnimplementedVmmdServer
	started chan *vmmdpb.ForwardUDPRequestInit
	stopped chan struct{}
}

func (g *udpGRPCGuest) ForwardUDPStream(stream vmmdpb.Vmmd_ForwardUDPStreamServer) error {
	defer func() { g.stopped <- struct{}{} }()
	frame, err := stream.Recv()
	if err != nil {
		return err
	}
	init := frame.GetInit()
	if init == nil {
		return errors.New("missing UDP init")
	}
	g.started <- init
	if err := stream.Send(&vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Init{Init: &vmmdpb.ForwardUDPResponseInit{}}}); err != nil {
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
		datagram, ok := frame.Frame.(*vmmdpb.ForwardUDPRequest_Datagram)
		if !ok {
			return errors.New("missing UDP datagram presence")
		}
		response := &vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Datagram{Datagram: datagram.Datagram}}
		if string(datagram.Datagram) == "bad-frame" {
			response = &vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Init{Init: &vmmdpb.ForwardUDPResponseInit{}}}
		}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

type udpGRPCAdmitter struct {
	store *state.MemStore
	calls atomic.Int32
}

func (a *udpGRPCAdmitter) AdmitInstance(ctx context.Context, app, deployment, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	a.calls.Add(1)
	if deployment == "" || scope != "" || trigger != "gateway" {
		return "", "", "", "", 0, false, 0, errors.New("incorrect admission context")
	}
	instance, err := a.store.CreateInstance(ctx, app, deployment, string(state.StateRunning), 256, "node", "wake")
	return instance.ID, "node", deployment, "wake", 0, false, 8080, err
}

// This composes real public sockets, in-memory intents, target selection and
// protobuf gRPC transport. Scheduler RPC and guest namespace execution are
// substituted; native acceptance covers those boundaries.
func TestUDPIngressGRPCAdmissionAndDisable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "udp-grpc@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "udp-grpc", Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "echo", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
	if err != nil {
		t.Fatal(err)
	}
	var socket *net.UDPConn
	for port := api.UDPListenerPublicPortMin; port <= api.UDPListenerPublicPortMax; port++ {
		socket, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal("no free public UDP test port")
	}
	defer socket.Close()
	publicPort := socket.LocalAddr().(*net.UDPAddr).Port
	intent, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: publicPort, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	rpc := grpc.NewServer()
	guest := &udpGRPCGuest{started: make(chan *vmmdpb.ForwardUDPRequestInit, 4), stopped: make(chan struct{}, 4)}
	vmmdpb.RegisterVmmdServer(rpc, guest)
	go func() { _ = rpc.Serve(listener) }()
	defer rpc.Stop()
	defer listener.Close()
	connection, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := store.CreateDeployment(ctx, state.Deployment{ID: "deployment", AppID: app.ID, Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	admit := &udpGRPCAdmitter{store: store}
	resolver := &udpd.StoreTargetResolver{Store: store, Admitter: admit}
	errorsReported := make(chan error, 4)
	ready := make(chan struct{})
	done := make(chan error, 1)
	var initialBind atomic.Bool
	supervisor := &udpd.Supervisor{BindHost: "127.0.0.1", Source: store, ResolveTarget: resolver.ResolveTarget, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, RefreshInterval: 5 * time.Millisecond, OnReady: func() { close(ready) }, OnError: func(err error) { errorsReported <- err }, Forwarder: gateway.UDPForwarder{Nodes: udpGRPCNodes{vmmdpb.NewVmmdClient(connection)}, IdleTimeout: time.Minute}, Listen: func(_ string, address *net.UDPAddr) (*net.UDPConn, error) {
		if address.Port != publicPort {
			return nil, errors.New("wrong public port")
		}
		if initialBind.CompareAndSwap(false, true) {
			return socket, nil
		}
		return net.ListenUDP("udp4", address)
	}}
	go func() { done <- supervisor.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("supervisor shutdown hung")
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("supervisor startup: %v", err)
	case <-time.After(time.Second):
		t.Fatal("supervisor not ready")
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort))
	first, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	roundTrip := func(client *net.UDPConn, payload []byte) {
		t.Helper()
		if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.WriteMsgUDP(payload, nil, nil); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, api.UDPDatagramMaxBytes)
		n, err := client.Read(buffer)
		if err != nil || !bytes.Equal(buffer[:n], payload) {
			t.Fatalf("UDP round trip: got=%d want=%d err=%v", n, len(payload), err)
		}
	}
	roundTrip(first, []byte{0, 255, 128, 10})
	second, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	roundTrip(second, []byte("independent-peer"))
	roundTrip(first, nil)
	roundTrip(second, bytes.Repeat([]byte{0, 255}, 4096))
	if admit.calls.Load() != 1 {
		t.Fatalf("admission calls=%d, want one wake", admit.calls.Load())
	}
	for i := 0; i < 2; i++ {
		select {
		case init := <-guest.started:
			if init.Port != 5353 || init.Instance == "" || init.MaxBytes != api.UDPStreamMaxBytes || init.MaxDatagrams != api.UDPStreamMaxDatagrams {
				t.Fatalf("UDP transport init=%+v", init)
			}
		case <-time.After(time.Second):
			t.Fatal("missing guest stream")
		}
	}
	// A malformed guest response closes only its originating peer session.
	bad, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if _, err := bad.Write([]byte("bad-frame")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errorsReported:
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("malformed response error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("malformed response was not rejected")
	}
	select {
	case <-guest.stopped:
	case <-time.After(time.Second):
		t.Fatal("malformed-response stream retained")
	}
	select {
	case init := <-guest.started:
		if init.Port != 5353 {
			t.Fatalf("failed peer guest port=%d", init.Port)
		}
	case <-time.After(time.Second):
		t.Fatal("missing failed-peer stream")
	}
	roundTrip(first, []byte("still-alive-one"))
	roundTrip(second, []byte("still-alive-two"))
	if admit.calls.Load() != 1 {
		t.Fatalf("failed peer caused extra admission: %d", admit.calls.Load())
	}
	// Reassign a live public endpoint to a different app. Existing peer streams
	// must end before traffic from the same client can enter the new app.
	replacement, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "udp-replacement", Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "echo", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{ID: "replacement-deployment", AppID: replacement.ID, Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUDPListener(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	intent, err = store.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: replacement.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: publicPort, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-guest.stopped:
		case <-time.After(time.Second):
			t.Fatal("reassignment retained an old app stream")
		}
	}
	// Allow reconciliation to finish binding before sending the new request.
	deadline := time.Now().Add(time.Second)
	for {
		_ = first.SetDeadline(time.Now().Add(50 * time.Millisecond))
		_, _ = first.Write([]byte("replacement"))
		buffer := make([]byte, 64)
		n, readErr := first.Read(buffer)
		if readErr == nil && string(buffer[:n]) == "replacement" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replacement endpoint did not recover: %v", readErr)
		}
	}
	roundTrip(second, []byte("replacement-second"))
	for i := 0; i < 2; i++ {
		select {
		case init := <-guest.started:
			instance, lookupErr := store.InstanceByID(ctx, init.Instance)
			if lookupErr != nil || instance.AppID != replacement.ID {
				t.Fatalf("reassigned stream reached wrong app: instance=%+v err=%v", instance, lookupErr)
			}
		case <-time.After(time.Second):
			t.Fatal("missing replacement stream")
		}
	}
	if admit.calls.Load() != 2 {
		t.Fatalf("reassignment admission calls=%d, want two apps", admit.calls.Load())
	}
	if _, err := store.SetUDPListenerEnabled(ctx, intent.ID, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-guest.stopped:
		case <-time.After(time.Second):
			t.Fatal("disable retained guest gRPC stream")
		}
	}
	// A fresh bind is stronger evidence than an unanswered UDP packet.
	deadline = time.Now().Add(time.Second)
	for {
		probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: publicPort})
		if err == nil {
			_ = probe.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disabled endpoint %s still bound: %v", address, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
