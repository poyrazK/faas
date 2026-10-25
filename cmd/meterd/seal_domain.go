package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// envSealDomainAlertingIdentity names the alerting seal-domain identity (ADR
// 20261022111812), delivered as a LoadCredential drop-in by the seal_domains
// role. meterd is the only reader of alert rule webhook secrets,
// notification channel destinations and MCP readiness provider keys.
const envSealDomainAlertingIdentity = "FAAS_SEAL_DOMAIN_ALERTING_IDENTITY_PATH"

// loadAlertingSealDomain loads the alerting seal-domain identity and its
// previous twin once the role stages them; nil before.
func loadAlertingSealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	path := getenv(envSealDomainAlertingIdentity)
	if path == "" {
		return nil, nil
	}
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("meterd: load alerting seal-domain identity: %w", err)
	}
	return ids, nil
}

// alertingReaders returns the identities that open alerting values: the
// alerting domain first, then legacy (the fleet and host identities) for
// values sealed before the domain, each once.
func alertingReaders(alerting, legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(alerting)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), alerting...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}
