package e2etest

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/serviceproxy"
)

func TestServiceGuestTLSUsesConstrainedTrustAndPrivateBridge(t *testing.T) {
	caPath := ServiceGuestTLS(t)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("missing fixture CA")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || serviceproxy.ValidateInternalServiceCA(ca) != nil {
		t.Fatal("fixture CA is not name-constrained")
	}
	pair, err := tls.LoadX509KeyPair(os.Getenv("FAAS_E2E_SERVICE_HTTPS_CERT"), os.Getenv("FAAS_E2E_SERVICE_HTTPS_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(os.Getenv("FAAS_E2E_SERVICE_HTTPS_KEY"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("private fixture key permissions changed")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("unreadable fixture trust anchor")
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	t.Cleanup(server.Close)
	for _, name := range []string{"billing.internal", "billing.example.com"} {
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: name}}
		client := &http.Client{Transport: transport}
		response, err := client.Get(server.URL)
		transport.CloseIdleConnections()
		if name == "billing.internal" {
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
		} else if err == nil {
			response.Body.Close()
			t.Fatal("private fixture trust authenticated a public DNS name")
		}
	}
	var cfg struct {
		Listen string `toml:"service_proxy_https_listen"`
		CA     string `toml:"service_proxy_tls_ca_path"`
		Cert   string `toml:"service_proxy_tls_cert_path"`
		Key    string `toml:"service_proxy_tls_key_path"`
	}
	if _, err := toml.Decode(gatewaydConfig("127.0.0.1:1", "127.0.0.1:2", "http://127.0.0.1:3", true), &cfg); err != nil {
		t.Fatal(err)
	}
	want := net.JoinHostPort(api.DefaultHostBridgeCIDR().Addr().Next().String(), "443")
	if cfg.Listen != want || cfg.CA != caPath || cfg.Cert != os.Getenv("FAAS_E2E_SERVICE_HTTPS_CERT") || cfg.Key != os.Getenv("FAAS_E2E_SERVICE_HTTPS_KEY") {
		t.Fatal("managed HTTPS fixture did not wire bridge TLS")
	}
	cfg.Listen, cfg.CA, cfg.Cert, cfg.Key = "", "", "", ""
	if _, err := toml.Decode(gatewaydConfig("127.0.0.1:1", "127.0.0.1:2", "http://127.0.0.1:3", false), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "" || cfg.CA != "" || cfg.Cert != "" || cfg.Key != "" {
		t.Fatal("ordinary CI acquired a bridge TLS listener")
	}
}
