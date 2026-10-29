package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// envSealDomainEdgeIdentity names the edge seal-domain identity (ADR
// 20261022111812), delivered as a LoadCredential drop-in by the seal_domains
// role. gatewayd-internal is the only reader of app basic-auth credentials
// and log drain authentication headers.
const envSealDomainEdgeIdentity = "FAAS_SEAL_DOMAIN_EDGE_IDENTITY_PATH"

// loadEdgeSealDomain loads the edge seal-domain identity and its previous
// twin once the role stages them; nil before.
func loadEdgeSealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	path := getenv(envSealDomainEdgeIdentity)
	if path == "" {
		return nil, nil
	}
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-internal: load edge seal-domain identity: %w", err)
	}
	return ids, nil
}

// edgeReaders returns the identities that open edge credentials: the edge
// domain first, then legacy (the fleet and host identities) for credentials
// sealed before the domain, each once.
func edgeReaders(edge, legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(edge)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), edge...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}
