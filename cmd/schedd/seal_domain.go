package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// envSealDomainControlIdentity names the control seal-domain identity (ADR
// 20261022111812), delivered as a LoadCredential drop-in by the seal_domains
// role. schedd is the only reader of the cluster signing key, on every node.
const envSealDomainControlIdentity = "FAAS_SEAL_DOMAIN_CONTROL_IDENTITY_PATH"

// loadControlSealDomain loads the control seal-domain identity and its
// previous twin once the role stages them; nil before.
func loadControlSealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	path := getenv(envSealDomainControlIdentity)
	if path == "" {
		return nil, nil
	}
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("schedd: load control seal-domain identity: %w", err)
	}
	return ids, nil
}

// controlReaders returns the identities that open the cluster signing key: the
// control domain first, then legacy (the fleet and host identities) for a
// key sealed before the domain, each once.
func controlReaders(control, legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(control)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), control...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}
