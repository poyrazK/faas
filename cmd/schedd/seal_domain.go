package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// envSealDomainDeliveryRetired marks a compute node whose delivery identity
// is retired (ADR-20261110210000, retire compute identities).
const envSealDomainDeliveryRetired = "FAAS_SEAL_DOMAIN_DELIVERY_RETIRED"

// deliveryRetiredFromEnv reads FAAS_SEAL_DOMAIN_DELIVERY_RETIRED. A retired
// schedd holds no identity that opens a delivery value: it asks secretd for
// trigger credentials (FAAS_SCHEDD_DELIVERY_OPENER=secretd, which already
// leaves webhooks, execution dispatch and the commit relay to the control
// plane), loads no delivery identity, and has no fleet identity in
// hostKeyDir (schedd's contract step), since apid also seals trigger
// credentials to the fleet key. Anything left refuses the start.
func deliveryRetiredFromEnv(getenv func(string) string, deliveryViaSecretd bool, hostKeyDir string) (bool, error) {
	switch v := getenv(envSealDomainDeliveryRetired); v {
	case "", "false":
		return false, nil
	case "true":
	default:
		return false, fmt.Errorf("schedd: %s=%q: want true or false", envSealDomainDeliveryRetired, v)
	}
	if !deliveryViaSecretd {
		return false, fmt.Errorf("schedd: %s needs FAAS_SCHEDD_DELIVERY_OPENER=secretd: nothing else would open delivery values", envSealDomainDeliveryRetired)
	}
	if getenv(envSealDomainDeliveryIdentity) != "" {
		return false, fmt.Errorf("schedd: %s but %s is set: remove the delivery identity (seal_domains role, faas_seal_domain_delivery_retired_on_compute) before starting", envSealDomainDeliveryRetired, envSealDomainDeliveryIdentity)
	}
	// Any fleet identity file refuses, readable or not: the names are the
	// ones secretbox.LoadFleetAndHostKeys looks for.
	for _, name := range []string{"fleet.age", "faas_fleet_age_identity", "fleet.age.previous", "faas_fleet_age_identity_previous"} {
		path := filepath.Join(hostKeyDir, name)
		if _, err := os.Lstat(path); err == nil {
			return false, fmt.Errorf("schedd: %s needs schedd's contract step (faas_seal_domain_control_contracted and faas_seal_domain_delivery_contracted): %s also opens delivery values", envSealDomainDeliveryRetired, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("schedd: %s: check for a fleet identity: %w", envSealDomainDeliveryRetired, err)
		}
	}
	return true, nil
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
