package main

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// basicAuthVerifiersFromEnv reads FAAS_GATEWAY_BASIC_AUTH
// (ADR-20261110210000 slice 3h):
//
//	sealed (or unset)  open the sealed credential with the edge identity
//	verifier           check against the argon2id verifier; open the sealed
//	                   credential only for a blob that has no verifier yet
//	verifier-only      check against verifiers alone; a blob without one
//	                   refuses every credential, and the edge identity is not
//	                   needed for basic auth
func basicAuthVerifiersFromEnv(getenv func(string) string, store *state.PgStore, reg prometheus.Registerer) (*gateway.BasicAuthVerifiers, error) {
	mode := getenv("FAAS_GATEWAY_BASIC_AUTH")
	var fallback bool
	switch mode {
	case "", "sealed":
		return nil, nil
	case "verifier":
		fallback = true
	case "verifier-only":
	default:
		return nil, fmt.Errorf("gatewayd-internal: FAAS_GATEWAY_BASIC_AUTH=%q: want sealed, verifier or verifier-only", mode)
	}
	if store == nil {
		return nil, errors.New("gatewayd-internal: FAAS_GATEWAY_BASIC_AUTH=" + mode + " needs the database")
	}
	return gateway.NewBasicAuthVerifiers(store, fallback, reg), nil
}
