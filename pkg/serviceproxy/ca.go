// Package serviceproxy contains shared certificate policy for private service
// bindings.
package serviceproxy

import (
	"crypto/x509"
	"errors"
)

// ValidateInternalServiceCA ensures a root installed into workload TLS trust
// is constrained to Gregale's private DNS suffix. The critical constraint is
// enforced by certificate verifiers, preventing this CA from authenticating
// public DNS names when clients use their ordinary system roots.
func ValidateInternalServiceCA(cert *x509.Certificate) error {
	if cert == nil || !cert.IsCA || !cert.BasicConstraintsValid {
		return errors.New("certificate is not a valid CA")
	}
	if !cert.PermittedDNSDomainsCritical || len(cert.PermittedDNSDomains) != 1 || cert.PermittedDNSDomains[0] != ".internal" {
		return errors.New("CA must have a critical permitted DNS name constraint for .internal")
	}
	return nil
}
