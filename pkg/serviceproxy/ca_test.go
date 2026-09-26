package serviceproxy

import (
	"crypto/x509"
	"testing"
)

func TestValidateInternalServiceCA(t *testing.T) {
	for _, tc := range []struct {
		name string
		cert *x509.Certificate
		ok   bool
	}{
		{
			name: "critical internal suffix constraint",
			cert: &x509.Certificate{IsCA: true, BasicConstraintsValid: true, PermittedDNSDomainsCritical: true, PermittedDNSDomains: []string{".internal"}},
			ok:   true,
		},
		{
			name: "missing constraints",
			cert: &x509.Certificate{IsCA: true, BasicConstraintsValid: true},
		},
		{
			name: "non-critical constraints",
			cert: &x509.Certificate{IsCA: true, BasicConstraintsValid: true, PermittedDNSDomains: []string{".internal"}},
		},
		{
			name: "broader constraint",
			cert: &x509.Certificate{IsCA: true, BasicConstraintsValid: true, PermittedDNSDomainsCritical: true, PermittedDNSDomains: []string{".internal", ".example.com"}},
		},
		{
			name: "wrong suffix",
			cert: &x509.Certificate{IsCA: true, BasicConstraintsValid: true, PermittedDNSDomainsCritical: true, PermittedDNSDomains: []string{".example.com"}},
		},
		{
			name: "not a CA",
			cert: &x509.Certificate{BasicConstraintsValid: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInternalServiceCA(tc.cert)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateInternalServiceCA() error = %v, want success %v", err, tc.ok)
			}
		})
	}
}
