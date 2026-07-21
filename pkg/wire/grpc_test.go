// gRPC dial/listen tests for pkg/wire. Exercises the strict target parser,
// the fail-closed mTLS gates (TCP/DNS nil-TLS, TCP nil-listener-TLS), and a
// real gRPC RPC over an mTLS-bound listener on 127.0.0.1:0. The round-trip
// tests build a throwaway CA and per-test leaf certificates under
// t.TempDir(); nothing on disk outlives the test process.

package wire

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthsvc "google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

// --- ParseTarget ----------------------------------------------------------

func TestParseTarget(t *testing.T) {
	cases := []struct {
		raw       string
		want      Target
		expectErr bool
	}{
		{raw: "unix:///run/faas/vmmd.sock", want: Target{Scheme: SchemeUnix, Address: "/run/faas/vmmd.sock"}},
		{raw: "tcp://127.0.0.1:50051", want: Target{Scheme: SchemeTCP, Address: "127.0.0.1:50051"}},
		{raw: "tcp://0.0.0.0:50051", want: Target{Scheme: SchemeTCP, Address: "0.0.0.0:50051"}},
		{raw: "tcp://:50051", want: Target{Scheme: SchemeTCP, Address: ":50051"}},
		{raw: "tcp://127.0.0.1:0", want: Target{Scheme: SchemeTCP, Address: "127.0.0.1:0"}},
		{raw: "dns:///vmmd.internal:50051", want: Target{Scheme: SchemeDNS, Address: "vmmd.internal:50051"}},
		{raw: "dns:///vmmd.internal:443", want: Target{Scheme: SchemeDNS, Address: "vmmd.internal:443"}},
		{raw: "dns://vmmd.internal:50051", want: Target{Scheme: SchemeDNS, Address: "vmmd.internal:50051"}},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseTarget(tc.raw)
			if (err != nil) != tc.expectErr {
				t.Fatalf("err = %v, expectErr = %v", err, tc.expectErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseTargetRejectsInvalidTargets(t *testing.T) {
	cases := []struct {
		raw  string
		name string
	}{
		{raw: "", name: "empty"},
		{raw: "/run/faas/vmmd.sock", name: "bare absolute path (no scheme)"},
		{raw: "relative.sock", name: "bare relative path (no scheme)"},
		{raw: "127.0.0.1:50051", name: "bare host:port"},
		{raw: "unix://relative.sock", name: "non-absolute unix path"},
		{raw: "unix://host/path", name: "unix with authority"},
		{raw: "tcp://127.0.0.1", name: "tcp missing port"},
		{raw: "tcp://127.0.0.1:99999", name: "tcp port out of range"},
		{raw: "tcp://127.0.0.1:abc", name: "tcp non-numeric port"},
		{raw: "tcp:///path", name: "tcp with path"},
		{raw: "dns://:50051", name: "dns missing hostname (triple-slash)"},
		{raw: "dns:///host:50051/extra", name: "dns with extra path"},
		{raw: "tcpp://127.0.0.1:50051", name: "unknown scheme"},
		{raw: "https://example.com", name: "https scheme"},
		{raw: "unix:///run/faas/vmmd.sock?query=1", name: "unix with query"},
		{raw: "unix:///run/faas/vmmd.sock#frag", name: "unix with fragment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTarget(tc.raw); err == nil {
				t.Fatalf("ParseTarget(%q) returned nil error; want error", tc.raw)
			}
		})
	}
}

func TestNormalizeLegacyTarget(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		errSub string
	}{
		{raw: "/run/faas/vmmd.sock", want: "unix:///run/faas/vmmd.sock"},
		{raw: "unix:///run/faas/vmmd.sock", want: "unix:///run/faas/vmmd.sock"},
		{raw: "tcp://127.0.0.1:50051", want: "tcp://127.0.0.1:50051"},
		{raw: "", errSub: "empty"},
		{raw: "relative.sock", errSub: "absolute"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := NormalizeLegacyTarget(tc.raw)
			if tc.errSub != "" {
				if err == nil {
					t.Fatalf("NormalizeLegacyTarget(%q) returned nil err; want containing %q", tc.raw, tc.errSub)
				}
				if !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err = %v; want substring %q", err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- Fail-closed auth -----------------------------------------------------

func TestDialTCPDNSRejectsNilTLS(t *testing.T) {
	for _, target := range []string{
		"tcp://127.0.0.1:50051",
		"dns:///vmmd.internal:50051",
	} {
		t.Run(target, func(t *testing.T) {
			_, err := Dial(context.Background(), target, nil)
			if err == nil {
				t.Fatalf("Dial(%q, nil) returned nil error", target)
			}
			if !strings.Contains(err.Error(), "mTLS required") {
				t.Fatalf("err = %v; want containing %q", err, "mTLS required")
			}
		})
	}
}

func TestDialTCPUnwrapsWithTLS(t *testing.T) {
	// When mTLS is provided we expect Dial to construct a (lazy) client conn.
	// We don't make the peer reachable; the construction step is the
	// contract being verified here.
	pki := newTestPKI(t)
	clientTLS, err := LoadClientTLSConfig(pki.clientCert, pki.clientKey, pki.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}
	// Port 1 is the IANA tcpmux port — never bound in tests; the lazy
	// dial returns immediately even though no peer is up.
	conn, err := Dial(context.Background(), "tcp://127.0.0.1:1", clientTLS)
	if err != nil {
		t.Fatalf("Dial with mTLS: %v", err)
	}
	if conn == nil {
		t.Fatalf("Dial returned nil conn")
	}
	_ = conn.Close()
}

func TestDialTCPRejectsPortZero(t *testing.T) {
	pki := newTestPKI(t)
	clientTLS, err := LoadClientTLSConfig(pki.clientCert, pki.clientKey, pki.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}
	_, err = Dial(context.Background(), "tcp://127.0.0.1:0", clientTLS)
	if err == nil {
		t.Fatalf("expected dial to reject port 0")
	}
	if !strings.Contains(err.Error(), "port 0") {
		t.Fatalf("err = %v; want mention of port 0", err)
	}
}

func TestListenTCPRequiresMTLS(t *testing.T) {
	_, err := Listen(context.Background(), "tcp://127.0.0.1:0", nil)
	if err == nil {
		t.Fatalf("Listen(tcp, nil TLS) returned nil error")
	}
	if !strings.Contains(err.Error(), "mTLS required") {
		t.Fatalf("err = %v; want containing %q", err, "mTLS required")
	}
	if !strings.Contains(err.Error(), "listen_addr") {
		t.Fatalf("err = %v; want mentioning %q", err, "listen_addr")
	}
}

func TestListenDNSRejected(t *testing.T) {
	_, err := Listen(context.Background(), "dns:///vmmd.internal:50051", nil)
	if err == nil {
		t.Fatalf("Listen(dns) returned nil error")
	}
	if !strings.Contains(err.Error(), "not a bind target") {
		t.Fatalf("err = %v; want %q", err, "not a bind target")
	}
}

func TestDialEmptyTarget(t *testing.T) {
	_, err := Dial(context.Background(), "", nil)
	if err == nil {
		t.Fatalf("Dial(\"\", nil) returned nil error")
	}
}

func TestDialContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DialContext(ctx, "/run/faas/vmmd.sock", nil)
	if err == nil {
		t.Fatalf("DialContext with cancelled ctx returned nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled (errors.Is)", err)
	}
}

// --- mTLS round-trip ------------------------------------------------------

func TestMTLSRoundTrip(t *testing.T) {
	pki := newTestPKI(t)
	serverTLS, err := LoadServerTLSConfig(pki.serverCert, pki.serverKey, pki.caCert)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	clientTLS, err := LoadClientTLSConfig(pki.clientCert, pki.clientKey, pki.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}

	lis, err := Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	addr := lis.Addr().String()
	healthServer := healthsvc.NewServer()
	healthServer.SetServingStatus("", healthgrpc.HealthCheckResponse_SERVING)
	srv := grpc.NewServer()
	healthgrpc.RegisterHealthServer(srv, healthServer)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := Dial(context.Background(), "tcp://"+addr, clientTLS)
	if err != nil {
		t.Fatalf("Dial tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cli := healthgrpc.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := cli.Check(ctx, &healthgrpc.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health/Check: %v", err)
	}
	if resp.GetStatus() != healthgrpc.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v; want SERVING", resp.GetStatus())
	}
}

func TestMTLSRoundTripRejectsWrongCA(t *testing.T) {
	serverPKI := newTestPKI(t)
	wrongPKI := newTestPKI(t) // independently generated, never trusted

	serverTLS, err := LoadServerTLSConfig(serverPKI.serverCert, serverPKI.serverKey, serverPKI.caCert)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	// Client trusts wrongPKI's CA but presents no certificate. Per the
	// loader contract, that produces a client config; the server's
	// RequireAndVerifyClientCert will then reject the unauthenticated peer
	// even before CA mismatch becomes relevant. Both failure modes prove
	// the auth boundary; assert on the RPC failure, not on Dial's return.
	clientTLS, err := LoadClientTLSConfig(wrongPKI.clientCert, wrongPKI.clientKey, wrongPKI.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}

	lis, err := Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	addr := lis.Addr().String()
	srv := grpc.NewServer()
	healthgrpc.RegisterHealthServer(srv, healthsvc.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := Dial(context.Background(), "tcp://"+addr, clientTLS)
	if err != nil {
		t.Fatalf("Dial tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cli := healthgrpc.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Check(ctx, &healthgrpc.HealthCheckRequest{}); err == nil {
		t.Fatalf("Health/Check succeeded with wrong CA; want failure")
	}
}

func TestMTLSRoundTripUntrustedServerCert(t *testing.T) {
	// The mirror case: server presents a leaf signed by a CA the client
	// doesn't trust. Without InsecureSkipVerify-only, the client-side
	// VerifyPeerCertificate must reject the handshake.
	serverPKI := newTestPKI(t)
	clientPKI := newTestPKI(t)

	serverTLS, err := LoadServerTLSConfig(serverPKI.serverCert, serverPKI.serverKey, serverPKI.caCert)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	clientTLS, err := LoadClientTLSConfig(clientPKI.clientCert, clientPKI.clientKey, clientPKI.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}

	lis, err := Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer()
	healthgrpc.RegisterHealthServer(srv, healthsvc.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := Dial(context.Background(), "tcp://"+lis.Addr().String(), clientTLS)
	if err != nil {
		t.Fatalf("Dial tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cli := healthgrpc.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Check(ctx, &healthgrpc.HealthCheckRequest{}); err == nil {
		t.Fatalf("Health/Check succeeded against untrusted server CA; want failure")
	}
}

// TestMTLSRoundTripRejectsHostnameMismatch locks the new contract that
// loadClientTLSConfig relies on stdlib's SAN check (closes alert #58).
// The server cert is issued with IPAddresses=[10.0.0.99] only, but the
// dial target is 127.0.0.1; the stdlib verifier (via grpc-go's
// ServerName auto-promotion from the dial :authority) must reject the
// handshake. Without the SAN check, this test would pass silently.
//
// The PKI is built inline (rather than reusing newTestPKI) because the
// helper hard-codes 127.0.0.1/localhost SANs that would mask the
// mismatch path.
func TestMTLSRoundTripRejectsHostnameMismatch(t *testing.T) {
	dir := t.TempDir()

	caCert, caCertPEM, caKey := mustGenSelfSigned(t, x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca-mismatch"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	})

	// Leaf signed for 10.0.0.99 only — IP SAN does NOT cover 127.0.0.1.
	serverCertPEM, serverKeyPEM := mustGenSigned(t, x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-server-mismatch"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("10.0.0.99")},
	}, caCert, caKey)

	clientCertPEM, clientKeyPEM := mustGenSigned(t, x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "test-client-mismatch"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, caCert, caKey)

	caCertPath := writeTestFile(t, filepath.Join(dir, "ca.pem"), caCertPEM)
	serverCertPath := writeTestFile(t, filepath.Join(dir, "server.crt"), serverCertPEM)
	serverKeyPath := writeTestFile(t, filepath.Join(dir, "server.key"), serverKeyPEM)
	clientCertPath := writeTestFile(t, filepath.Join(dir, "client.crt"), clientCertPEM)
	clientKeyPath := writeTestFile(t, filepath.Join(dir, "client.key"), clientKeyPEM)

	serverTLS, err := LoadServerTLSConfig(serverCertPath, serverKeyPath, caCertPath)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	clientTLS, err := LoadClientTLSConfig(clientCertPath, clientKeyPath, caCertPath)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}

	lis, err := Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer()
	healthgrpc.RegisterHealthServer(srv, healthsvc.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := Dial(context.Background(), "tcp://"+lis.Addr().String(), clientTLS)
	if err != nil {
		t.Fatalf("Dial tcp mTLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cli := healthgrpc.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Check(ctx, &healthgrpc.HealthCheckRequest{}); err == nil {
		t.Fatalf("Health/Check succeeded against hostname-mismatched server; stdlib SAN check is not enforcing")
	}
}

// --- TLS loaders ----------------------------------------------------------

func TestTLSLoadersAcceptNilAllEmpty(t *testing.T) {
	if s, err := LoadServerTLSConfig("", "", ""); err != nil || s != nil {
		t.Fatalf("server empty: cfg=%v err=%v", s, err)
	}
	if c, err := LoadClientTLSConfig("", "", ""); err != nil || c != nil {
		t.Fatalf("client empty: cfg=%v err=%v", c, err)
	}
}

func TestTLSLoadersRejectPartialConfiguration(t *testing.T) {
	cases := []struct {
		name          string
		cert, key, ca string
		// missingSub is the field that MUST appear in the error. The
		// loaders always list every missing field, so any subset suffices.
		missingSub string
		load       func(string, string, string) (*tls.Config, error)
	}{
		{"server: only cert", "a", "", "", "tls_ca_path", LoadServerTLSConfig},
		{"server: only key", "", "b", "", "tls_ca_path", LoadServerTLSConfig},
		{"server: only ca", "", "", "c", "tls_cert_path", LoadServerTLSConfig},
		{"server: cert+key no ca", "a", "b", "", "tls_ca_path", LoadServerTLSConfig},
		{"server: cert+ca no key", "a", "", "c", "tls_key_path", LoadServerTLSConfig},
		{"server: key+ca no cert", "", "b", "c", "tls_cert_path", LoadServerTLSConfig},
		{"client: only cert", "a", "", "", "tls_ca_path", LoadClientTLSConfig},
		{"client: cert+key no ca", "a", "b", "", "tls_ca_path", LoadClientTLSConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.load(tc.cert, tc.key, tc.ca)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.missingSub)
			}
			if !strings.Contains(err.Error(), tc.missingSub) {
				t.Fatalf("err = %v; want substring %q", err, tc.missingSub)
			}
		})
	}
}

func TestTLSLoadersRejectInvalidPEM(t *testing.T) {
	dir := t.TempDir()
	badCA := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(badCA, []byte("not a PEM"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pki := newTestPKI(t)
	// Server cert valid, CA file unreadable as PEM.
	_, err := LoadServerTLSConfig(pki.serverCert, pki.serverKey, badCA)
	if err == nil {
		t.Fatalf("expected error for non-PEM CA")
	}
	if !strings.Contains(err.Error(), "CA file") {
		t.Fatalf("err = %v; want CA file mention", err)
	}
}

// TestLoadClientTLSConfigDefaultVerifier pins the contract that
// loadClientTLSConfig delegates entirely to stdlib's verifier: no
// InsecureSkipVerify, no custom VerifyPeerCertificate. grpc-go's
// tlsCreds.ClientHandshake populates ServerName from the dial
// :authority before tls.Client is called (see internal/credentials
// CloneTLSConfig + assignment in credentials/tls.go).
func TestLoadClientTLSConfigDefaultVerifier(t *testing.T) {
	pki := newTestPKI(t)
	cfg, err := LoadClientTLSConfig(pki.clientCert, pki.clientKey, pki.caCert)
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}
	if cfg.InsecureSkipVerify {
		t.Fatalf("InsecureSkipVerify = true; want false (stdlib verifier must run, including SAN check)")
	}
	if cfg.VerifyPeerCertificate != nil {
		t.Fatalf("VerifyPeerCertificate != nil; want nil (stdlib handles chain + SAN + EKU)")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %x; want TLS 1.3 (%x)", cfg.MinVersion, tls.VersionTLS13)
	}
}

// TestTLSLoadersWithPrefix: schedd's error-name accuracy depends on the
// _WithPrefix variants. Pin that the prefix is applied to every missing
// field name and that the no-prefix variant stays generic.
func TestTLSLoadersWithPrefix(t *testing.T) {
	t.Run("server+prefix names vmmd_tls_*", func(t *testing.T) {
		_, err := LoadServerTLSConfigWithPrefix("vmmd_", "/some/cert", "", "")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "vmmd_tls_key_path") {
			t.Errorf("err = %v; want vmmd_tls_key_path named", err)
		}
		if !strings.Contains(err.Error(), "vmmd_tls_ca_path") {
			t.Errorf("err = %v; want vmmd_tls_ca_path named", err)
		}
		if strings.Contains(err.Error(), "vmmd_tls_cert_path") {
			t.Errorf("err = %v; do NOT want vmmd_tls_cert_path named (it was set)", err)
		}
	})
	t.Run("client+empty prefix names tls_*", func(t *testing.T) {
		_, err := LoadClientTLSConfigWithPrefix("", "/some/cert", "", "")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "tls_key_path") {
			t.Errorf("err = %v; want tls_key_path named", err)
		}
		if !strings.Contains(err.Error(), "tls_ca_path") {
			t.Errorf("err = %v; want tls_ca_path named", err)
		}
	})
}

// TestTargetStringRoundTrip: pin the asymmetries of Target.String() so
// nobody silently changes it. unix reconstructs the canonical form;
// tcp and dns round-trip via ParseTarget. Pass them back through
// ParseTarget to confirm the loop is stable.
func TestTargetStringRoundTrip(t *testing.T) {
	cases := []string{
		"unix:///run/faas/vmmd.sock",
		"tcp://127.0.0.1:50051",
		"tcp://0.0.0.0:50051",
		"tcp://:50051",
		"dns:///vmmd.internal:50051",
		"dns://vmmd.internal:50051",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			tgt, err := ParseTarget(raw)
			if err != nil {
				t.Fatalf("ParseTarget: %v", err)
			}
			round, err := ParseTarget(tgt.String())
			if err != nil {
				t.Fatalf("ParseTarget(String()): %v", err)
			}
			if round != tgt {
				t.Fatalf("round-trip mismatch: in=%+v out=%+v", tgt, round)
			}
		})
	}
}

// --- TCP listener bound to a real port, no RPC ----------------------------

func TestListenTCPAllocatesPort(t *testing.T) {
	pki := newTestPKI(t)
	serverTLS, err := LoadServerTLSConfig(pki.serverCert, pki.serverKey, pki.caCert)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	lis, err := Listen(context.Background(), "tcp://127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	if _, ok := lis.Addr().(*net.TCPAddr); !ok {
		t.Fatalf("Addr() = %T; want *net.TCPAddr", lis.Addr())
	}
}

// --- ListenAs owner-aware -----------------------------------------------

func TestListenAsRequiresAbsolutePath(t *testing.T) {
	// The scheme parser already rejects non-absolute paths; this guards
	// against future regressions that might bypass ParseTarget.
	_, err := ListenAs(context.Background(), "unix://relative.sock", nil, "root")
	if err == nil {
		t.Fatalf("expected error for relative unix path")
	}
}

// --- Compile-time pin: insecure credentials stay the unix default ------

func TestDialUnixInsecure(t *testing.T) {
	// A nil tlsCfg on a unix:// target should yield a *grpc.ClientConn
	// that doesn't carry any TLS credentials. We can't easily probe the
	// creds object directly without exporting types; instead we just
	// confirm construction succeeds and the dial is lazy (no peer up).
	conn, err := Dial(context.Background(), "/run/faas/should-not-exist.sock", nil)
	if err != nil {
		t.Fatalf("Dial(unix, nil TLS): %v", err)
	}
	_ = conn.Close()
	_ = credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	_ = insecure.NewCredentials() // keep import live
}

// --- Test PKI helpers ----------------------------------------------------

type testPKI struct {
	caCert     string
	serverCert string
	serverKey  string
	clientCert string
	clientKey  string
}

// newTestPKI builds a one-shot CA + server leaf + client leaf. All PEM
// files live under t.TempDir() so they disappear with the test. Each
// call generates an independent CA, which lets the wrong-CA tests prove
// the trust boundary without any global state.
func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()

	caCert, caCertPEM, caKey := mustGenSelfSigned(t, x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	})

	serverCertPEM, serverKeyPEM := mustGenSigned(t, x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}, caCert, caKey)

	clientCertPEM, clientKeyPEM := mustGenSigned(t, x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, caCert, caKey)

	caCertPath := writeTestFile(t, filepath.Join(dir, "ca.pem"), caCertPEM)
	serverCertPath := writeTestFile(t, filepath.Join(dir, "server.crt"), serverCertPEM)
	serverKeyPath := writeTestFile(t, filepath.Join(dir, "server.key"), serverKeyPEM)
	clientCertPath := writeTestFile(t, filepath.Join(dir, "client.crt"), clientCertPEM)
	clientKeyPath := writeTestFile(t, filepath.Join(dir, "client.key"), clientKeyPEM)

	return testPKI{
		caCert:     caCertPath,
		serverCert: serverCertPath,
		serverKey:  serverKeyPath,
		clientCert: clientCertPath,
		clientKey:  clientKeyPath,
	}
}

// mustGenSelfSigned returns (parsed-cert, cert-PEM, key) for a self-signed CA.
func mustGenSelfSigned(t *testing.T, tmpl x509.Certificate) (*x509.Certificate, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate (self-signed): %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse self-signed: %v", err)
	}
	certPEM := mustEncodePEM(t, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	_ = mustEncodePEM(t, "EC PRIVATE KEY", keyDER) // discarded; not written
	return cert, certPEM, key
}

// mustGenSigned signs tmpl with parent + parentKey and returns
// (cert-PEM, key-PEM). The leaf key is freshly generated.
func mustGenSigned(t *testing.T, tmpl x509.Certificate, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) ([]byte, []byte) {
	t.Helper()
	leaf, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, parent, &leaf.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM := mustEncodePEM(t, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(leaf)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	keyPEM := mustEncodePEM(t, "EC PRIVATE KEY", keyDER)
	return certPEM, keyPEM
}

func writeTestFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}

// mustEncodePEM wraps der with a BEGIN/END block using encoding/pem under
// the hood (the stdlib helper), so the loader's tls.LoadX509KeyPair path
// is exercised end-to-end.
func mustEncodePEM(t *testing.T, typ string, der []byte) []byte {
	t.Helper()
	out := pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
	if out == nil {
		t.Fatalf("pem.EncodeToMemory returned nil")
	}
	return out
}

// pemBlockFor / pemEncodeStd shims removed: the helpers above use the
// stdlib encoding/pem directly.
