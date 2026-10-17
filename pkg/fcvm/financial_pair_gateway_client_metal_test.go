//go:build linux && metal

package fcvm_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const pairPayloadSize = 64 * 1024

type financialPairGateway struct {
	fixture *fcvm.FinancialPairTrafficFixture
	nodes   *gateway.NodeClientCache
	server  *httptest.Server
}

func newFinancialPairGateway(t *testing.T) *financialPairGateway {
	t.Helper()
	if os.Getenv("FAAS_FINANCIAL_PAIR") != "1" {
		t.Skip("requires the guarded physical two-host financial runner")
	}
	options, clientTLS := financialPairTLS(t)
	f := fcvm.NewFinancialPairTrafficFixture(t, options)
	g := &financialPairGateway{fixture: f}
	gateway.SetNodeResolver(func(ctx context.Context, id string) (string, bool) {
		node, err := f.Store().ComputeNodeByID(ctx, id)
		return node.TargetURL, err == nil && node.Active && id == f.Node()
	})
	t.Cleanup(func() { gateway.SetNodeResolver(func(context.Context, string) (string, bool) { return "", false }) })
	g.nodes = gateway.NewNodeClientCache(func(ctx context.Context, target string) (*grpc.ClientConn, error) {
		return wire.DialContext(ctx, target, clientTLS)
	}, nil)
	t.Cleanup(func() { _ = g.nodes.Close() })
	f.PublishTarget(t)
	f.Call(t, "wake")
	handler := http.HandlerFunc(g.serveHTTP)
	g.server = httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	g.server.Config.Protocols = protocols
	g.server.Start()
	t.Cleanup(g.server.Close)
	t.Cleanup(func() { f.DatabaseAvailable(true) })
	return g
}

func (g *financialPairGateway) target() gateway.Target {
	return gateway.Target{AppID: g.fixture.App(), NodeID: g.fixture.Node(), InstanceID: g.fixture.Instance(), Port: 9000}
}

// The fixture supplies a previously admitted target. The production transport
// and SQL-backed node resolver are real; API/scheduler admission is qualified
// separately by the full-daemon lane. Public headers cannot select private.
func (g *financialPairGateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	target := g.target()
	r.Header.Set("x-faas-instance", target.InstanceID)
	r.Header.Set("x-faas-protocol", "http1")
	switch {
	case strings.HasPrefix(r.URL.Path, "/grpc/"):
		target.Port = 9002
		r.Header.Set("x-faas-protocol", "grpc")
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/grpc")
	case r.URL.Path == "/sidecar8080":
		r = r.WithContext(gateway.WithSidecarPort(r.Context(), 8080))
	case r.URL.Path == "/sidecar9001":
		r = r.WithContext(gateway.WithSidecarPort(r.Context(), 9001))
	}
	if r.URL.Path == "/upgrade" {
		gateway.ForwardingRawReverseProxy(g.nodes, nil, nil)(target).ServeHTTP(w, r)
		return
	}
	gateway.ForwardingReverseProxyWithEvents(g.nodes, nil, nil)(target).ServeHTTP(w, r)
}

type pairResponse struct {
	body   []byte
	chunks int
	port   string
	err    error
}

func (g *financialPairGateway) http(ctx context.Context, path string) pairResponse {
	return g.httpAt(ctx, g.server.URL+path)
}

func (g *financialPairGateway) httpAt(ctx context.Context, url string) pairResponse {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return pairResponse{err: err}
	}
	// Attempted route-metadata injection must remain a public request.
	req.Header.Set("gregale-private-forward", "1")
	response, err := g.server.Client().Do(req)
	if err != nil {
		return pairResponse{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return pairResponse{err: fmt.Errorf("gateway HTTP %d", response.StatusCode)}
	}
	result := pairResponse{port: response.Header.Get("X-Financial-Guest-Port")}
	buffer := make([]byte, 4096)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			result.body = append(result.body, buffer[:n]...)
			result.chunks++
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				result.err = readErr
			}
			return result
		}
		if len(result.body) > pairPayloadSize {
			result.err = errors.New("unexpected oversized guest reply")
			return result
		}
	}
}

func (g *financialPairGateway) grpcPayload(ctx context.Context) ([]byte, error) {
	// The public HTTP listener accepts H2C. This generated-message RPC crosses
	// the gateway's real gRPC-aware HTTP transport and the guest's gRPC server.
	conn, err := grpc.NewClient("passthrough:///"+g.server.Listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	var response wrapperspb.BytesValue
	err = conn.Invoke(ctx, "/grpc/financial.fixture.Payload/Read", &emptypb.Empty{}, &response)
	if err == nil && !bytes.Equal(response.Value, bytes.Repeat([]byte{byte(9002 % 251)}, pairPayloadSize)) {
		err = errors.New("gRPC payload identity/size changed")
	}
	return response.Value, err
}

func (g *financialPairGateway) upgrade(ctx context.Context) ([]byte, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", g.server.Listener.Addr().String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, "GET /upgrade HTTP/1.1\r\nHost: fixture.example\r\nConnection: Upgrade\r\nUpgrade: financial-echo\r\n\r\n"); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("gateway upgrade %d", response.StatusCode)
	}
	body := bytes.Repeat([]byte{0x9d}, pairPayloadSize)
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}
	got := make([]byte, len(body))
	n, err := io.ReadFull(reader, got)
	if err == nil && !bytes.Equal(got, body) {
		err = errors.New("upgrade echo changed")
	}
	return got[:n], err
}

func (g *financialPairGateway) tcp(ctx context.Context) ([]byte, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			target := g.target()
			target.Port = 9090
			err = (gateway.TCPForwarder{Nodes: g.nodes, IdleTimeout: time.Second}).ServeConn(ctx, conn, target)
		}
		done <- err
	}()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		<-done
		return nil, err
	}
	defer func() { _ = conn.Close(); cancel(); <-done }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	body := bytes.Repeat([]byte{0x7e}, pairPayloadSize)
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}
	got := make([]byte, len(body))
	n, err := io.ReadFull(conn, got)
	if err == nil && !bytes.Equal(got, body) {
		err = errors.New("TCP echo changed")
	}
	return got[:n], err
}

type financialUDPPeer struct {
	conn    *net.UDPConn
	address *net.UDPAddr
}

func (p *financialUDPPeer) Receive(ctx context.Context) ([]byte, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = p.conn.SetReadDeadline(deadline)
	}
	buffer := make([]byte, 65507)
	n, address, err := p.conn.ReadFromUDP(buffer)
	if err == nil && (address.Port != p.address.Port || !address.IP.Equal(p.address.IP)) {
		return nil, errors.New("unexpected fixture UDP peer")
	}
	return buffer[:n], err
}
func (p *financialUDPPeer) Send(_ context.Context, body []byte) error {
	_, err := p.conn.WriteToUDP(body, p.address)
	return err
}

func (g *financialPairGateway) udp(ctx context.Context) ([]byte, error) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	target := g.target()
	target.Port = 9091
	go func() {
		done <- (gateway.UDPForwarder{Nodes: g.nodes, IdleTimeout: time.Second}).ServePeer(ctx, &financialUDPPeer{conn: listener, address: conn.LocalAddr().(*net.UDPAddr)}, target)
	}()
	defer func() { cancel(); _ = listener.Close(); <-done }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	body := bytes.Repeat([]byte{0x6a}, 4096)
	var got []byte
	for range 16 {
		if _, err := conn.Write(body); err != nil {
			return got, err
		}
		buffer := make([]byte, len(body))
		n, err := conn.Read(buffer)
		if err != nil {
			return got, err
		}
		if !bytes.Equal(buffer[:n], body) {
			return got, errors.New("UDP echo changed")
		}
		got = append(got, buffer[:n]...)
	}
	return got, nil
}
