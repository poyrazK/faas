package main

import (
	"errors"
	"path/filepath"
	"slices"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// loadRuntimeSealDomain loads the runtime seal-domain identity (ADR
// 20261022111812) and its previous twin from secretsDir/domains, next to
// host.age. vmmd is root and reads the file directly. A node that has not
// been given the domain yet returns no identities and keeps opening
// runtime secrets with the fleet identity.
func loadRuntimeSealDomain(secretsDir string) ([]*age.X25519Identity, error) {
	return loadSealDomain(secretsDir, sealdomain.Runtime)
}

// loadBuildSealDomain loads the build seal-domain identity, which opens the
// registry passwords a builder VM pulls its base images with
// (ADR-20261026091500). A node without it keeps opening older passwords
// with the fleet and host identities.
func loadBuildSealDomain(secretsDir string) ([]*age.X25519Identity, error) {
	return loadSealDomain(secretsDir, sealdomain.Build)
}

func loadSealDomain(secretsDir string, domain sealdomain.Domain) ([]*age.X25519Identity, error) {
	ids, err := sealdomain.LoadReaderIdentities(sealdomain.IdentityPath(filepath.Join(secretsDir, "domains"), domain))
	if errors.Is(err, sealdomain.ErrNoIdentity) {
		return nil, nil
	}
	return ids, err
}

// unsealIdentities returns what vmmd opens runtime secrets (host) and
// registry passwords (registry) with: each domain's identities first, then
// the legacy fleet and host identities. A contracted vmmd (ADR
// 20261022111812, contract step) has no legacy identities and needs both
// domains, since nothing else would open what was re-sealed to them.
func unsealIdentities(runtime, build, legacy []*age.X25519Identity, contracted bool) (host, registry []*age.X25519Identity, err error) {
	if contracted && (len(runtime) == 0 || len(build) == 0) {
		return nil, nil, errors.New("vmmd: FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED needs domains/runtime.age and domains/build.age staged next to host.age")
	}
	return slices.Concat(runtime, legacy), slices.Concat(build, legacy), nil
}
