package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/serviceproxy"
)

// serviceProxyHTTPSConfig is deliberately separate from the public ACME and
// daemon mTLS configurations. Only an operator-provisioned private CA may
// authenticate the bridge-only *.internal leaf.
func serviceProxyHTTPSConfig(cfg *Config) (*tls.Config, error) {
	if strings.TrimSpace(cfg.ServiceProxyHTTPSListen) == "" {
		if cfg.ServiceProxyTLSCertPath != "" || cfg.ServiceProxyTLSKeyPath != "" || cfg.ServiceProxyTLSCAPath != "" {
			return nil, errors.New("gatewayd: service_proxy_tls_* requires service_proxy_https_listen")
		}
		return nil, nil
	}
	if cfg.ServiceProxyListen == "" {
		return nil, errors.New("gatewayd: service_proxy_https_listen requires service_proxy_listen")
	}
	if err := validateServiceProxyListen(cfg.ServiceProxyListen); err != nil {
		return nil, err
	}
	if err := validateServiceProxyHTTPSListen(cfg.ServiceProxyHTTPSListen, cfg.ServiceProxyListen); err != nil {
		return nil, err
	}
	if cfg.ServiceProxyTLSCertPath == "" || cfg.ServiceProxyTLSKeyPath == "" || cfg.ServiceProxyTLSCAPath == "" {
		return nil, errors.New("gatewayd: service_proxy_https_listen requires service_proxy_tls_cert_path, service_proxy_tls_key_path and service_proxy_tls_ca_path")
	}
	caPEM, err := os.ReadFile(cfg.ServiceProxyTLSCAPath)
	if err != nil {
		return nil, fmt.Errorf("gatewayd: read service proxy CA: %w", err)
	}
	roots, err := serviceProxyCARoots(caPEM)
	if err != nil {
		return nil, fmt.Errorf("gatewayd: %w", err)
	}
	loadLeaf := func() (*tls.Certificate, error) {
		info, err := os.Stat(cfg.ServiceProxyTLSKeyPath)
		if err != nil {
			return nil, fmt.Errorf("service proxy key: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o027 != 0 {
			return nil, errors.New("service proxy key must be a regular file with no group write or other access")
		}
		pair, err := tls.LoadX509KeyPair(cfg.ServiceProxyTLSCertPath, cfg.ServiceProxyTLSKeyPath)
		if err != nil {
			return nil, fmt.Errorf("service proxy certificate: %w", err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("service proxy leaf: %w", err)
		}
		if leaf.IsCA || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "*.internal" || len(leaf.IPAddresses) != 0 {
			return nil, errors.New("service proxy leaf must have only the *.internal DNS SAN")
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "billing.internal", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return nil, fmt.Errorf("service proxy leaf does not verify against configured CA: %w", err)
		}
		return &pair, nil
	}
	if _, err := loadLeaf(); err != nil {
		return nil, fmt.Errorf("gatewayd: %w", err)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return loadLeaf() // leaf rotation is observed at the next handshake
		},
	}, nil
}

func serviceProxyCARoots(bundle []byte) (*x509.CertPool, error) {
	if len(bundle) == 0 || len(bundle) > 64*1024 {
		return nil, errors.New("service proxy CA bundle must contain 1..65536 bytes")
	}
	roots := x509.NewCertPool()
	count := 0
	for rest := bundle; len(strings.TrimSpace(string(rest))) > 0; {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("service proxy CA bundle must contain only PEM CA certificates")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("service proxy CA bundle contains a non-CA certificate")
		}
		if err := serviceproxy.ValidateInternalServiceCA(cert); err != nil {
			return nil, fmt.Errorf("service proxy CA bundle: %w", err)
		}
		roots.AddCert(cert)
		count++
		rest = next
	}
	if count == 0 {
		return nil, errors.New("service proxy CA bundle contains no CA certificates")
	}
	return roots, nil
}

func validateServiceProxyHTTPSListen(httpsAddr, httpAddr string) error {
	host, port, err := net.SplitHostPort(httpsAddr)
	if err != nil || port != "443" {
		return errors.New("gatewayd: service_proxy_https_listen must use the private host-bridge address on port 443")
	}
	httpHost, _, err := net.SplitHostPort(httpAddr)
	if err != nil || host != httpHost {
		return errors.New("gatewayd: service_proxy_https_listen must use the same host-bridge address as service_proxy_listen")
	}
	return nil
}

// The TLS listener has no control-path escape hatch: only a single-label
// .internal SNI matching HTTP Host can reach the binding-aware proxy.
func serviceProxyHTTPSHandler(proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || !validServiceProxyAliasAuthority(r.Host) || !strings.EqualFold(r.TLS.ServerName, stripServiceProxyHostPort(r.Host)) {
			http.Error(w, "private service alias required", http.StatusMisdirectedRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func stripServiceProxyHostPort(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return strings.TrimSuffix(name, ".")
	}
	return strings.TrimSuffix(host, ".")
}

func validServiceProxyAliasAuthority(authority string) bool {
	if strings.Contains(authority, ":") {
		_, port, err := net.SplitHostPort(authority)
		if err != nil || port != "443" {
			return false
		}
	}
	name := strings.ToLower(stripServiceProxyHostPort(authority))
	if !strings.HasSuffix(name, ".internal") {
		return false
	}
	label := strings.TrimSuffix(name, ".internal")
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range label {
		if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
