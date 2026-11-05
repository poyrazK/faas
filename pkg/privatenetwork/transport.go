package privatenetwork

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
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
	maxVXLANVNI           = api.PrivateNetworkMaxTransportVNI
	vxlanUDPPort          = 4789
	// DefaultFabricMTU fits a 1280-byte encrypted overlay after the outer
	// IPv4, UDP and VXLAN headers. All nodes in a region use one common MTU.
	DefaultFabricMTU        = 1230
	FabricTransportOverhead = 50
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
	MTU              int
	// VNI is reserved durably per region. Zero retains the legacy preferred
	// value for offline plans; production vmmd supplies the allocated value.
	VNI uint32
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
	// PeerSync is safe to run on every reconciliation. Flushing the link's
	// static FDB first prevents a drained node from remaining reachable after
	// the regional peer roster changes or vmmd restarts.
	PeerSync [][]string
	Teardown [][]string
}

// FabricReadiness is the node-local observation returned after a fabric
// reconciliation. Supported is intentionally separate from Ready so a
// rolling upgrade can talk to an older vmmd without treating its empty ack
// as a dataplane failure.
type FabricReadiness struct {
	Supported             bool
	Ready                 bool
	Detail                string
	ExpectedPeerAddresses []netip.Addr
	ObservedPeerAddresses []netip.Addr
}

// ParseFabricTransportPeers extracts IPv4 VXLAN destinations from the output
// of `bridge fdb show dev <link>`. iproute2 may add flags in any order, so we
// only rely on the stable `dst <address>` pair and ignore malformed rows.
func ParseFabricTransportPeers(output []byte) []netip.Addr {
	seen := make(map[netip.Addr]struct{})
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "00:00:00:00:00:00" {
			continue
		}
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] != "dst" {
				continue
			}
			addr, err := netip.ParseAddr(fields[i+1])
			if err != nil || !addr.Is4() {
				continue
			}
			seen[addr] = struct{}{}
		}
	}
	peers := make([]netip.Addr, 0, len(seen))
	for peer := range seen {
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].String() < peers[j].String() })
	return peers
}

// FabricTransportPeerCommands reconciles the observed flood roster without
// the bulk-flush operation, which older VXLAN kernels do not implement. A
// replay of a converged roster emits no commands and never appends duplicates.
func FabricTransportPeerCommands(linkName string, expected, observed []netip.Addr) [][]string {
	missing, stale := FabricPeerDrift(expected, observed)
	commands := make([][]string, 0, len(missing)+len(stale))
	for _, peer := range stale {
		commands = append(commands, []string{"bridge", "fdb", "del", "00:00:00:00:00:00", "dev", linkName, "dst", peer.String()})
	}
	for _, peer := range missing {
		commands = append(commands, []string{"bridge", "fdb", "append", "00:00:00:00:00:00", "dev", linkName, "dst", peer.String()})
	}
	return commands
}

// FabricPeerDrift returns the expected peers missing from the kernel FDB and
// the unexpected peers still present after reconciliation.
func FabricPeerDrift(expected, observed []netip.Addr) (missing, stale []netip.Addr) {
	expectedSet := make(map[netip.Addr]struct{}, len(expected))
	observedSet := make(map[netip.Addr]struct{}, len(observed))
	for _, peer := range expected {
		if peer.IsValid() {
			expectedSet[peer] = struct{}{}
		}
	}
	for _, peer := range observed {
		if peer.IsValid() {
			observedSet[peer] = struct{}{}
		}
	}
	for peer := range expectedSet {
		if _, ok := observedSet[peer]; !ok {
			missing = append(missing, peer)
		}
	}
	for peer := range observedSet {
		if _, ok := expectedSet[peer]; !ok {
			stale = append(stale, peer)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].String() < missing[j].String() })
	sort.Slice(stale, func(i, j int) bool { return stale[i].String() < stale[j].String() })
	return missing, stale
}

// BuildFabricTransportPlan validates a transport spec and returns idempotent
// iproute2 commands. L2 VXLAN is intentional: all members already share the
// same private-network CIDR, so a routed L3 overlay would create overlapping
// connected routes on every node.
func BuildFabricTransportPlan(spec FabricTransportSpec) (FabricTransportPlan, error) {
	if spec.MTU == 0 {
		spec.MTU = DefaultFabricMTU
	}
	if spec.MTU < 576 || spec.MTU > 1500 {
		return FabricTransportPlan{}, errors.New("privatenetwork: transport MTU must be between 576 and 1500")
	}
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
		MTU:              spec.MTU,
	}

	linkName := FabricTransportLinkName(validatedFabric.AccountID, validatedFabric.NetworkID)
	vni := spec.VNI
	if vni == 0 {
		vni = fabricTransportVNI(validatedFabric.AccountID, validatedFabric.NetworkID)
	}
	if vni > maxVXLANVNI {
		return FabricTransportPlan{}, errors.New("privatenetwork: invalid VXLAN identifier")
	}
	validated.VNI = vni
	bridge := BridgeName(validatedFabric.AccountID, validatedFabric.NetworkID)
	setup := [][]string{
		{"ip", "link", "add", "dev", linkName, "mtu", fmt.Sprint(spec.MTU), "type", "vxlan", "id", fmt.Sprintf("%d", vni), "dev", iface, "local", local.String(), "dstport", fmt.Sprintf("%d", vxlanUDPPort), "nolearning"},
		{"ip", "link", "set", "dev", linkName, "master", bridge},
		{"ip", "link", "set", "dev", linkName, "up"},
	}
	for _, peer := range canonicalPeers {
		setup = append(setup, []string{"bridge", "fdb", "append", "00:00:00:00:00:00", "dev", linkName, "dst", peer.String()})
	}
	peerSync := [][]string{{"bridge", "fdb", "flush", "dev", linkName}}
	for _, peer := range canonicalPeers {
		peerSync = append(peerSync, []string{"bridge", "fdb", "append", "00:00:00:00:00:00", "dev", linkName, "dst", peer.String()})
	}
	return FabricTransportPlan{
		Spec:     validated,
		LinkName: linkName,
		VNI:      vni,
		Setup:    setup,
		PeerSync: peerSync,
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
	return state.PreferredPrivateNetworkVNI(accountID, networkID)
}

func isInterfaceNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':'
}
