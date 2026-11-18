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

// envSealDomainBuildRetired marks a compute node whose build identity is
// retired (ADR-20261110210000 slice 3g-3).
const envSealDomainBuildRetired = "FAAS_SEAL_DOMAIN_BUILD_RETIRED"

// buildRetiredFromEnv reads FAAS_SEAL_DOMAIN_BUILD_RETIRED. A retired imaged
// holds no identity that opens a registry password: it pulls private images
// through secretd (FAAS_IMAGED_REGISTRY_PULLS=secretd), loads no build
// identity, and runs contracted, without the fleet and host identities
// (FAAS_HOST_AGE_IDENTITY_PATH), since passwords sealed before the domain
// open with them. Anything left refuses the start.
func buildRetiredFromEnv(getenv func(string) string, pullsViaSecretd bool) (bool, error) {
	switch v := getenv(envSealDomainBuildRetired); v {
	case "", "false":
		return false, nil
	case "true":
	default:
		return false, fmt.Errorf("imaged: %s=%q: want true or false", envSealDomainBuildRetired, v)
	}
	if !pullsViaSecretd {
		return false, fmt.Errorf("imaged: %s needs FAAS_IMAGED_REGISTRY_PULLS=secretd: nothing else would pull apps' private images", envSealDomainBuildRetired)
	}
	if getenv(envSealDomainBuildIdentity) != "" {
		return false, fmt.Errorf("imaged: %s but %s is set: remove the build identity (seal_domains role, faas_seal_domain_build_retired_on_compute) before starting", envSealDomainBuildRetired, envSealDomainBuildIdentity)
	}
	if getenv("FAAS_HOST_AGE_IDENTITY_PATH") != "" {
		return false, fmt.Errorf("imaged: %s needs the build contract step (faas_seal_domain_build_contracted): the fleet and host identities at FAAS_HOST_AGE_IDENTITY_PATH also open registry passwords", envSealDomainBuildRetired)
	}
	return true, nil
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
