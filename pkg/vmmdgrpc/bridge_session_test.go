package vmmdgrpc

// adr: 20261017 connection survival, §2 one bridge per instance (P1b)

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/instancebridge"
)

// sessionBridge serves sessions for guests on 127.0.0.1, as the instance
// bridge does, and returns its socket path.
func sessionBridge(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ibs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "data.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var d net.Dialer
	httpLn := instancebridge.Split(ctx, ln, &instancebridge.Sessions{GuestIP: "127.0.0.1", Dial: d.DialContext})
	srv := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(httpLn) }()
	t.Cleanup(func() { cancel(); _ = srv.Close() })
	return path
}

func sessionGuest(t *testing.T, serve func(net.Conn)) uint32 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); serve(c) }()
		}
	}()
	return uint32(ln.Addr().(*net.TCPAddr).Port)
}

// A session reads like the per-connection process: bytes until EOF, then
// Wait reports how it ended.
func TestBridgeSessionLooksLikeTheBridgeProcess(t *testing.T) {
	path := sessionBridge(t)
	port := sessionGuest(t, func(c net.Conn) {
		body, _ := io.ReadAll(c)
		_, _ = c.Write([]byte("echo:" + string(body)))
	})
	s, err := dialBridgeSession(t.Context(), path, bridgeIdentity{}, instancebridge.KindTCP, port)
	if err != nil {
		t.Fatal(err)
	}
	conn := s.conn()
	defer conn.close()
	if _, err := conn.input.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := conn.input.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(conn.output)
	if err != nil || string(got) != "echo:ping" {
		t.Fatalf("output = %q, %v", got, err)
	}
	if err := conn.run.Wait(); err != nil || bridgeFailed(err) {
		t.Fatalf("clean session Wait = %v", err)
	}
}

func TestBridgeSessionReportsAFailedEnd(t *testing.T) {
	path := sessionBridge(t)
	port := sessionGuest(t, func(c net.Conn) {
		_, _ = io.ReadFull(c, make([]byte, 1))
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\ndone")
		if tcp, ok := c.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
	})
	s, err := dialBridgeSession(t.Context(), path, bridgeIdentity{}, instancebridge.KindRaw, port)
	if err != nil {
		t.Fatal(err)
	}
	conn := s.conn()
	defer conn.close()
	go func() {
		chunk := make([]byte, 64*1024)
		for i := 0; i < 64; i++ {
			if _, err := conn.input.Write(chunk); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		_ = conn.input.Close()
	}()
	_, _ = io.ReadAll(conn.output) // EOF at the end, as a process's stdout
	if err := conn.run.Wait(); !bridgeFailed(err) || conn.stderr() == "" {
		t.Fatalf("failed session Wait = %v, stderr %q", err, conn.stderr())
	}
}

func TestBridgeSessionDialFailures(t *testing.T) {
	path := sessionBridge(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := uint32(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	// Raw: the session is returned already ended, the dial error as its
	// stderr, as the raw process reported it.
	s, err := dialBridgeSession(t.Context(), path, bridgeIdentity{}, instancebridge.KindRaw, closed)
	if err != nil {
		t.Fatal(err)
	}
	conn := s.conn()
	defer conn.close()
	if got, _ := io.ReadAll(conn.output); len(got) != 0 || !bridgeFailed(conn.run.Wait()) || conn.stderr() == "" {
		t.Fatalf("raw dial failure: output %q, stderr %q", got, conn.stderr())
	}
	// TCP and UDP: the open fails, as their process readiness did.
	if _, err := dialBridgeSession(t.Context(), path, bridgeIdentity{}, instancebridge.KindTCP, closed); status.Code(err) != codes.Unavailable {
		t.Fatalf("TCP dial failure = %v, want Unavailable", err)
	}
}

// A bridge from before the session protocol answers HTTP; the connection
// falls back to its own process.
func TestBridgeSessionOnABridgeWithoutSessions(t *testing.T) {
	dir, err := os.MkdirTemp("", "ibs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "data.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if _, err := dialBridgeSession(t.Context(), path, bridgeIdentity{}, instancebridge.KindTCP, 80); !errors.Is(err, errSessionsUnsupported) {
		t.Fatalf("open on an HTTP-only bridge = %v, want unsupported", err)
	}
}

// The session context (the client, or the instance's teardown) ends the
// session, as it killed the process.
func TestBridgeSessionEndsWithItsContext(t *testing.T) {
	path := sessionBridge(t)
	port := sessionGuest(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	ctx, cancel := context.WithCancel(t.Context())
	s, err := dialBridgeSession(ctx, path, bridgeIdentity{}, instancebridge.KindTCP, port)
	if err != nil {
		t.Fatal(err)
	}
	conn := s.conn()
	defer conn.close()
	done := make(chan error, 1)
	go func() { _, _ = io.ReadAll(conn.output); done <- conn.run.Wait() }()
	cancel()
	select {
	case err := <-done:
		if !bridgeFailed(err) {
			t.Fatalf("Wait after cancel = %v, want a failed end", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session outlived its context")
	}
}

func TestBridgeFailedClassifiesProcessAndSessionEnds(t *testing.T) {
	exitErr := exec.Command("false").Run()
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{io.EOF, false},
		{exitErr, true},
		{&instancebridge.SessionError{Code: instancebridge.CodeIO}, true},
	} {
		if got := bridgeFailed(tc.err); got != tc.want {
			t.Errorf("bridgeFailed(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// A session takes any live bridge of the instance, whatever port started
// it; with none it starts one with what the instance's HTTP traffic
// recorded, so a later HTTP request does not replace it.
func TestStreamBridgeManagerAcquireSession(t *testing.T) {
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)
	var starts atomic.Int32
	var ports []uint32
	manager := newTestStreamBridgeManager(t, func() { starts.Add(1) })
	spawn := manager.spawn
	manager.spawn = func(ctx context.Context, path, netnsName string, id bridgeIdentity, sock, ip string, port uint32, deadline string, env []string) (*exec.Cmd, *bytes.Buffer, error) {
		ports = append(ports, port)
		return spawn(ctx, path, netnsName, id, sock, ip, port, deadline, env)
	}
	ctx := context.Background()
	httpLease, err := manager.acquire(ctx, &vmmdpb.ForwardHTTPRequestInit{Instance: "inst-s", Port: 8080, AppProtocol: "grpc"}, "fc-inst-s", bridgeIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	httpLease.release()
	session, err := manager.acquireSession(ctx, "inst-s", "fc-inst-s", bridgeIdentity{}, 5432)
	if err != nil {
		t.Fatal(err)
	}
	session.release()
	if starts.Load() != 1 {
		t.Fatalf("a session on another port started %d bridges, want the HTTP one reused", starts.Load())
	}

	manager.suspend(ctx, "inst-s") // the bridge is gone; its start is remembered
	session, err = manager.acquireSession(ctx, "inst-s", "fc-inst-s", bridgeIdentity{}, 5432)
	if err != nil {
		t.Fatal(err)
	}
	session.release()
	again, err := manager.acquire(ctx, &vmmdpb.ForwardHTTPRequestInit{Instance: "inst-s", Port: 8080, AppProtocol: "grpc"}, "fc-inst-s", bridgeIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	again.release()
	if starts.Load() != 2 || len(ports) != 2 || ports[1] != 8080 {
		t.Fatalf("starts = %d on ports %v, want the session's restart on the HTTP port and reused by HTTP", starts.Load(), ports)
	}
}

// sessionVMM is a live instance for the forward handlers.
type sessionVMM struct{ stubVMM }

func (sessionVMM) NetnsFor(instance string) (string, bool) { return "fc-" + instance, true }
func (sessionVMM) InstancePID(string) (int, bool)          { return os.Getpid(), true }

// HTTP and upgrade forwards need a node-owned admission permit (ADR-570).
func (sessionVMM) AcquireHTTPForward(ctx context.Context, _ string) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

// sessionServer is a vmmd server whose instance bridges are served in this
// process on a temporary socket, with sessions on; the per-connection
// bridge binaries do not exist, so a process spawn would fail the RPC.
func sessionServer(t *testing.T) (vmmdpb.VmmdClient, *atomic.Int32) {
	t.Helper()
	t.Setenv(instanceBridgeSessionsEnv, "1")
	t.Setenv(tcpBridgePathEnv, "/nonexistent/vmmd-tcp-bridge")
	t.Setenv(rawBridgePathEnv, "/nonexistent/vmmd-raw-bridge")
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)
	dir, err := os.MkdirTemp("", "ibh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	server := New(sessionVMM{}, nil, "test", nil)
	m := server.streamBridges
	m.reapInterval = time.Hour
	m.socketPath = func(key string, _ bridgeIdentity) string { return filepath.Join(dir, key+".sock") }
	var spawns atomic.Int32
	m.spawn = func(bridgeCtx context.Context, _, _ string, _ bridgeIdentity, sock, _ string, _ uint32, _ string, _ []string) (*exec.Cmd, *bytes.Buffer, error) {
		spawns.Add(1)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			return nil, nil, err
		}
		ctx, cancel := context.WithCancel(bridgeCtx)
		var d net.Dialer
		httpLn := instancebridge.Split(ctx, ln, &instancebridge.Sessions{GuestIP: "127.0.0.1", Dial: d.DialContext})
		srv := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(httpLn) }()
		t.Cleanup(func() { cancel(); _ = srv.Close() })
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		return cmd, &bytes.Buffer{}, nil
	}
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() { grpcServer.Stop(); _ = server.Close(context.Background()) })
	client, err := grpc.NewClient("passthrough:///session-test", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return vmmdpb.NewVmmdClient(client), &spawns
}

// ForwardTCPStream carries a connection through a session on the
// instance's bridge, and later connections reuse that bridge.
func TestForwardTCPStreamRunsAsAnInstanceBridgeSession(t *testing.T) {
	client, spawns := sessionServer(t)
	port := sessionGuest(t, func(c net.Conn) {
		body, _ := io.ReadAll(c)
		_, _ = c.Write([]byte("echo:" + string(body)))
	})
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := client.ForwardTCPStream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(&vmmdpb.ForwardTCPRequest{Frame: &vmmdpb.ForwardTCPRequest_Init{Init: &vmmdpb.ForwardTCPRequestInit{Instance: "inst-tcp", Port: port}}}); err != nil {
			t.Fatal(err)
		}
		if first, err := stream.Recv(); err != nil || first.GetInit() == nil || first.GetInit().GetError() != "" {
			t.Fatalf("init = %v, %v", first, err)
		}
		_ = stream.Send(&vmmdpb.ForwardTCPRequest{Frame: &vmmdpb.ForwardTCPRequest_BodyChunk{BodyChunk: []byte("ping")}})
		_ = stream.CloseSend()
		var got []byte
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("recv: %v", err)
			}
			got = append(got, frame.GetBodyChunk()...)
		}
		cancel()
		if string(got) != "echo:ping" {
			t.Fatalf("connection %d carried %q", i, got)
		}
	}
	if spawns.Load() != 1 {
		t.Fatalf("bridges started = %d, want one shared by both connections", spawns.Load())
	}
}

// ForwardRawStream carries an upgrade through a session: the head comes
// back as the init frame, the rest as body.
func TestForwardRawStreamRunsAsAnInstanceBridgeSession(t *testing.T) {
	client, _ := sessionServer(t)
	port := sessionGuest(t, func(c net.Conn) {
		_, _ = bufio.NewReader(c).ReadString('\n')
		_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\nhello")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.ForwardRawStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&vmmdpb.ForwardRawRequest{Frame: &vmmdpb.ForwardRawRequest_Init{Init: &vmmdpb.ForwardRawRequestInit{Instance: "inst-raw", Port: port}}}); err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&vmmdpb.ForwardRawRequest{Frame: &vmmdpb.ForwardRawRequest_BodyChunk{BodyChunk: []byte("GET /ws HTTP/1.1\r\nUpgrade: websocket\r\n\r\n")}})
	_ = stream.CloseSend()
	first, err := stream.Recv()
	if err != nil || first.GetInit().GetStatus() != 101 || first.GetInit().GetError() != "" {
		t.Fatalf("init = %v, %v", first, err)
	}
	var got []byte
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		got = append(got, frame.GetBodyChunk()...)
	}
	if string(got) != "hello" {
		t.Fatalf("body = %q", got)
	}
}

// ForwardUDPStream carries datagrams through a session; ending the RPC
// while the response side still reads must not race the session's drain.
func TestForwardUDPStreamRunsAsAnInstanceBridgeSession(t *testing.T) {
	client, _ := sessionServer(t)
	t.Setenv(udpBridgePathEnv, "/nonexistent/vmmd-udp-bridge")
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guest.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := guest.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = guest.WriteToUDP(append([]byte("pong:"), buf[:n]...), from)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.ForwardUDPStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port := uint32(guest.LocalAddr().(*net.UDPAddr).Port)
	if err := stream.Send(&vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Init{Init: &vmmdpb.ForwardUDPRequestInit{Instance: "inst-udp", Port: port}}}); err != nil {
		t.Fatal(err)
	}
	if first, err := stream.Recv(); err != nil || first.GetInit() == nil {
		t.Fatalf("init = %v, %v", first, err)
	}
	if err := stream.Send(&vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Datagram{Datagram: []byte("ping")}}); err != nil {
		t.Fatal(err)
	}
	reply, err := stream.Recv()
	if err != nil || string(reply.GetDatagram()) != "pong:ping" {
		t.Fatalf("datagram = %v, %v", reply, err)
	}
	_ = stream.CloseSend()
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
	}
}
