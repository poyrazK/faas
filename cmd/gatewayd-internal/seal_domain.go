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

// envSealDomainEdgeRetired marks a compute node whose edge identity is
// retired (ADR-20261110210000, retire compute identities): the control
// plane's secretd opens every edge value for it.
const envSealDomainEdgeRetired = "FAAS_SEAL_DOMAIN_EDGE_RETIRED"

// edgeRetiredFromEnv reads FAAS_SEAL_DOMAIN_EDGE_RETIRED. A retired gateway
// holds no identity that opens an edge value, so each kind of edge value
// must reach it another way: basic auth through argon2id verifiers alone,
// log drain headers through secretd, and sealed edge-rule request headers
// injected by the instance's vmmd. It must not load the edge identity, nor
// the fleet and host identities (the edge contract step unsets
// FAAS_HOST_KEY_PATH). Anything left refuses the start rather than run with
// a key the node should no longer hold.
func edgeRetiredFromEnv(getenv func(string) string) (bool, error) {
	switch v := getenv(envSealDomainEdgeRetired); v {
	case "", "false":
		return false, nil
	case "true":
	default:
		return false, fmt.Errorf("gatewayd-internal: %s=%q: want true or false", envSealDomainEdgeRetired, v)
	}
	for _, need := range []struct{ env, want, why string }{
		{"FAAS_GATEWAY_BASIC_AUTH", "verifier-only", "basic auth would open sealed credentials"},
		{"FAAS_GATEWAY_LOG_DRAIN_OPENER", "secretd", "log drain headers would open here"},
		{"FAAS_GATEWAY_EDGE_HEADER_SECRETS", "inject", "sealed edge-rule request headers would open here"},
	} {
		if got := getenv(need.env); got != need.want {
			return false, fmt.Errorf("gatewayd-internal: %s needs %s=%s (got %q): otherwise %s", envSealDomainEdgeRetired, need.env, need.want, got, need.why)
		}
	}
	if getenv(envSealDomainEdgeIdentity) != "" {
		return false, fmt.Errorf("gatewayd-internal: %s but %s is set: remove the edge identity (seal_domains role, faas_seal_domain_edge_retired_on_compute) before starting", envSealDomainEdgeRetired, envSealDomainEdgeIdentity)
	}
	if getenv("FAAS_HOST_KEY_PATH") != "" {
		return false, fmt.Errorf("gatewayd-internal: %s needs the edge contract step (faas_seal_domain_edge_contracted): FAAS_HOST_KEY_PATH is set, and the fleet identity also opens edge values", envSealDomainEdgeRetired)
	}
	return true, nil
}
