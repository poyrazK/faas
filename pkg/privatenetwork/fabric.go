package privatenetwork

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// Linux limits interface names to IFNAMSIZ-1 (15) bytes. Keep the
	// prefix readable and derive the remainder from the account/network pair
	// so account-local network IDs cannot collide on a shared compute node.
	fabricBridgePrefix = "gpn-"
	fabricBridgeHexLen = 10
	maxInterfaceName   = 15
)

// FabricSpec is the provider-neutral node-side identity of one Gregale
// private network. AccountID is part of the identity even though NetworkID is
// account-scoped: two accounts are allowed to choose the same network ID.
type FabricSpec struct {
	AccountID string
	NetworkID string
	Region    string
	CIDR      netip.Prefix
}

// NewFabricSpec validates a persisted Gregale network before it crosses the
// control-plane/node boundary.
func NewFabricSpec(network state.PrivateNetwork) (FabricSpec, error) {
	return NewFabricSpecFromValues(network.AccountID, network.ID, network.Region, network.CIDR.String())
}

// NewFabricSpecFromValues is the wire-facing constructor used by vmmd. It
// keeps validation at the node trust boundary instead of relying solely on
// apid's earlier request checks.
func NewFabricSpecFromValues(accountID, networkID, region, cidr string) (FabricSpec, error) {
	accountID = strings.TrimSpace(accountID)
	networkID = strings.TrimSpace(networkID)
	region = strings.TrimSpace(region)
	if accountID == "" {
		return FabricSpec{}, errors.New("privatenetwork: account_id is required")
	}
	if err := api.ValidatePrivateNetworkIdentifier(networkID); err != nil {
		return FabricSpec{}, fmt.Errorf("privatenetwork: network_id: %w", err)
	}
	if err := api.ValidatePrivateNetworkIdentifier(region); err != nil {
		return FabricSpec{}, fmt.Errorf("privatenetwork: region: %w", err)
	}
	prefix, err := api.ValidatePrivateNetworkCIDR(cidr)
	if err != nil {
		return FabricSpec{}, fmt.Errorf("privatenetwork: cidr: %w", err)
	}
	return FabricSpec{AccountID: accountID, NetworkID: networkID, Region: region, CIDR: prefix}, nil
}

// FabricPlan is the deterministic host-side plan for one network. The
// commands intentionally use `ip link`/`ip addr` only; an executor can probe
// for an existing bridge and treat EEXIST as converged, while `ip addr
// replace` and `ip link set` are naturally idempotent.
type FabricPlan struct {
	Spec        FabricSpec
	BridgeName  string
	Gateway     netip.Addr
	GatewayCIDR netip.Prefix
	Setup       [][]string
	Teardown    [][]string
}

// BuildFabricPlan returns the host bridge and gateway plan for a network.
// The bridge is deliberately dedicated to this account/network. vmmd layers
// workload side-link attachment and (when explicitly enabled) the transport
// link on this stable identity when a ready app wake carries an allocated
// member IP.
func BuildFabricPlan(spec FabricSpec) (FabricPlan, error) {
	validated, err := NewFabricSpecFromValues(spec.AccountID, spec.NetworkID, spec.Region, spec.CIDR.String())
	if err != nil {
		return FabricPlan{}, err
	}
	gateway, err := firstHostAddress(validated.CIDR)
	if err != nil {
		return FabricPlan{}, err
	}
	bridge := BridgeName(validated.AccountID, validated.NetworkID)
	gatewayCIDR := netip.PrefixFrom(gateway, validated.CIDR.Bits())
	return FabricPlan{
		Spec:        validated,
		BridgeName:  bridge,
		Gateway:     gateway,
		GatewayCIDR: gatewayCIDR,
		Setup: [][]string{
			{"ip", "link", "add", "dev", bridge, "type", "bridge"},
			{"ip", "addr", "replace", gatewayCIDR.String(), "dev", bridge},
			{"ip", "link", "set", "dev", bridge, "up"},
		},
		Teardown: [][]string{{"ip", "link", "del", "dev", bridge}},
	}, nil
}

// BridgeName returns the stable Linux bridge name for an account/network
// pair. It is safe to use directly as an interface name.
func BridgeName(accountID, networkID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(accountID) + "\x00" + strings.TrimSpace(networkID)))
	return fabricBridgePrefix + hex.EncodeToString(sum[:])[:fabricBridgeHexLen]
}

func firstHostAddress(prefix netip.Prefix) (netip.Addr, error) {
	if !prefix.IsValid() || !prefix.Addr().Is4() {
		return netip.Addr{}, errors.New("privatenetwork: fabric requires an IPv4 CIDR")
	}
	rawBase := prefix.Masked().Addr().As4()
	base := binary.BigEndian.Uint32(rawBase[:])
	if base == ^uint32(0) {
		return netip.Addr{}, errors.New("privatenetwork: CIDR has no gateway address")
	}
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], base+1)
	return netip.AddrFrom4(raw), nil
}

func init() {
	if len(fabricBridgePrefix)+fabricBridgeHexLen > maxInterfaceName {
		panic("privatenetwork: bridge name exceeds Linux interface limit")
	}
}
