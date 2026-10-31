package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// outboundSealDomainLoaded reports whether the outbound seal-domain identity
// (ADR 20261022111812) at domainPath loads. An unset path is a node the
// seal_domains role has not staged yet; a set path that does not load is an
// error, as for the other domains' readers. The answer feeds
// faas_seal_domain_loaded{domain="outbound"}, which
// FaasSealDomainIdentityMissing watches.
func outboundSealDomainLoaded(domainPath string) (bool, error) {
	if domainPath == "" {
		return false, nil
	}
	if _, err := sealdomain.LoadReaderIdentities(domainPath); err != nil {
		return false, fmt.Errorf("outboundd: load outbound seal-domain identity: %w", err)
	}
	return true, nil
}
