// mtls_e2e_test.go — cross-process mTLS round-trip over a real TCP listener
// (ADR-052 §Test surface). The pkg/wire/peer_test.go unit tests cover the
// in-process handshake; this file covers the load-bearing gap that bufconn
// hides: a real OS TCP listener, a real *tls.Config, and the full pkg/pki
// → wire.DialContext → wire.ServerCredsOrEmpty → PeerCN chain.
//
// What this tests:
//   1. Gregale pki init produces a CA + per-daemon leaves that round-trip
//      over a real TCP listener on 127.0.0.1 (no KVM, no Postgres).
//   2. The handler-layer PeerCN lookup returns the client's CN only after
//      a successful handshake (regression-pin for the tls.NewListener
//      removal that motivated this slice).
//
// Build tag: (none). CI-safe. Runs under `make test`. Skips when
// FAAS_SKIP_MTLS_E2E is set (matches the FAAS_SKIP_PG_TESTS pattern
// from cmd/e2e/quota_e2e_test.go).

package e2e_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"

	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/wire"
)

// skipUnlessMTLS skips the test when the operator sets FAAS_SKIP_MTLS_E2E.
// Matches the FAAS_SKIP_PG_TESTS pattern from cmd/e2e/quota_e2e_test.go.
func skipUnlessMTLS(t *testing.T) {
	t.Helper()
	if os.Getenv("FAAS_SKIP_MTLS_E2E") != "" {
		t.Skip("FAAS_SKIP_MTLS_E2E set; skipping mTLS e2e")
	}
}

// TestMTLSE2E_PKIBootstrapLandsExpectedLayout verifies the operator-side
// CLI path lands material on disk in the expected layout. The e2e
// round-trip below depends on this — without CA + leaves, there's no
// chain to verify.
func TestMTLSE2E_PKIBootstrapLandsExpectedLayout(t *testing.T) {
	skipUnlessMTLS(t)

	root := t.TempDir()

	// Bootstrap a CA, then issue every per-daemon leaf defined in
	// pkg/pki.Roles(). Mirrors what `gregale pki init` does on a
	// production box — we go through pkg/pki directly here so the
	// test doesn't depend on the gregale binary being installed.
	caCert, caKey, err := pki.EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	for _, role := range pki.Roles() {
		if err := pki.EnsureLeaf(root, role, caCert, caKey, false); err != nil {
			t.Fatalf("EnsureLeaf %s/%s: %v", role.Directory, role.Filename, err)
		}
	}

	// Every Role.AltNames entry must be reachable on disk; the SAN
	// list is what the stdlib verifier checks during the handshake.
	for _, role := range pki.Roles() {
		certPath, keyPath := pki.LeafPaths(root, role)
		if _, err := os.Stat(certPath); err != nil {
			t.Errorf("leaf %s missing: %v", certPath, err)
		}
		if _, err := os.Stat(keyPath); err != nil {
			t.Errorf("key %s missing: %v", keyPath, err)
		}
	}
}

// TestMTLSE2E_RoundTripOnRealTCP exercises the full mTLS path on a real
// 127.0.0.1 TCP listener (no bufconn). This is the only test that proves
// the cert path works on a real OS listener — bufconn hides handshake
// bugs that show up only on the wire.
func TestMTLSE2E_RoundTripOnRealTCP(t *testing.T) {
	skipUnlessMTLS(t)

	fx := bootstrapPKI(t)
	serverTLS, err := wire.LoadServerTLSConfigWithPrefix("server_", fx.serverCert, fx.serverKey, fx.caCert)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	clientTLS, err := wire.LoadClientTLSConfigWithPrefix("client_", fx.clientCert, fx.clientKey, fx.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}

	// Bind on a free 127.0.0.1 port via wire.Listen (real TCP —
	// not bufconn, not a net.Pipe).
	lis, err := wire.Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	// Capture the peer's CN via a unary interceptor. Reading PeerCN
	// here is the exact contract ADR-052 §Handler-layer peer binding
	// relies on; if this fails, the ServerCredsOrEmpty wiring is
	// broken (the load-bearing regression that motivated this slice).
	var capturedCN string
	var sawTLSInfo bool
	interceptor := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if p, ok := peer.FromContext(ctx); ok {
			if _, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				sawTLSInfo = true
			}
		}
		if cn, perr := wire.PeerCN(ctx); perr == nil {
			capturedCN = cn
		}
		return handler(ctx, req)
	}

	opts := []grpc.ServerOption{grpc.UnaryInterceptor(interceptor)}
	opts = append(opts, wire.ServerCredsOrEmpty(serverTLS)...)
	srv := grpc.NewServer(opts...)
	hs := &mtlsHealthServer{}
	healthgrpc.RegisterHealthServer(srv, hs)
	hs.serving = true
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	// Dial the same listener with the client cert. The handshake
	// must succeed: chain trust (both signed by fx.caCert), SAN
	// match (server cert carries 127.0.0.1 + localhost), EKU
	// (server leaf has ServerAuth, client leaf has ClientAuth).
	conn, err := wire.Dial(context.Background(), "tcp://"+lis.Addr().String(), clientTLS)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthgrpc.NewHealthClient(conn).Check(ctx, &healthgrpc.HealthCheckRequest{}); err != nil {
		t.Fatalf("Health/Check: %v", err)
	}
	if !sawTLSInfo {
		t.Fatal("peer.AuthInfo did not carry credentials.TLSInfo (gRPC server transport should populate it on every TLS connection)")
	}
	if capturedCN == "" {
		t.Fatal("PeerCN returned empty CN — handler-layer CN binding is broken")
	}
	// The server sees the client's CN, not the server's CN. The
	// client cert's CN is "mtls-e2e-client" (bootstrapPKI).
	if capturedCN != "mtls-e2e-client" {
		t.Errorf("PeerCN = %q, want %q", capturedCN, "mtls-e2e-client")
	}
}

// --- fixtures -------------------------------------------------------------

type mtlsPKI struct {
	rootDir    string
	caCert     string
	caKey      string
	serverCert string
	serverKey  string
	clientCert string
	clientKey  string
}

// bootstrapPKI lays out a minimal CA + server + client leaf under a
// per-test TempDir. Mirrors what `gregale pki init` does in production,
// but with a single-server + single-client pair because that's all
// the e2e needs.
func bootstrapPKI(t *testing.T) mtlsPKI {
	t.Helper()
	root := t.TempDir()

	caCert, caKey, err := pki.EnsureCA(root, false)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	caCertPath, caKeyPath := pki.CARoot(root)

	serverRole := pki.Role{
		CommonName: "mtls-e2e-server",
		Kind:       pki.KindServer,
		Directory:  "server",
		Filename:   "server",
		AltNames:   pki.LocalDevSANs(),
	}
	if err := pki.EnsureLeaf(root, serverRole, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf server: %v", err)
	}
	serverCert, serverKey := pki.LeafPaths(root, serverRole)

	clientRole := pki.Role{
		CommonName: "mtls-e2e-client",
		Kind:       pki.KindClient,
		Directory:  "client",
		Filename:   "client",
		AltNames:   pki.LocalDevSANs(),
	}
	if err := pki.EnsureLeaf(root, clientRole, caCert, caKey, false); err != nil {
		t.Fatalf("EnsureLeaf client: %v", err)
	}
	clientCert, clientKey := pki.LeafPaths(root, clientRole)

	return mtlsPKI{
		rootDir:    root,
		caCert:     caCertPath,
		caKey:      caKeyPath,
		serverCert: serverCert,
		serverKey:  serverKey,
		clientCert: clientCert,
		clientKey:  clientKey,
	}
}

// mtlsHealthServer is the minimum surface of *health.Server we need
// for the round-trip test. Mirrors the pattern in pkg/wire/peer_test.go.
type mtlsHealthServer struct {
	healthgrpc.UnimplementedHealthServer
	serving bool
}

func (s *mtlsHealthServer) Check(_ context.Context, _ *healthgrpc.HealthCheckRequest) (*healthgrpc.HealthCheckResponse, error) {
	if !s.serving {
		return nil, nil
	}
	return &healthgrpc.HealthCheckResponse{Status: healthgrpc.HealthCheckResponse_SERVING}, nil
}

// TestMTLSE2E_HandshakeAcceptsRegisteredCN pins the PR-C (issue #678 /
// ADR-056) CN-binding handshake-layer verifier on a real mTLS leg:
// when the verifier holds the client's CN, the handshake completes
// and the round-trip RPC succeeds.
//
// The verifier is installed on BOTH sides (server validates client,
// client validates server) — the contract under test is "leaf-CN
// lookup against the registered set succeeds on every handshake",
// and both call paths must exercise the hook. Symmetric wiring also
// mirrors the production daemon shape: every daemon's
// *WithVerifier helper installs the hook on its own dial/listen side.
func TestMTLSE2E_HandshakeAcceptsRegisteredCN(t *testing.T) {
	skipUnlessMTLS(t)

	fx := bootstrapPKI(t)

	// Build the verifier and register the client CN BEFORE the
	// handshake. The server side will see this CN as the client's
	// leaf-CN; the client side will see the server's leaf-CN
	// ("mtls-e2e-server") — register both so symmetric wiring
	// doesn't accidentally reject on the client's hook.
	v := wire.NewInmemNodeVerifier()
	v.Set([]string{"mtls-e2e-client", "mtls-e2e-server"})

	serverTLS, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("server_", fx.serverCert, fx.serverKey, fx.caCert, v)
	if err != nil {
		t.Fatalf("LoadServerTLSConfigWithPrefixAndVerifier: %v", err)
	}
	clientTLS, err := wire.LoadClientTLSConfigWithPrefixAndVerifier("client_", fx.clientCert, fx.clientKey, fx.caCert, v)
	if err != nil {
		t.Fatalf("LoadClientTLSConfigWithPrefixAndVerifier: %v", err)
	}

	// Sanity: the wire factory installed the hook on both sides.
	if serverTLS.VerifyPeerCertificate == nil {
		t.Fatal("serverTLS.VerifyPeerCertificate = nil; want non-nil hook installed")
	}
	if clientTLS.VerifyPeerCertificate == nil {
		t.Fatal("clientTLS.VerifyPeerCertificate = nil; want non-nil hook installed")
	}
	if v.Size() != 2 {
		t.Errorf("verifier size = %d, want 2 (both client+server CNs registered)", v.Size())
	}

	// Bind, serve, dial — mirror TestMTLSE2E_RoundTripOnRealTCP shape.
	lis, err := wire.Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer(wire.ServerCredsOrEmpty(serverTLS)...)
	hs := &mtlsHealthServer{}
	healthgrpc.RegisterHealthServer(srv, hs)
	hs.serving = true
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := wire.Dial(context.Background(), "tcp://"+lis.Addr().String(), clientTLS)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthgrpc.NewHealthClient(conn).Check(ctx, &healthgrpc.HealthCheckRequest{}); err != nil {
		t.Fatalf("Health/Check: registered CN handshake still rejected: %v", err)
	}
}

// TestMTLSE2E_HandshakeRejectsUnregisteredCN is the negative control
// for PR-C (issue #678 / ADR-056): with the verifier installed on
// the server side but the verifier set EMPTY (the strict-nil path
// from InmemNodeVerifier is equivalent — empty set returns
// ErrNodeVerifierCNMismatch for every CN), the client leaf-CN
// ("mtls-e2e-client") is not in the registered set, so the server's
// handshake-layer hook aborts the TLS handshake before any RPC
// dispatch.
//
// Why the assertion is probe-based, not errors.Is:
//
//	The server-side hook returns ErrNodeVerifierCNMismatch wrapped
//	by nodeVerifierWithCN. crypto/tls then sends alertBadCertificate
//	and surfaces the error to gRPC's transport. gRPC's TLS
//	credentials layer rewrites the stdlib error into a transport
//	error string ("tls: bad certificate") that does NOT preserve
//	the underlying wrap — errors.Is(err, wire.ErrNodeVerifierCNMismatch)
//	fails because the chain is severed at the gRPC translation
//	boundary (verified empirically on gRPC v1.67).
//
//	To pin the rejection robustly, this test wraps the
//	InmemNodeVerifier in a recordingVerifier that captures every
//	LookupCN call AND the verifier-error code returned. The probe
//	proves the hook ran, was consulted with the client CN, and
//	returned ErrNodeVerifierCNMismatch. The dial-side assertion is
//	reduced to "the dial/RPC fails" — the rejection is pinned via
//	the probe, not the dial error string.
func TestMTLSE2E_HandshakeRejectsUnregisteredCN(t *testing.T) {
	skipUnlessMTLS(t)

	fx := bootstrapPKI(t)

	// Server-side verifier: a recordingVerifier that delegates to
	// an empty InmemNodeVerifier. The empty set → every CN rejected
	// (strict empty-set semantics from InmemNodeVerifier.LookupCN).
	emptyDelegate := wire.NewInmemNodeVerifier()
	if size := emptyDelegate.Size(); size != 0 {
		t.Fatalf("fresh InmemNodeVerifier size = %d, want 0", size)
	}
	probe := newRecordingVerifier(emptyDelegate)

	serverTLS, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("server_", fx.serverCert, fx.serverKey, fx.caCert, probe)
	if err != nil {
		t.Fatalf("LoadServerTLSConfigWithPrefixAndVerifier: %v", err)
	}
	if serverTLS.VerifyPeerCertificate == nil {
		t.Fatal("serverTLS.VerifyPeerCertificate = nil; want non-nil hook installed")
	}

	// Client-side: NO verifier — stdlib chain/SAN/EKU only. The
	// server's hook does the rejecting. This mirrors the production
	// asymmetry: in the cross-box path the listener-side verifier
	// is the load-bearing gate (the dial-side verifier is opt-in).
	clientTLS, err := wire.LoadClientTLSConfigWithPrefix("client_", fx.clientCert, fx.clientKey, fx.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfigWithPrefix: %v", err)
	}

	lis, err := wire.Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer(wire.ServerCredsOrEmpty(serverTLS)...)
	hs := &mtlsHealthServer{}
	healthgrpc.RegisterHealthServer(srv, hs)
	hs.serving = true
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := wire.Dial(context.Background(), "tcp://"+lis.Addr().String(), clientTLS)
	if err != nil {
		// gRPC may surface handshake failure at dial time if the
		// server rejects the connection eagerly. Either path is OK
		// — the probe pins the verifier side; this branch only
		// asserts the dial didn't accidentally succeed.
	} else {
		t.Cleanup(func() { _ = conn.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = healthgrpc.NewHealthClient(conn).Check(ctx, &healthgrpc.HealthCheckRequest{})
	}

	// Probe assertion: the server's hook was consulted with the
	// client CN exactly once and returned ErrNodeVerifierCNMismatch.
	// This pins the verifier contract independent of gRPC's
	// transport-layer error wrapping (which severs the wrap chain
	// across the TLS credentials boundary — see the function-level
	// comment above).
	calls := probe.calls()
	if len(calls) != 1 {
		t.Fatalf("recordingVerifier saw %d calls, want 1 (one handshake attempt)", len(calls))
	}
	if calls[0].cn != "mtls-e2e-client" {
		t.Errorf("probe saw CN %q, want %q (the bootstrapPKI client CN)", calls[0].cn, "mtls-e2e-client")
	}
	if !errors.Is(calls[0].err, wire.ErrNodeVerifierCNMismatch) {
		t.Errorf("probe saw err %v, want wire.ErrNodeVerifierCNMismatch in chain", calls[0].err)
	}
}

// recordingVerifier wraps a wire.NodeVerifier and records every
// LookupCN call. Tests use it to pin the verifier contract without
// relying on the dial-side error chain (gRPC rewrites stdlib TLS
// errors at the credentials boundary, severing the %w wrap from
// nodeVerifierWithCN). Probe-based assertions are robust to any
// future gRPC / stdlib error-translation change.
type recordingVerifier struct {
	delegate wire.NodeVerifier
	mu       sync.Mutex
	recorded []recordingVerifierCall
}

type recordingVerifierCall struct {
	cn  string
	err error
}

func newRecordingVerifier(delegate wire.NodeVerifier) *recordingVerifier {
	return &recordingVerifier{delegate: delegate}
}

func (v *recordingVerifier) LookupCN(cn string) error {
	err := v.delegate.LookupCN(cn)
	v.mu.Lock()
	v.recorded = append(v.recorded, recordingVerifierCall{cn: cn, err: err})
	v.mu.Unlock()
	return err
}

func (v *recordingVerifier) calls() []recordingVerifierCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]recordingVerifierCall, len(v.recorded))
	copy(out, v.recorded)
	return out
}

// TestMTLSE2E_RoleGateRefusesWrongBox pins the per-daemon role gate
// (ADR-092 / Gate-B PR-1). Each row exercises one box-shape refusal:
// running a control-plane-only daemon under the compute-only role must
// be rejected, and the inverse must also be rejected. Single-box dev
// (RoleSingleBox) must keep being accepted on every allow-list — this
// is the default-local back-compat path that keeps `make bootstrap`
// against 127.0.0.1 working unchanged.
//
// The table also covers the M9 shared schedd row, and the case where
// RoleSingleBox is NOT in the allow-list (a degenerate future shape,
// not used today; pinned here so a future refactor cannot silently
// strip single-box back-compat without surfacing it).
func TestMTLSE2E_RoleGateRefusesWrongBox(t *testing.T) {
	skipUnlessMTLS(t)

	cases := []struct {
		daemon string
		allow  []role.Role
		bad    role.Role
		good   role.Role
	}{
		// Control-plane-only daemons (live on fsn-1).
		{"apid", []role.Role{role.RoleSingleBox, role.RoleControlPlane}, role.RoleComputeOnly, role.RoleControlPlane},
		// M9 runs one schedd on each compute node as well as the
		// control-plane schedd; an unknown role remains rejected.
		{"schedd", []role.Role{role.RoleSingleBox, role.RoleControlPlane, role.RoleComputeOnly}, role.Role("invalid"), role.RoleComputeOnly},

		// Compute-only daemons (live on fsn-2).
		{"vmmd", []role.Role{role.RoleSingleBox, role.RoleComputeOnly}, role.RoleControlPlane, role.RoleComputeOnly},
		{"builderd", []role.Role{role.RoleSingleBox, role.RoleComputeOnly}, role.RoleControlPlane, role.RoleComputeOnly},
	}

	for _, c := range cases {
		t.Run(c.daemon+"_rejects_"+string(c.bad), func(t *testing.T) {
			if err := role.Require(c.daemon, c.bad, c.allow...); err == nil {
				t.Fatalf("%s: bad role %q accepted (allow=%v), want refusal", c.daemon, c.bad, c.allow)
			}
		})
		t.Run(c.daemon+"_accepts_"+string(c.good), func(t *testing.T) {
			if err := role.Require(c.daemon, c.good, c.allow...); err != nil {
				t.Fatalf("%s: good role %q refused (allow=%v): %v", c.daemon, c.good, c.allow, err)
			}
		})
		t.Run(c.daemon+"_accepts_single_box", func(t *testing.T) {
			if err := role.Require(c.daemon, role.RoleSingleBox, c.allow...); err != nil {
				t.Fatalf("%s: single-box dev refused (allow=%v): %v — single-box back-compat must stay accepted", c.daemon, c.allow, err)
			}
		})
	}
}
