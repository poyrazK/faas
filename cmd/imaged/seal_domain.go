package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// envSealDomainBuildIdentity names the build seal-domain identity (ADR
// 20261022111812), delivered as a LoadCredential drop-in by the seal_domains
// role. imaged is the only reader of private-registry passwords.
const envSealDomainBuildIdentity = "FAAS_SEAL_DOMAIN_BUILD_IDENTITY_PATH"

// loadBuildSealDomain loads the build seal-domain identity and its previous
// twin once the role stages them; nil before.
func loadBuildSealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	path := getenv(envSealDomainBuildIdentity)
	if path == "" {
		return nil, nil
	}
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("imaged: load build seal-domain identity: %w", err)
	}
	return ids, nil
}

// registryReaders returns the identities that open private-registry
// passwords: the build domain first, then legacy (the fleet and host
// identities) for passwords sealed before the domain, each once.
func registryReaders(build, legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(build)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), build...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}
