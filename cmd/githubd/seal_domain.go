package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// Source seal domain (ADR 20261022111812). githubd is the only reader of
// GitHub install tokens and webhook secrets, and it writes the install
// tokens itself: each refresh seals a new token, so a token sealed before
// the domain expires within the hour and its successor follows the write
// mode. apid seals webhook secrets to the domain's public recipient.
const (
	envSealDomainSourceIdentity = "FAAS_SEAL_DOMAIN_SOURCE_IDENTITY_PATH"
	envSealDomainSourceWrites   = "FAAS_SEAL_DOMAIN_SOURCE_WRITES"
)

// sourceSealDomain is githubd's view of the source domain.
type sourceSealDomain struct {
	// identities are the domain identity and its previous twin; nil until
	// the seal_domains role stages the domain.
	identities []*age.X25519Identity
	mode       sealdomain.WriteMode
}

func loadSourceSealDomain(getenv func(string) string) (sourceSealDomain, error) {
	mode, err := sealdomain.ParseWriteMode(getenv(envSealDomainSourceWrites))
	if err != nil {
		return sourceSealDomain{}, fmt.Errorf("githubd: %s: %w", envSealDomainSourceWrites, err)
	}
	d := sourceSealDomain{mode: mode}
	if path := getenv(envSealDomainSourceIdentity); path != "" {
		if d.identities, err = sealdomain.LoadReaderIdentities(path); err != nil {
			return sourceSealDomain{}, fmt.Errorf("githubd: load source seal-domain identity: %w", err)
		}
	}
	if (mode == sealdomain.WriteDual || mode == sealdomain.WriteDomain) && len(d.identities) == 0 {
		return sourceSealDomain{}, fmt.Errorf("githubd: %s=%s needs %s", envSealDomainSourceWrites, mode, envSealDomainSourceIdentity)
	}
	return d, nil
}

// staged reports whether the domain identity is loaded.
func (d sourceSealDomain) staged() bool { return len(d.identities) > 0 }

// readers returns the identities that open source-domain values: the domain
// first, then legacy (the fleet and host identities) for values sealed
// before the domain.
func (d sourceSealDomain) readers(legacy ...*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(d.identities)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), d.identities...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}

// recipients returns who githubd seals install tokens to under the write
// mode: the fleet recipient until the domain is staged, then the domain and
// the fleet recipient, then the domain alone.
func (d sourceSealDomain) recipients(fleet *age.X25519Recipient) ([]*age.X25519Recipient, error) {
	var domain *age.X25519Recipient
	if d.staged() {
		domain = d.identities[0].Recipient()
	}
	return sealdomain.Recipients(d.mode, domain, fleet)
}
