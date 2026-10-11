package runtimefence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// ReceiptClientConfig is private administrative transport review, separate from
// authority enrollment. PEM inputs are parsed into owned credentials; neither
// caller-owned tls.Config nor a mutable shared http.Client is accepted. Keys
// must be supplied by trusted private wiring; this slice does not load files,
// enroll an authority, provide key custody or qualify enforcement semantics.
type ReceiptClientConfig struct {
	Endpoint                             string
	AuthorityID, ServerSPKISHA256        string
	AuthorityPublicKey                   []byte
	ServerRootsPEM, ClientCertificatePEM []byte
	ClientPrivateKeyPEM                  []byte
}

func NewReceiptClient(config ReceiptClientConfig) (*ReceiptClient, error) {
	u, err := receiptEndpoint(config.Endpoint)
	if err != nil || !Digest(config.ServerSPKISHA256) {
		return nil, deliveryError("pinned HTTPS endpoint")
	}
	verifier, err := NewVerifier(config.AuthorityID, config.AuthorityPublicKey)
	if err != nil {
		return nil, deliveryError("pinned receipt authority")
	}
	tlsConfig, credential, err := receiptTLS(config)
	if err != nil {
		return nil, err
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport := &http.Transport{
		Proxy: nil, Protocols: protocols, DisableCompression: true,
		DisableKeepAlives:      true,
		MaxConnsPerHost:        api.RuntimeUpgradeExternalFenceDeliveryConnections,
		MaxResponseHeaderBytes: api.RuntimeUpgradeExternalFenceHeaderMaxBytes,
		ResponseHeaderTimeout:  api.RuntimeUpgradeExternalFenceDeliveryTimeout,
		DialTLSContext:         receiptTLSDialer(tlsConfig, credential),
	}
	return &ReceiptClient{endpoint: u.String(), verifier: verifier, client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func receiptTLS(config ReceiptClientConfig) (*tls.Config, *tls.Certificate, error) {
	for _, input := range [][]byte{config.ServerRootsPEM, config.ClientCertificatePEM, config.ClientPrivateKeyPEM} {
		if len(input) == 0 || len(input) > api.RuntimeUpgradeExternalFenceTLSMaterialMaxBytes {
			return nil, nil, deliveryError("bounded TLS credentials")
		}
	}
	roots, err := receiptRoots(config.ServerRootsPEM)
	if err != nil {
		return nil, nil, err
	}
	credential, err := tls.X509KeyPair(config.ClientCertificatePEM, config.ClientPrivateKeyPEM)
	if err != nil {
		return nil, nil, deliveryError("client certificate and private key")
	}
	pin := config.ServerSPKISHA256
	return &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, NextProtos: []string{"http/1.1"},
		VerifyConnection: func(state tls.ConnectionState) error {
			// Normal chain, expiry and hostname verification runs first. A pin
			// cannot replace it or trust an otherwise invalid certificate.
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return deliveryError("verified server certificate")
			}
			digest := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if hex.EncodeToString(digest[:]) != pin {
				return deliveryError("server public key pin")
			}
			return nil
		},
	}, &credential, nil
}

func receiptTLSDialer(config *tls.Config, credential *tls.Certificate) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		connectionConfig := config.Clone()
		var requested atomic.Bool
		connectionConfig.GetClientCertificate = func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			if err := info.SupportsCertificate(credential); err != nil {
				return nil, deliveryError("compatible client credential")
			}
			requested.Store(true)
			return credential, nil
		}
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: api.RuntimeUpgradeExternalFenceDeliveryTimeout}, Config: connectionConfig}
		connection, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		// Refuse to send the intent unless the peer requested client identity.
		// No resumption cache or pooled connection can bypass this handshake.
		if !requested.Load() {
			_ = connection.Close()
			return nil, deliveryError("client authentication required")
		}
		return connection, nil
	}
}

func receiptRoots(raw []byte) (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	count := 0
	for raw = bytes.TrimSpace(raw); len(raw) > 0; raw = bytes.TrimSpace(raw) {
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, deliveryError("explicit server roots")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, deliveryError("server root encoding")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, deliveryError("server root certificate")
		}
		roots.AddCert(certificate)
		count++
		raw = rest
	}
	if count == 0 {
		return nil, deliveryError("explicit server roots")
	}
	return roots, nil
}

func receiptEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) == 0 || len(endpoint) > api.RuntimeUpgradeExternalFenceEndpointMaxBytes || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" || u.Path != ReceiptLookupPath || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.String() != endpoint || !receiptHostname(u.Hostname()) {
		return nil, deliveryError("canonical HTTPS lookup URL")
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 || strconv.FormatUint(value, 10) != port {
			return nil, deliveryError("canonical HTTPS port")
		}
		host += ":" + port
	}
	if host != u.Host {
		return nil, deliveryError("canonical HTTPS host")
	}
	return u, nil
}

func receiptHostname(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.String() == host && address.Zone() == "" && !address.Is4In6() && !address.IsUnspecified() && !address.IsMulticast()
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' || strings.IndexFunc(label, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' }) != -1 {
			return false
		}
	}
	return true
}
