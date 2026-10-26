package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// schedd reads two seal domains (ADR 20261022111812), each delivered as a
// LoadCredential drop-in by the seal_domains role on every node:
//   - control: the cluster signing key, which schedd alone opens;
//   - delivery: app webhook signing secrets, commit-source connections and
//     execution payloads, which apid seals and schedd opens to deliver them.
const (
	envSealDomainControlIdentity  = "FAAS_SEAL_DOMAIN_CONTROL_IDENTITY_PATH"
	envSealDomainDeliveryIdentity = "FAAS_SEAL_DOMAIN_DELIVERY_IDENTITY_PATH"
)

// loadControlSealDomain loads the control seal-domain identity and its
// previous twin once the role stages them; nil before.
func loadControlSealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	return loadSealDomain(getenv, envSealDomainControlIdentity, sealdomain.Control)
}

// loadDeliverySealDomain loads the delivery seal-domain identity and its
// previous twin once the role stages them; nil before.
func loadDeliverySealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	return loadSealDomain(getenv, envSealDomainDeliveryIdentity, sealdomain.Delivery)
}

func loadSealDomain(getenv func(string) string, env string, domain sealdomain.Domain) ([]*age.X25519Identity, error) {
	path := getenv(env)
	if path == "" {
		return nil, nil
	}
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("schedd: load %s seal-domain identity: %w", domain, err)
	}
	return ids, nil
}

// domainReaders returns the identities that open a domain's values: the
// domain first, then legacy (the fleet and host identities) for values
// sealed before the domain, each once.
func domainReaders(domain, legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(domain)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), domain...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}
