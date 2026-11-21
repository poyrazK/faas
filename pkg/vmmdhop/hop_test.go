package vmmdhop

// adr: 20261121202814 — the hop must enforce what vmmd's gRPC listener does:
// the same TLS and client verification, the same caller policy, only
// ForwardHTTPStream, and the same status errors.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

const forwardMethod = vmmdpb.Vmmd_ForwardHTTPStream_FullMethodName

type testPKI struct {
	ca    *x509.Certificate
	caKey *ecdsa.PrivateKey
	pool  *x509.CertPool
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return &testPKI{ca: ca, caKey: key, pool: pool}
}

var serial atomic.Int64

func (p *testPKI) leaf(t *testing.T, cn string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1) + 10), Subject: pkix.Name{CommonName: cn},
		DNSNames:  []string{cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// serverTLS mirrors wire's vmmd server config, reload hook included: the
// hook returns a config offering only h2, as production's does.
func (p *testPKI) serverTLS(t *testing.T) *tls.Config {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{p.leaf(t, "vmmd.test", x509.ExtKeyUsageServerAuth)},
		ClientCAs:    p.pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
	}
	live := cfg.Clone()
	cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { return live, nil }
	return cfg
}

func (p *testPKI) clientTLS(t *testing.T, cn string) *tls.Config {
	cfg := &tls.Config{RootCAs: p.pool, ServerName: "vmmd.test", MinVersion: tls.VersionTLS13}
	if cn != "" {
		cfg.Certificates = []tls.Certificate{p.leaf(t, cn, x509.ExtKeyUsageClientAuth)}
	}
	return cfg
}

// fakeVmmd echoes a forward: the response init names the caller's CN and the
// x-test metadata, and the body echoes the request body.
type fakeVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	calls   atomic.Int64
	block   bool
	ended   chan error
	details bool
}

func (f *fakeVmmd) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	f.calls.Add(1)
	ctx := stream.Context()
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if f.details {
		st, _ := status.New(codes.ResourceExhausted, "busy").WithDetails(&errdetails.ErrorInfo{Reason: "concurrency_throttled"})
		return st.Err()
	}
	if f.block {
		<-ctx.Done()
		f.ended <- ctx.Err()
		return ctx.Err()
	}
	cn, _ := wire.PeerCN(ctx)
	md, _ := metadata.FromIncomingContext(ctx)
	var body []byte
	for {
		m, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		body = append(body, m.GetBodyChunk()...)
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
		Status: 200,
		Headers: []*vmmdpb.Header{
			{Name: "X-Peer-Cn", Value: cn},
			{Name: "X-Test", Value: first.GetInit().GetMethod() + "|" + firstOf(md.Get("x-test"))},
		},
	}}}); err != nil {
		return err
	}
	return stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: body}})
}

func firstOf(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

type testVmmd struct {
	addr string
	fake *fakeVmmd
	pki  *testPKI
}

// startVmmd serves fake on one TCP listener the way vmmd does: gRPC with the
// caller policy, and, with hop, the hop in front of it.
func startVmmd(t *testing.T, fake *fakeVmmd, hop bool) *testVmmd {
	t.Helper()
	pki := newTestPKI(t)
	serverCfg := pki.serverTLS(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverCfg)), grpc.ChainStreamInterceptor(vmmdgrpc.CallerPolicyStreamInterceptor()))
	vmmdpb.RegisterVmmdServer(gsrv, fake)
	served := lis
	if hop {
		srv, err := NewServer(ServerConfig{
			TLS: serverCfg, Service: fake, Interceptor: vmmdgrpc.CallerPolicyStreamInterceptor(),
			Streams: StreamHandlers(&vmmdpb.Vmmd_ServiceDesc, "ForwardHTTPStream"),
		})
		if err != nil {
			t.Fatal(err)
		}
		served = NewListener(lis, srv)
	}
	go func() { _ = gsrv.Serve(served) }()
	t.Cleanup(gsrv.Stop)
	return &testVmmd{addr: lis.Addr().String(), fake: fake, pki: pki}
}

func (v *testVmmd) client(t *testing.T, cn string) *Client {
	t.Helper()
	c, err := NewClient(v.addr, v.pki.clientTLS(t, cn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func forward(t *testing.T, ctx context.Context, c *Client, body string) (*vmmdpb.ForwardHTTPResponseInit, string, error) {
	t.Helper()
	s, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](ctx, c, forwardMethod)
	if err != nil {
		return nil, "", err
	}
	_ = s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1", Method: "POST"}}})
	if body != "" {
		_ = s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: []byte(body)}})
	}
	_ = s.CloseSend()
	var init *vmmdpb.ForwardHTTPResponseInit
	var got []byte
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return init, string(got), nil
		}
		if err != nil {
			return init, string(got), err
		}
		if i := m.GetInit(); i != nil {
			init = i
		}
		got = append(got, m.GetBodyChunk()...)
	}
}

func header(init *vmmdpb.ForwardHTTPResponseInit, name string) string {
	for _, h := range init.GetHeaders() {
		if h.GetName() == name {
			return h.GetValue()
		}
	}
	return ""
}

// Security requirement 2: a gateway certificate is served through the caller
// policy, with its CN and the call's metadata visible to the handler; the
// connection is reused for the next call.
func TestHopServesGatewayCertificate(t *testing.T) {
	v := startVmmd(t, &fakeVmmd{}, true)
	c := v.client(t, "gatewayd-internal.faas")
	var dials atomic.Int64
	dial := c.dial
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, address)
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-test", "corr-1")
	for i := range 3 {
		init, body, err := forward(t, ctx, c, "hello")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if init.GetStatus() != 200 || header(init, "X-Peer-Cn") != "gatewayd-internal.faas" || header(init, "X-Test") != "POST|corr-1" || body != "hello" {
			t.Fatalf("call %d: init %v body %q", i, init, body)
		}
	}
	if dials.Load() != 1 {
		t.Fatalf("dials = %d, want 1: a clean exchange returns its connection", dials.Load())
	}
}

// Security requirement 2: certificates the policy does not allow on
// ForwardHTTPStream are refused before the handler runs, as on gRPC.
func TestHopRefusesCallersThePolicyRefuses(t *testing.T) {
	for _, cn := range []string{"builderd.faas", "imaged.faas", "pgproxyd.faas", "stranger.faas"} {
		t.Run(cn, func(t *testing.T) {
			fake := &fakeVmmd{}
			v := startVmmd(t, fake, true)
			_, _, err := forward(t, context.Background(), v.client(t, cn), "")
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("err = %v, want PermissionDenied", err)
			}
			if fake.calls.Load() != 0 {
				t.Fatal("handler ran for a refused caller")
			}
		})
	}
	// schedd may forward, as the policy allows.
	fake := &fakeVmmd{}
	v := startVmmd(t, fake, true)
	if _, _, err := forward(t, context.Background(), v.client(t, "schedd.faas"), ""); err != nil {
		t.Fatalf("schedd: %v", err)
	}
}

// Security requirement 1: no client certificate, no hop; nothing reaches the
// handler.
func TestHopRequiresClientCertificate(t *testing.T) {
	fake := &fakeVmmd{}
	v := startVmmd(t, fake, true)
	_, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](context.Background(), v.client(t, ""), forwardMethod)
	if err == nil {
		// TLS 1.3 reports a refused client certificate on the first read.
		_, _, err = forward(t, context.Background(), v.client(t, ""), "")
	}
	if err == nil {
		t.Fatal("a client without a certificate was served")
	}
	if fake.calls.Load() != 0 {
		t.Fatal("handler ran without a client certificate")
	}
}

// Security requirement 2: only ForwardHTTPStream is served on the hop.
func TestHopRefusesOtherMethods(t *testing.T) {
	fake := &fakeVmmd{}
	v := startVmmd(t, fake, true)
	c := v.client(t, "schedd.faas")
	for _, method := range []string{vmmdpb.Vmmd_Destroy_FullMethodName, vmmdpb.Vmmd_ExecuteAppTaskStream_FullMethodName, "/x.Y/Z"} {
		s, err := NewStream[vmmdpb.DestroyRequest, vmmdpb.DestroyResponse](context.Background(), c, method)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Send(&vmmdpb.DestroyRequest{Instance: "i-1"})
		_ = s.CloseSend()
		if _, err := s.Recv(); status.Code(err) != codes.Unimplemented {
			t.Fatalf("%s: err = %v, want Unimplemented", method, err)
		}
	}
	if fake.calls.Load() != 0 {
		t.Fatal("a handler ran")
	}
}

// Security requirement 4: gRPC on the shared listener still handshakes
// itself, so the handler reads the caller's CN, and the policy still refuses.
func TestGRPCUnchangedOnSharedListener(t *testing.T) {
	fake := &fakeVmmd{}
	v := startVmmd(t, fake, true)
	call := func(cn string) (*vmmdpb.ForwardHTTPResponseInit, error) {
		conn, err := grpc.NewClient(v.addr, grpc.WithTransportCredentials(credentials.NewTLS(v.pki.clientTLS(t, cn))))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		s, err := vmmdpb.NewVmmdClient(conn).ForwardHTTPStream(context.Background())
		if err != nil {
			return nil, err
		}
		_ = s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1", Method: "GET"}}})
		_ = s.CloseSend()
		m, err := s.Recv()
		return m.GetInit(), err
	}
	init, err := call("gatewayd-internal.faas")
	if err != nil || header(init, "X-Peer-Cn") != "gatewayd-internal.faas" {
		t.Fatalf("gateway over gRPC: init %v err %v", init, err)
	}
	if _, err := call("builderd.faas"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("builderd over gRPC: err = %v, want PermissionDenied", err)
	}
}

// A vmmd without the hop refuses the protocol: the client falls back to gRPC
// before sending anything, and stays there for a while.
func TestHopFallsBackWhenVmmdLacksIt(t *testing.T) {
	v := startVmmd(t, &fakeVmmd{}, false)
	c := v.client(t, "gatewayd-internal.faas")
	var dials atomic.Int64
	dial := c.dial
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, address)
	}
	for range 2 {
		if _, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](context.Background(), c, forwardMethod); !errors.Is(err, ErrFallback) {
			t.Fatalf("err = %v, want ErrFallback", err)
		}
	}
	if dials.Load() != 1 {
		t.Fatalf("dials = %d, want 1 during the fallback window", dials.Load())
	}
}

// The gateway cancels by closing the connection; vmmd's handler sees its
// context end, and the caller gets Canceled.
func TestHopCancelReachesHandler(t *testing.T) {
	fake := &fakeVmmd{block: true, ended: make(chan error, 1)}
	v := startVmmd(t, fake, true)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](ctx, v.client(t, "gatewayd-internal.faas"), forwardMethod)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1"}}})
	_ = s.CloseSend()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if _, err := s.Recv(); status.Code(err) != codes.Canceled {
		t.Fatalf("Recv err = %v, want Canceled", err)
	}
	select {
	case err := <-fake.ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler context ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler context was not cancelled")
	}
}

// Status errors arrive with their details, as gRPC delivers them (the
// gateway's node-admission refusal reads them).
func TestHopStatusDetails(t *testing.T) {
	v := startVmmd(t, &fakeVmmd{details: true}, true)
	_, _, err := forward(t, context.Background(), v.client(t, "gatewayd-internal.faas"), "")
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted || len(st.Details()) != 1 {
		t.Fatalf("status = %v details %v", st, st.Details())
	}
	if info, ok := st.Details()[0].(*errdetails.ErrorInfo); !ok || info.GetReason() != "concurrency_throttled" {
		t.Fatalf("detail = %v", st.Details()[0])
	}
}

// Security requirement 3: limits.
func TestFrameLimits(t *testing.T) {
	big := []byte{frameMessage}
	big = append(big, 0x80, 0x80, 0x80, 0x04) // varint 4 MiB + ... over the limit below
	if _, _, err := readFrame(bufioReader(append(big, make([]byte, 16)...)), 1<<20); err == nil {
		t.Fatal("a frame over the limit was read")
	}
	if _, _, err := readFrame(bufioReader([]byte{frameMessage, 5, 1, 2}), MaxMessageBytes); err == nil {
		t.Fatal("a truncated frame was read")
	}
}
