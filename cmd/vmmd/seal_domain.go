package main

import (
	"errors"
	"path/filepath"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// loadRuntimeSealDomain loads the runtime seal-domain identity (ADR
// 20261022111812) and its previous twin from secretsDir/domains, next to
// host.age. vmmd is root and reads the file directly. A node that has not
// been given the domain yet returns no identities and keeps opening
// runtime secrets with the fleet identity.
func loadRuntimeSealDomain(secretsDir string) ([]*age.X25519Identity, error) {
	ids, err := sealdomain.LoadReaderIdentities(sealdomain.IdentityPath(filepath.Join(secretsDir, "domains"), sealdomain.Runtime))
	if errors.Is(err, sealdomain.ErrNoIdentity) {
		return nil, nil
	}
	return ids, err
}

// registerSealDomainLoaded exports faas_seal_domain_loaded{domain}: 1 when
// this vmmd loaded the domain identity at start. Writers may seal runtime
// secrets to the domain alone only once every vmmd reports 1.
func registerSealDomainLoaded(reg prometheus.Registerer, d sealdomain.Domain, loaded bool) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "faas_seal_domain_loaded",
		Help: "1 when this daemon loaded the seal domain's identity at start (ADR 20261022111812).",
	}, []string{"domain"})
	value := 0.0
	if loaded {
		value = 1
	}
	g.WithLabelValues(string(d)).Set(value)
	reg.MustRegister(g)
}
