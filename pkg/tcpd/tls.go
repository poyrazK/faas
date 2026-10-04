package tcpd

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CertificateProvider returns an immutable snapshot, including its private key.
// Implementations must honor cancellation and must not mutate returned objects.
// Only the public edge owns this provider; keys never enter guest transport.
type CertificateProvider interface {
	Certificate(context.Context, string) (*tls.Certificate, error)
}

// TerminateTLS takes ownership of conn on failure. Success transfers ownership
// of the TLS connection to the caller. Call after reserving session credits and
// before admitting a workload.
func TerminateTLS(ctx context.Context, conn net.Conn, hostname string, provider CertificateProvider) (_ *tls.Conn, err error) {
	if conn == nil {
		return nil, errors.New("TLS requires a connection")
	}
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	if ctx == nil || provider == nil {
		return nil, errors.New("TLS requires a context, certificate provider and DNS hostname")
	}
	policy, err := (api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: hostname}).Normalize()
	if err != nil {
		return nil, err
	}
	hostname = policy.Hostname
	ctx, cancel := context.WithTimeout(ctx, api.TCPListenerTLSHandshakeTimeout)
	defer cancel()
	config := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if !strings.EqualFold(hello.ServerName, hostname) {
			return nil, errors.New("TLS server name does not match listener")
		}
		certificate, err := provider.Certificate(ctx, hostname)
		if err != nil {
			// Provider errors may include storage paths or credential material.
			return nil, errors.New("TLS certificate unavailable")
		}
		if _, err := inspectTLSCertificate(certificate, hostname, time.Now()); err != nil {
			return nil, err
		}
		return certificate, nil
	}}
	secure := tls.Server(conn, config)
	if err := secure.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("listener TLS handshake: %w", err)
	}
	return secure, nil
}

func inspectTLSCertificate(certificate *tls.Certificate, hostname string, now time.Time) (time.Time, error) {
	if certificate == nil || len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
		return time.Time{}, errors.New("TLS certificate incomplete")
	}
	if certificate.SupportedSignatureAlgorithms != nil && len(certificate.SupportedSignatureAlgorithms) == 0 {
		return time.Time{}, errors.New("TLS certificate has no permitted signature algorithms")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return time.Time{}, errors.New("TLS certificate invalid")
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return time.Time{}, errors.New("TLS certificate outside validity period")
	}
	if err := leaf.VerifyHostname(hostname); err != nil {
		return time.Time{}, errors.New("TLS certificate name does not match listener")
	}
	if len(leaf.ExtKeyUsage)+len(leaf.UnknownExtKeyUsage) != 0 {
		serverAuth := false
		for _, usage := range leaf.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
				serverAuth = true
			}
		}
		if !serverAuth {
			return time.Time{}, errors.New("TLS certificate does not permit server authentication")
		}
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return time.Time{}, errors.New("TLS certificate private key unsupported")
	}
	publicKey, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(publicKey, leaf.RawSubjectPublicKeyInfo) {
		return time.Time{}, errors.New("TLS certificate private key does not match")
	}
	return leaf.NotAfter, nil
}
