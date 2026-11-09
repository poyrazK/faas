// adr: 20261102081618, 20261103163954
// edge_tls_test.go pins the compute edge's public TLS listener. Behind
// Cloudflare it serves only peers holding the zone's client certificate, and
// trusts their CF-Connecting-IP. With no front it serves any client that
// names an app host, over HTTP/2 or HTTP/1.1, takes the client address from
// the connection, and still trusts a peer holding the zone's certificate.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestEdgeTLSConfigFromEnv(t *testing.T) {
	full := map[string]string{
		edgeTLSAddrEnv:     ":443",
		edgeTLSCertEnv:     "/etc/faas/edge-tls/cert.pem",
		edgeTLSKeyEnv:      "/etc/faas/edge-tls/key.pem",
		edgeTLSClientCAEnv: "/etc/faas/edge-tls/client-ca.pem",
	}
	without := func(env map[string]string, name string) map[string]string {
		out := map[string]string{}
		for k, v := range env {
			if k != name {
				out[k] = v
			}
		}
		return out
	}
	with := func(env map[string]string, name, value string) map[string]string {
		out := without(env, name)
		out[name] = value
		return out
	}
	direct := with(full, edgeTLSFrontEnv, "none")
	fromDatabase := with(with(with(without(without(direct, edgeTLSCertEnv), edgeTLSKeyEnv), edgeTLSCertSourceEnv, "database"), edgeTLSIdentityEnv, "/run/credentials/x/edge_tls"), edgeTLSClientCAEnv, full[edgeTLSClientCAEnv])
	for _, tc := range []struct {
		name         string
		mode         publicEdgeMode
		env          map[string]string
		wantEnabled  bool
		wantDirect   bool
		wantDatabase bool
		wantErr      string
	}{
		{name: "off", mode: edgeModeCompute, env: map[string]string{}},
		{name: "on for a compute edge", mode: edgeModeCompute, env: full, wantEnabled: true},
		{name: "missing the client CA", mode: edgeModeCompute, env: without(full, edgeTLSClientCAEnv), wantErr: edgeTLSClientCAEnv},
		{name: "files without an address", mode: edgeModeCompute, env: without(full, edgeTLSAddrEnv), wantErr: edgeTLSAddrEnv},
		{name: "control-plane edge", mode: edgeModeControlPlane, env: full, wantErr: "for a compute edge"},
		{name: "behind Cloudflare by name", mode: edgeModeCompute, env: with(full, edgeTLSFrontEnv, "cloudflare"), wantEnabled: true},
		{name: "the default front alone", mode: edgeModeCompute, env: map[string]string{edgeTLSFrontEnv: "cloudflare"}},
		{name: "no front", mode: edgeModeCompute, env: direct, wantEnabled: true, wantDirect: true},
		{name: "no front without the client CA", mode: edgeModeCompute, env: without(direct, edgeTLSClientCAEnv), wantErr: edgeTLSClientCAEnv},
		{name: "no front without an address", mode: edgeModeCompute, env: map[string]string{edgeTLSFrontEnv: "none"}, wantErr: edgeTLSAddrEnv},
		{name: "no front on a control-plane edge", mode: edgeModeControlPlane, env: direct, wantErr: "for a compute edge"},
		{name: "an unknown front", mode: edgeModeCompute, env: with(full, edgeTLSFrontEnv, "akamai"), wantErr: edgeTLSFrontEnv},
		{name: "the edge-tls identity alone", mode: edgeModeCompute, env: map[string]string{edgeTLSIdentityEnv: "/run/credentials/x/edge_tls"}},
		{name: "from the database", mode: edgeModeCompute, env: fromDatabase, wantEnabled: true, wantDirect: true, wantDatabase: true},
		{name: "from the database without the identity", mode: edgeModeCompute, env: without(fromDatabase, edgeTLSIdentityEnv), wantErr: edgeTLSIdentityEnv},
		{name: "from the database with files too", mode: edgeModeCompute, env: with(fromDatabase, edgeTLSCertEnv, "/etc/faas/edge-tls/cert.pem"), wantErr: "takes no"},
		{name: "from the database of an unknown CA", mode: edgeModeCompute, env: with(fromDatabase, edgeTLSCertCAEnv, "zerossl"), wantErr: edgeTLSCertCAEnv},
		{name: "a CA for files", mode: edgeModeCompute, env: with(full, edgeTLSCertCAEnv, "letsencrypt"), wantErr: edgeTLSCertCAEnv},
		{name: "an unknown source", mode: edgeModeCompute, env: with(full, edgeTLSCertSourceEnv, "vault"), wantErr: edgeTLSCertSourceEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := edgeTLSConfigFromEnv(tc.mode, func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.enabled() != tc.wantEnabled {
				t.Fatalf("enabled = %v, want %v", c.enabled(), tc.wantEnabled)
			}
			if c.direct() != tc.wantDirect {
				t.Fatalf("direct = %v, want %v", c.direct(), tc.wantDirect)
			}
			if c.fromDatabase() != tc.wantDatabase || (c.fromDatabase() && c.ca != edgecert.CALetsEncrypt) {
				t.Fatalf("source = %q, ca = %q; want database %v, letsencrypt by default", c.source, c.ca, tc.wantDatabase)
			}
		})
	}
}

// testCA is a certificate authority that writes PEM files for the listener.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) pem() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// issue returns a leaf certificate for usage, signed by ca.
func (ca *testCA) issue(t *testing.T, usage x509.ExtKeyUsage, dnsNames ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func writePEMPair(t *testing.T, dir string, cert tls.Certificate) (certFile, keyFile string) {
	t.Helper()
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestEdgeTLSListenerServesOnlyTheZone(t *testing.T) {
	dir := t.TempDir()
	originCA, zoneCA, otherCA := newTestCA(t, "origin"), newTestCA(t, "zone"), newTestCA(t, "other zone")
	certFile, keyFile := writePEMPair(t, dir, originCA.issue(t, x509.ExtKeyUsageServerAuth, "*.gregale.dev"))
	clientCAFile := filepath.Join(dir, "client-ca.pem")
	if err := os.WriteFile(clientCAFile, zoneCA.pem(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := edgeTLSConfig{addr: "127.0.0.1:0", certFile: certFile, keyFile: keyFile, clientCAFile: clientCAFile}
	ln, err := openEdgeTLSListener(t.Context(), cfg, nil, nil, edgeClientLimits{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(chan string, 1)
	srv := &http.Server{Handler: cloudflareClientIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("X-Forwarded-For")
	}), nil)}
	failures := prometheus.NewCounter(prometheus.CounterOpts{Name: "failures"})
	cfg.attach(srv, quietEdgeLog(), failures)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	roots := x509.NewCertPool()
	roots.AddCert(originCA.cert)
	get := func(clientCert *tls.Certificate) (*http.Response, error) {
		tlsCfg := &tls.Config{RootCAs: roots, ServerName: "shop.gregale.dev"}
		if clientCert != nil {
			tlsCfg.Certificates = []tls.Certificate{*clientCert}
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodGet, "https://"+ln.Addr().String()+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "shop.gregale.dev"
		req.Header.Set("CF-Connecting-IP", "203.0.113.9")
		req.Header.Set("X-Forwarded-For", "198.51.100.1")
		return client.Do(req)
	}

	zoneCert := zoneCA.issue(t, x509.ExtKeyUsageClientAuth)
	resp, err := get(&zoneCert)
	if err != nil {
		t.Fatalf("with the zone's certificate: %v", err)
	}
	_ = resp.Body.Close()
	if got := <-seen; got != "203.0.113.9" {
		t.Fatalf("client address = %q, want CF-Connecting-IP", got)
	}

	otherCert := otherCA.issue(t, x509.ExtKeyUsageClientAuth)
	for name, cert := range map[string]*tls.Certificate{"no certificate": nil, "another zone's certificate": &otherCert} {
		if resp, err := get(cert); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s: request served, want a failed handshake", name)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(failures) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := testutil.ToFloat64(failures); got != 2 {
		t.Fatalf("handshake failures = %v, want 2", got)
	}
	select {
	case got := <-seen:
		t.Fatalf("a refused peer reached the handler with %q", got)
	default:
	}
}

func TestEdgeTLSListenerWithNoFront(t *testing.T) {
	dir := t.TempDir()
	publicCA, zoneCA := newTestCA(t, "public"), newTestCA(t, "zone")
	certFile, keyFile := writePEMPair(t, dir, publicCA.issue(t, x509.ExtKeyUsageServerAuth, "*.gregale.dev"))
	clientCAFile := filepath.Join(dir, "client-ca.pem")
	if err := os.WriteFile(clientCAFile, zoneCA.pem(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := edgeTLSConfig{addr: "127.0.0.1:0", certFile: certFile, keyFile: keyFile, clientCAFile: clientCAFile, front: edgeFrontNone}
	appHost := func(name string) bool { return strings.HasSuffix(name, ".gregale.dev") }
	ln, err := openEdgeTLSListener(t.Context(), cfg, appHost, nil, edgeClientLimits{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	type seenRequest struct {
		proto            int
		clientIP, scheme string
	}
	seen := make(chan seenRequest, 4)
	// The production chain: cloudflareClientIP, then the gateway's forwarding
	// context. A direct client is no trusted ingress peer.
	srv := &http.Server{Handler: cloudflareClientIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP, scheme := gateway.CanonicalForwardingContext(r, nil)
		seen <- seenRequest{proto: r.ProtoMajor, clientIP: clientIP, scheme: scheme}
	}), nil)}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	cfg.attach(srv, quietEdgeLog(), prometheus.NewCounter(prometheus.CounterOpts{Name: "failures"}))
	if srv.HTTP2 == nil || srv.HTTP2.MaxConcurrentStreams != api.EdgeHTTP2MaxConcurrentStreams || srv.HTTP2.MaxReadFrameSize != api.EdgeHTTP2MaxReadFrameSize {
		t.Fatalf("HTTP/2 config = %+v, want the edge's explicit limits", srv.HTTP2)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	roots := x509.NewCertPool()
	roots.AddCert(publicCA.cert)
	zoneCert := zoneCA.issue(t, x509.ExtKeyUsageClientAuth)
	otherCert := newTestCA(t, "another zone").issue(t, x509.ExtKeyUsageClientAuth)
	get := func(serverName string, nextProto []string, clientCert *tls.Certificate) (*http.Response, error) {
		tlsCfg := &tls.Config{RootCAs: roots, ServerName: serverName, NextProtos: nextProto}
		if serverName == "" {
			// No server name: skip the name check on our side so the
			// edge's refusal is what fails the handshake.
			tlsCfg.InsecureSkipVerify = true //nolint:gosec // the test's own server
		}
		if clientCert != nil {
			tlsCfg.Certificates = []tls.Certificate{*clientCert}
		}
		transport := &http.Transport{TLSClientConfig: tlsCfg, ForceAttemptHTTP2: slices.Contains(nextProto, "h2")}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		req, err := http.NewRequest(http.MethodGet, "https://"+ln.Addr().String()+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "shop.gregale.dev"
		req.Header.Set("CF-Connecting-IP", "203.0.113.9")
		req.Header.Set("X-Forwarded-For", "198.51.100.1")
		return client.Do(req)
	}

	for _, tc := range []struct {
		name         string
		nextProto    []string
		clientCert   *tls.Certificate
		wantProto    int
		wantClientIP string
	}{
		// A direct client: no certificate, its headers ignored.
		{name: "HTTP/2", nextProto: []string{"h2", "http/1.1"}, wantProto: 2, wantClientIP: "127.0.0.1"},
		{name: "HTTP/1.1", nextProto: []string{"http/1.1"}, wantProto: 1, wantClientIP: "127.0.0.1"},
		// Cloudflare, for a host sent back behind it: its certificate
		// verified, CF-Connecting-IP trusted.
		{name: "Cloudflare's certificate", nextProto: []string{"http/1.1"}, clientCert: &zoneCert, wantProto: 1, wantClientIP: "203.0.113.9"},
		// Another zone's certificate does not chain to our CA: served as
		// any client, trusted for nothing.
		{name: "another zone's certificate", nextProto: []string{"http/1.1"}, clientCert: &otherCert, wantProto: 1, wantClientIP: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := get("shop.gregale.dev", tc.nextProto, tc.clientCert)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_ = resp.Body.Close()
			if resp.ProtoMajor != tc.wantProto {
				t.Fatalf("response protocol = HTTP/%d, want HTTP/%d", resp.ProtoMajor, tc.wantProto)
			}
			got := <-seen
			if got.proto != tc.wantProto || got.clientIP != tc.wantClientIP || got.scheme != "https" {
				t.Fatalf("request = HTTP/%d from %s over %s, want HTTP/%d from %s over https", got.proto, got.clientIP, got.scheme, tc.wantProto, tc.wantClientIP)
			}
		})
	}

	for name, serverName := range map[string]string{
		"no server name":               "",
		"a name outside the app hosts": "node.example.net",
	} {
		t.Run(name, func(t *testing.T) {
			if resp, err := get(serverName, []string{"http/1.1"}, nil); err == nil {
				_ = resp.Body.Close()
				t.Fatal("request served, want a failed handshake")
			}
		})
	}
	select {
	case got := <-seen:
		t.Fatalf("a refused handshake reached the handler: %+v", got)
	default:
	}
}

func TestEdgeServerErrorsLogOneHandshakeFailureAMinute(t *testing.T) {
	var buf bytes.Buffer
	now := time.Unix(1000, 0)
	failures := prometheus.NewCounter(prometheus.CounterOpts{Name: "failures"})
	e := newEdgeServerErrors(slog.New(slog.NewTextHandler(&buf, nil)), failures)
	e.now = func() time.Time { return now }

	handshake := []byte("http: TLS handshake error from 172.70.1.1:5000: tls: client didn't provide a certificate\n")
	for range 3 {
		_, _ = e.Write(handshake)
	}
	now = now.Add(time.Minute)
	_, _ = e.Write(handshake)
	_, _ = e.Write([]byte("http: superfluous response.WriteHeader call\n"))

	if got := testutil.ToFloat64(failures); got != 4 {
		t.Fatalf("failures = %v, want every handshake counted", got)
	}
	if got := strings.Count(buf.String(), "edge TLS handshake failed"); got != 2 {
		t.Fatalf("logged %d handshake failures, want 2 (one a minute):\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "superfluous") {
		t.Fatalf("another server error was not logged:\n%s", buf.String())
	}
}
