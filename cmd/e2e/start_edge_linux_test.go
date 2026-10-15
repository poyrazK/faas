//go:build linux

package e2e_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// Provide the upstream TLS boundary locally without public DNS, hosts-file
// changes, or insecure TLS. Only this child CLI uses the local CONNECT proxy
// and trusts this disposable certificate. Every request reaches the real edge.
func startAcceptanceTLS(t *testing.T, edge string) (proxyURL, caPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "start acceptance"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		DNSNames:    []string{"*.apps.test.example", "*" + wire.DeployWildcardSuffix},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:        true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath = filepath.Join(t.TempDir(), "edge-ca.pem")
	writeStartAcceptanceFile(t, caPath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	origin, err := url.Parse(edge)
	if err != nil {
		t.Fatal(err)
	}
	reverse := httputil.NewSingleHostReverseProxy(origin)
	reverse.Transport = &http.Transport{Proxy: nil}
	server := httptest.NewUnstartedServer(reverse)
	server.Config.SetKeepAlivesEnabled(false)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		if r.Method != http.MethodConnect || err != nil || port != "443" ||
			!(strings.HasSuffix(host, ".apps.test.example") || strings.HasPrefix(host, "deploy-") && strings.HasSuffix(host, wire.DeployWildcardSuffix)) {
			http.Error(w, "outside acceptance fixture", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", server.Listener.Addr().String(), 5*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_ = client.SetDeadline(time.Now().Add(40 * time.Second))
		_ = upstream.SetDeadline(time.Now().Add(40 * time.Second))
		if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffered); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	t.Cleanup(proxy.Close)
	return proxy.URL, caPath
}
