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
// vmmd's relay client leaf is the one exception (SharedDirectoryFeatureLeaf):
// it sits beside vmmd's required leaves, at the path vmmd's relay drop-in
// has named on every split-box compute node since ADR-989. Trust-bundle
// installs and renewals replace only the required leaves' own files, so they
// leave it alone too.

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

// FeaturePlatformLogs is the platform log pipeline's mTLS
// (ADR-20261023124955 §2): Loki's server leaf on the control plane, and the
// client leaves of the Promtail on every host and of Prometheus and Grafana
// on the control plane. Every client verifies Loki as loki.faas. Loki
// accepts any leaf this CA signed, so the private-network rule on its port is
// what keeps other hosts out.
const FeaturePlatformLogs = "platform-logs"

// Platform-log leaf identities.
const (
	LokiServerName       = "loki.faas"
	PromtailLokiClient   = "promtail.faas"
	PrometheusLokiClient = "prometheus.faas"
	GrafanaLokiClient    = "grafana.faas"
)

// FeatureOutboundRelay is the ADR-989 app relay's mTLS on a split box:
// outboundd's relay server leaf on the control plane, and vmmd's relay client
// leaf on each compute node. vmmd verifies the relay as outboundd.faas, and
// outboundd accepts a client only when its CN is a registered compute node,
// so the client leaf carries the node's identity (RoleUsesNodeIdentity: it
// is in vmmd's directory).
const FeatureOutboundRelay = "outbound-relay"

// Outbound relay leaf identities. The client leaf's CN is the compute node's
// name; VmmdRelayClient is only its placeholder in the role table.
const (
	OutbounddRelayServerName = "outboundd.faas"
	VmmdRelayClient          = "vmmd.faas"
)

// SharedDirectoryFeatureLeaf reports whether a feature leaf may sit in a
// required leaf's directory: only vmmd's relay client leaf does.
func SharedDirectoryFeatureLeaf(role Role) bool {
	return role.Directory == "vmmd" && role.Filename == "outboundd-client"
}

// Features lists the known feature-leaf sets.
func Features() []string {
	return []string{FeatureLiveQueries, FeaturePlatformLogs, FeatureOutboundRelay}
}

// FeatureRoles returns the leaves a host of boxRole carries for feature, or
// nil for an unknown feature or a box role that carries none of it.
func FeatureRoles(feature, boxRole string) []Role {
	var controlPlane, compute []Role
	switch feature {
	case FeatureLiveQueries:
		controlPlane = []Role{{CommonName: LivequerydServerName, Kind: KindServer, Directory: "livequeryd", Filename: "server", AltNames: ProductionSANs(LivequerydServerName)}}
		compute = []Role{{CommonName: RealtimedLiveClient, Kind: KindClient, Directory: "realtimed", Filename: "livequeryd-client", AltNames: ProductionSANs(RealtimedLiveClient)}}
	case FeaturePlatformLogs:
		promtail := Role{CommonName: PromtailLokiClient, Kind: KindClient, Directory: "promtail", Filename: "loki-client", AltNames: ProductionSANs(PromtailLokiClient)}
		controlPlane = []Role{
			{CommonName: LokiServerName, Kind: KindServer, Directory: "loki", Filename: "server", AltNames: ProductionSANs(LokiServerName)},
			promtail,
			{CommonName: PrometheusLokiClient, Kind: KindClient, Directory: "prometheus", Filename: "loki-client", AltNames: ProductionSANs(PrometheusLokiClient)},
			{CommonName: GrafanaLokiClient, Kind: KindClient, Directory: "grafana", Filename: "loki-client", AltNames: ProductionSANs(GrafanaLokiClient)},
		}
		compute = []Role{promtail}
	case FeatureOutboundRelay:
		controlPlane = []Role{{CommonName: OutbounddRelayServerName, Kind: KindServer, Directory: "outboundd", Filename: "server", AltNames: ProductionSANs(OutbounddRelayServerName)}}
		compute = []Role{{CommonName: VmmdRelayClient, Kind: KindClient, Directory: "vmmd", Filename: "outboundd-client", AltNames: ProductionSANs(VmmdRelayClient)}}
	default:
		return nil
	}
	switch boxRole {
	case "control-plane":
		return controlPlane
	case "compute-only":
		return compute
	case "single-box", "":
		return featureUnion(controlPlane, compute)
	default:
		return nil
	}
}

// featureUnion joins two leaf lists, keeping one copy of a leaf both hold.
func featureUnion(a, b []Role) []Role {
	out := append([]Role{}, a...)
	for _, role := range b {
		if !slices.ContainsFunc(out, func(r Role) bool { return r.Directory == role.Directory && r.Filename == role.Filename }) {
			out = append(out, role)
		}
	}
	return out
}

// EnsureFeatureLeaves issues feature's leaves for boxRole under rootDir,
// signed by the CA under caRoot. A leaf is issued when it is missing, expires
// within ReissueThreshold, or lacks a SAN; otherwise it is kept. It returns
// the leaves it issued. caRoot must already hold a CA: this never creates one.
func EnsureFeatureLeaves(caRoot, rootDir, feature, boxRole string) ([]Role, error) {
	return EnsureFeatureLeavesForNode(caRoot, rootDir, feature, boxRole, "")
}

// EnsureFeatureLeavesForNode is EnsureFeatureLeaves for a compute node whose
// feature leaves include a node-identity leaf: that leaf's CN is nodeCN, and
// a kept leaf must already carry it. A compute-only node-identity leaf
// without a nodeCN is refused, because the control plane would reject it.
func EnsureFeatureLeavesForNode(caRoot, rootDir, feature, boxRole, nodeCN string) ([]Role, error) {
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
		commonName := ""
		if boxRole == "compute-only" && RoleUsesNodeIdentity(role) {
			if nodeCN == "" {
				return issued, fmt.Errorf("pki: feature leaf %s/%s carries the compute node's identity: pass its node name", role.Directory, role.Filename)
			}
			commonName = nodeCN
		}
		err := ensureLeafWithIdentity(rootDir, role, commonName, caCert, caKey, false, AltNames{})
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
