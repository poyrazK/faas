package pki

import (
	"errors"
	"fmt"
	"slices"
)

// Feature leaves are certificates a host carries only once an operator turns
// on the feature that needs them. They are deliberately not in Roles or
// RolesForBox: nothing requires them, so trust-bundle validation, bundle
// installs and renewal of the required set never look at them, and adding
// one never blocks a rollout of hosts that lack it (the v0.1.18-rc.255
// failure TestRolesForBoxMatchLiveHosts pins). Each lives in its own
// directory under the PKI root, which InstallTrustBundle leaves alone, and
// the feature's own deployment issues and renews it with EnsureFeatureLeaves.

// FeatureLiveQueries is the live-query hub's mTLS (ADR-20261016151947):
// livequeryd's server leaf on the control plane, and realtimed's client leaf
// on the hosts that run realtimed. realtimed verifies hubs as
// livequeryd.faas, and livequeryd accepts only realtimed.faas.
const FeatureLiveQueries = "live-queries"

// Live-query leaf identities.
const (
	LivequerydServerName = "livequeryd.faas"
	RealtimedLiveClient  = "realtimed.faas"
)

// Features lists the known feature-leaf sets.
func Features() []string { return []string{FeatureLiveQueries} }

// FeatureRoles returns the leaves a host of boxRole carries for feature, or
// nil for an unknown feature or a box role that carries none of it.
func FeatureRoles(feature, boxRole string) []Role {
	if feature != FeatureLiveQueries {
		return nil
	}
	server := Role{CommonName: LivequerydServerName, Kind: KindServer, Directory: "livequeryd", Filename: "server", AltNames: ProductionSANs(LivequerydServerName)}
	client := Role{CommonName: RealtimedLiveClient, Kind: KindClient, Directory: "realtimed", Filename: "livequeryd-client", AltNames: ProductionSANs(RealtimedLiveClient)}
	switch boxRole {
	case "control-plane":
		return []Role{server}
	case "compute-only":
		return []Role{client}
	case "single-box", "":
		return []Role{server, client}
	default:
		return nil
	}
}

// EnsureFeatureLeaves issues feature's leaves for boxRole under rootDir,
// signed by the CA under caRoot. A leaf is issued when it is missing, expires
// within ReissueThreshold, or lacks a SAN; otherwise it is kept. It returns
// the leaves it issued. caRoot must already hold a CA: this never creates one.
func EnsureFeatureLeaves(caRoot, rootDir, feature, boxRole string) ([]Role, error) {
	if !slices.Contains(Features(), feature) {
		return nil, fmt.Errorf("pki: unknown feature %q (known: %v)", feature, Features())
	}
	roles := FeatureRoles(feature, boxRole)
	if len(roles) == 0 {
		return nil, fmt.Errorf("pki: feature %q has no leaves for box role %q", feature, boxRole)
	}
	caCert, caKey, err := loadExistingCA(CARoot(caRoot))
	if err != nil {
		return nil, err
	}
	if caCert == nil {
		return nil, fmt.Errorf("pki: no CA under %q; feature leaves are issued only by the CA's host", caRoot)
	}
	var issued []Role
	for _, role := range roles {
		err := EnsureLeaf(rootDir, role, caCert, caKey, false)
		if errors.Is(err, ErrLeafNotExpiringSoon) {
			continue
		}
		if err != nil {
			return issued, fmt.Errorf("pki: feature leaf %s/%s: %w", role.Directory, role.Filename, err)
		}
		issued = append(issued, role)
	}
	return issued, nil
}
