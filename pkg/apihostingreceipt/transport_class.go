package apihostingreceipt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"
)

// transportFailureClass names, without the cause's text (which can carry a
// challenge token or credentials), why a verification request never got an
// HTTP response: the verifier origin's DNS, a refused or reset connection, a
// timeout, or TLS. "" when the class is unknown.
func transportFailureClass(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var recordErr tls.RecordHeaderError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return "verifier origin " + boundedHost(dnsErr.Name) + " has no DNS record"
		}
		return "verifier origin DNS lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "verifier origin unreachable"
	case errors.As(err, &certErr), errors.As(err, &unknownAuthority), errors.As(err, &hostnameErr), errors.As(err, &recordErr):
		return "TLS handshake failed"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	}
	return ""
}

// boundedHost keeps a DNS name printable ASCII and at most 253 bytes.
func boundedHost(name string) string {
	const limit = 253
	out := make([]byte, 0, min(len(name), limit))
	for i := 0; i < len(name) && len(out) < limit; i++ {
		if c := name[i]; c > 0x20 && c < 0x7f {
			out = append(out, c)
		}
	}
	return string(out)
}
