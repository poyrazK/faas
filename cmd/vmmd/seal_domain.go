package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
//
// A node whose runtime identity is retired (ADR-20261110210000 slice 3e)
// opens runtime secrets through secretd and holds no runtime identity; one
// whose build identity is retired (slice 3g-3) pulls builder registries
// through secretd and holds no build identity. Neither needs its retired
// domain.
func unsealIdentities(runtime, build, legacy []*age.X25519Identity, contracted, runtimeRetired, buildRetired bool) (host, registry []*age.X25519Identity, err error) {
	if contracted {
		var missing []string
		if !runtimeRetired && len(runtime) == 0 {
			missing = append(missing, "domains/runtime.age")
		}
		if !buildRetired && len(build) == 0 {
			missing = append(missing, "domains/build.age")
		}
		if len(missing) > 0 {
			return nil, nil, fmt.Errorf("vmmd: FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED needs %s staged next to host.age", strings.Join(missing, " and "))
		}
	}
	return slices.Concat(runtime, legacy), slices.Concat(build, legacy), nil
}

// checkBuildRetired checks a node whose build identity is retired
// (ADR-20261110210000 slice 3g-3). Such a node must hold no private key that
// opens an app's registry password: it is contracted (no fleet or host
// identity), builder VMs pull through the secretd-backed vsock mirror, and
// no build identity is left on disk; any leftover refuses the start rather
// than run with it.
func checkBuildRetired(secretsDir string, contracted bool, mirrorMode string) error {
	if !contracted {
		return errors.New("vmmd: FAAS_SEAL_DOMAIN_BUILD_RETIRED needs FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED: the fleet identity also opens registry passwords")
	}
	if mirrorMode != "secretd" {
		return errors.New("vmmd: FAAS_SEAL_DOMAIN_BUILD_RETIRED needs FAAS_VMMD_BUILD_REGISTRY_MIRROR=secretd: nothing else would give builder VMs their app's private registries")
	}
	dir := filepath.Join(secretsDir, "domains")
	for _, leftover := range []string{
		sealdomain.IdentityPath(dir, sealdomain.Build),
		sealdomain.PreviousIdentityPath(dir, sealdomain.Build),
	} {
		if _, err := os.Lstat(leftover); err == nil {
			return fmt.Errorf("vmmd: FAAS_SEAL_DOMAIN_BUILD_RETIRED but %s is still on this host: remove it (seal_domains role, faas_seal_domain_build_retired_on_compute) before starting", leftover)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("vmmd: check for a retired build identity: %w", err)
		}
	}
	return nil
}

// runtimeRetiredRecipient checks a node whose runtime identity is retired
// (ADR-20261110210000 slice 3e) and returns the runtime domain's public
// recipient, which it seals crash-capture keys to so that only the control
// plane's secretd opens them. Such a node must hold no private key that
// opens a tenant's runtime secrets: it is contracted (no fleet or host
// identity), opens through secretd, and has no runtime identity left on
// disk; any leftover refuses the start rather than run with it.
func runtimeRetiredRecipient(secretsDir string, contracted bool, openerMode string) (*age.X25519Recipient, error) {
	if !contracted {
		return nil, errors.New("vmmd: FAAS_SEAL_DOMAIN_RUNTIME_RETIRED needs FAAS_SEAL_DOMAIN_RUNTIME_CONTRACTED: the fleet identity also opens runtime secrets")
	}
	if openerMode != "secretd" {
		return nil, errors.New("vmmd: FAAS_SEAL_DOMAIN_RUNTIME_RETIRED needs FAAS_RUNTIME_SECRET_OPENER=secretd: nothing else would open runtime secrets")
	}
	dir := filepath.Join(secretsDir, "domains")
	for _, leftover := range []string{
		sealdomain.IdentityPath(dir, sealdomain.Runtime),
		sealdomain.PreviousIdentityPath(dir, sealdomain.Runtime),
	} {
		if _, err := os.Lstat(leftover); err == nil {
			return nil, fmt.Errorf("vmmd: FAAS_SEAL_DOMAIN_RUNTIME_RETIRED but %s is still on this host: remove it (seal_domains role, faas_seal_domain_runtime_retired_on_compute) before starting", leftover)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("vmmd: check for a retired runtime identity: %w", err)
		}
	}
	raw, err := os.ReadFile(sealdomain.RecipientPath(dir, sealdomain.Runtime))
	if err != nil {
		return nil, fmt.Errorf("vmmd: FAAS_SEAL_DOMAIN_RUNTIME_RETIRED needs the runtime recipient: %w", err)
	}
	recipient, err := age.ParseX25519Recipient(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("vmmd: parse the runtime recipient: %w", err)
	}
	return recipient, nil
}
