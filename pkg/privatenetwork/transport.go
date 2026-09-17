package privatenetwork

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

const (
	// Linux limits interface names to IFNAMSIZ-1 (15) bytes. Keep the
	// transport link distinct from the per-network bridge while retaining a
	// short, deterministic name that is safe to use in iproute2 commands.
	fabricTransportPrefix = "gpx-"
	fabricTransportHexLen = 10
	maxVXLANVNI           = 16_777_214 // reserve 0 and the all-ones value
	vxlanUDPPort          = 4789
)

// FabricTransportSpec describes the node-local inputs needed to connect one
// Gregale bridge to its regional peers. The underlay is deliberately an
// operator-provided interface (Tailscale, WireGuard, or another encrypted
// overlay); Gregale never needs cloud-provider credentials or APIs.
type FabricTransportSpec struct {
	Fabric           FabricSpec
	OverlayInterface string
	LocalAddress     netip.Addr
	PeerAddresses    []netip.Addr
}

// FabricTransportPlan is a deterministic host-side VXLAN plan. Every node in
// a region uses the same VNI and bridge identity for a given account/network;
// static FDB entries keep traffic unicast over the operator's encrypted
// overlay instead of relying on multicast support from the underlay.
type FabricTransportPlan struct {
	Spec     FabricTransportSpec
	LinkName string
	VNI      uint32
	Setup    [][]string
	Teardown [][]string
}

// BuildFabricTransportPlan validates a transport spec and returns idempotent
// iproute2 commands. L2 VXLAN is intentional: all members already share the
// same private-network CIDR, so a routed L3 overlay would create overlapping
// connected routes on every node.
func BuildFabricTransportPlan(spec FabricTransportSpec) (FabricTransportPlan, error) {
	validatedFabric, err := NewFabricSpecFromValues(
		spec.Fabric.AccountID,
		spec.Fabric.NetworkID,
		spec.Fabric.Region,
		spec.Fabric.CIDR.String(),
	)
	if err != nil {
		return FabricTransportPlan{}, err
	}

	iface := strings.TrimSpace(spec.OverlayInterface)
	if iface == "" {
		return FabricTransportPlan{}, errors.New("privatenetwork: transport overlay interface is required")
	}
	if len(iface) > maxInterfaceName {
		return FabricTransportPlan{}, fmt.Errorf("privatenetwork: transport overlay interface exceeds %d bytes", maxInterfaceName)
	}
	for _, r := range iface {
		if !isInterfaceNameRune(r) {
			return FabricTransportPlan{}, fmt.Errorf("privatenetwork: transport overlay interface contains unsupported character %q", r)
		}
	}

	local := spec.LocalAddress
	if !local.IsValid() || !local.Is4() {
		return FabricTransportPlan{}, errors.New("privatenetwork: transport local address must be an IPv4 address")
	}

	peers := make([]string, 0, len(spec.PeerAddresses))
	seen := make(map[string]struct{}, len(spec.PeerAddresses))
	for _, peer := range spec.PeerAddresses {
		if !peer.IsValid() || !peer.Is4() {
			return FabricTransportPlan{}, fmt.Errorf("privatenetwork: transport peer %q must be an IPv4 address", peer)
		}
		if peer == local {
			return FabricTransportPlan{}, fmt.Errorf("privatenetwork: transport peer %s is the local address", peer)
		}
		key := peer.String()
		if _, ok := seen[key]; ok {
			return FabricTransportPlan{}, fmt.Errorf("privatenetwork: transport peer %s is duplicated", key)
		}
		seen[key] = struct{}{}
		peers = append(peers, key)
	}
	sort.Strings(peers)
	canonicalPeers := make([]netip.Addr, 0, len(peers))
	for _, peer := range peers {
		canonicalPeers = append(canonicalPeers, netip.MustParseAddr(peer))
	}
	validated := FabricTransportSpec{
		Fabric:           validatedFabric,
		OverlayInterface: iface,
		LocalAddress:     local,
		PeerAddresses:    canonicalPeers,
	}

	linkName := FabricTransportLinkName(validatedFabric.AccountID, validatedFabric.NetworkID)
	vni := fabricTransportVNI(validatedFabric.AccountID, validatedFabric.NetworkID)
	bridge := BridgeName(validatedFabric.AccountID, validatedFabric.NetworkID)
	setup := [][]string{
		{"ip", "link", "add", "dev", linkName, "type", "vxlan", "id", fmt.Sprintf("%d", vni), "dev", iface, "local", local.String(), "dstport", fmt.Sprintf("%d", vxlanUDPPort), "nolearning"},
		{"ip", "link", "set", "dev", linkName, "master", bridge},
		{"ip", "link", "set", "dev", linkName, "up"},
	}
	for _, peer := range canonicalPeers {
		setup = append(setup, []string{"bridge", "fdb", "replace", "00:00:00:00:00:00", "dev", linkName, "dst", peer.String()})
	}
	return FabricTransportPlan{
		Spec:     validated,
		LinkName: linkName,
		VNI:      vni,
		Setup:    setup,
		Teardown: [][]string{{"ip", "link", "del", "dev", linkName}},
	}, nil
}

// FabricTransportLinkName returns the stable Linux link name for an
// account/network pair. It is separate from BridgeName so transport teardown
// can never accidentally remove a tenant bridge.
func FabricTransportLinkName(accountID, networkID string) string {
	sum := sha256.Sum256([]byte("transport\x00" + strings.TrimSpace(accountID) + "\x00" + strings.TrimSpace(networkID)))
	return fabricTransportPrefix + hex.EncodeToString(sum[:])[:fabricTransportHexLen]
}

func fabricTransportVNI(accountID, networkID string) uint32 {
	sum := sha256.Sum256([]byte("transport-vni\x00" + strings.TrimSpace(accountID) + "\x00" + strings.TrimSpace(networkID)))
	value := binary.BigEndian.Uint32(sum[:4]) % maxVXLANVNI
	return value + 1
}

func isInterfaceNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':'
}
