package state

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PrivateNetwork is a Gregale-owned private address space. It is deliberately
// provider-neutral: the row describes the tenant network contract, while the
// vmmd node-fabric operation realizes the account-scoped host bridge and its
// node-local workload side-links. Cross-node transport remains a later layer.
type PrivateNetwork struct {
	ID            string
	AccountID     string
	Name          string
	Region        string
	CIDR          netip.Prefix
	AllowedCIDRs  []netip.Prefix
	FirewallRules []api.PrivateNetworkFirewallRule
	Status        string
	StatusDetail  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PrivateNetworkAddress is a stable address reservation for a network member.
// OwnerType/OwnerID identify the workload or service that owns the address;
// they are not customer-controlled routing destinations.
type PrivateNetworkAddress struct {
	ID        string
	AccountID string
	NetworkID string
	OwnerType string
	OwnerID   string
	Address   netip.Addr
	CreatedAt time.Time
}

// PrivateNetworkStore is an optional extension to Store so older test doubles
// can continue serving the app attachment API. Both production and in-memory
// stores implement it for the Gregale-owned fabric surface.
type PrivateNetworkStore interface {
	CreatePrivateNetwork(context.Context, PrivateNetwork) (PrivateNetwork, error)
	GetPrivateNetwork(context.Context, string, string) (PrivateNetwork, error)
	ListPrivateNetworks(context.Context, string) ([]PrivateNetwork, error)
	DeletePrivateNetwork(context.Context, string, string) error
	AllocatePrivateNetworkAddress(context.Context, string, string, string, string) (PrivateNetworkAddress, error)
	ReleasePrivateNetworkAddress(context.Context, string, string, string, string) error
}

// PrivateNetworkPolicyStore is the additive extension used by the reusable
// network firewall endpoint. Keeping it separate lets older Store adapters
// continue serving the original network and attachment surfaces.
type PrivateNetworkPolicyStore interface {
	PrivateNetworkStore
	UpdatePrivateNetworkPolicy(context.Context, string, string, []netip.Prefix) (PrivateNetwork, error)
}

// PrivateNetworkFirewallPolicyStore extends the CIDR policy endpoint with
// protocol/port rules while keeping the older policy interface compatible.
type PrivateNetworkFirewallPolicyStore interface {
	PrivateNetworkPolicyStore
	UpdatePrivateNetworkFirewallPolicy(context.Context, string, string, []netip.Prefix, []api.PrivateNetworkFirewallRule) (PrivateNetwork, error)
}

var _ PrivateNetworkStore = (*MemStore)(nil)
var _ PrivateNetworkStore = (*PgStore)(nil)
var _ PrivateNetworkPolicyStore = (*MemStore)(nil)
var _ PrivateNetworkPolicyStore = (*PgStore)(nil)
var _ PrivateNetworkFirewallPolicyStore = (*MemStore)(nil)
var _ PrivateNetworkFirewallPolicyStore = (*PgStore)(nil)

func validatePrivateNetwork(in PrivateNetwork) (PrivateNetwork, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.Name = strings.TrimSpace(in.Name)
	in.Region = strings.TrimSpace(in.Region)
	if in.AccountID == "" || in.Name == "" || in.Region == "" {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.Name); err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.Region); err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	if in.ID != "" {
		if err := api.ValidatePrivateNetworkIdentifier(in.ID); err != nil {
			return PrivateNetwork{}, ErrInvalidArgument
		}
	}
	if !in.CIDR.IsValid() {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	prefix, err := api.ValidatePrivateNetworkCIDR(in.CIDR.String())
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	in.CIDR = prefix
	policyRaw := make([]string, 0, len(in.AllowedCIDRs))
	for _, policy := range in.AllowedCIDRs {
		policyRaw = append(policyRaw, policy.String())
	}
	policy, err := api.ValidatePrivateNetworkPolicyCIDRs(policyRaw, []netip.Prefix{in.CIDR})
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	in.AllowedCIDRs = policy
	rules, err := api.ValidatePrivateNetworkFirewallRules(in.FirewallRules, in.CIDR)
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	in.FirewallRules = clonePrivateNetworkFirewallRules(rules)
	if in.Status == "" {
		in.Status = api.PrivateNetworkStatusReady
	}
	if in.Status != api.PrivateNetworkStatusReady && in.Status != api.PrivateNetworkStatusError {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	return in, nil
}

func clonePrivateNetwork(in PrivateNetwork) PrivateNetwork {
	in.AllowedCIDRs = append([]netip.Prefix(nil), in.AllowedCIDRs...)
	in.FirewallRules = clonePrivateNetworkFirewallRules(in.FirewallRules)
	return in
}

func clonePrivateNetworkFirewallRules(in []api.PrivateNetworkFirewallRule) []api.PrivateNetworkFirewallRule {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.PrivateNetworkFirewallRule, len(in))
	for i, rule := range in {
		out[i] = rule
		out[i].CIDRs = append([]string(nil), rule.CIDRs...)
		out[i].Ports = append([]string(nil), rule.Ports...)
	}
	return out
}

func clonePrivateNetworkAddress(in PrivateNetworkAddress) PrivateNetworkAddress { return in }

func privateNetworkAddressKey(networkID, ownerType, ownerID string) string {
	return networkID + "\x00" + ownerType + "\x00" + ownerID
}

func firstPrivateNetworkAddress(prefix netip.Prefix) (uint32, uint32, bool) {
	if !prefix.IsValid() || !prefix.Addr().Is4() {
		return 0, 0, false
	}
	raw := prefix.Addr().As4()
	base := binary.BigEndian.Uint32(raw[:])
	size := uint32(1) << uint(32-prefix.Bits())
	if size < 4 { // network, gateway, one member, broadcast
		return 0, 0, false
	}
	return base + 2, base + size - 1, true
}

func privateNetworkAddressFromUint32(value uint32) netip.Addr {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], value)
	return netip.AddrFrom4(raw)
}

func allocatePrivateNetworkAddress(prefix netip.Prefix, used map[netip.Addr]struct{}) (netip.Addr, bool) {
	first, last, ok := firstPrivateNetworkAddress(prefix)
	if !ok {
		return netip.Addr{}, false
	}
	for value := first; value < last; value++ {
		candidate := privateNetworkAddressFromUint32(value)
		if _, exists := used[candidate]; exists {
			continue
		}
		return candidate, true
	}
	return netip.Addr{}, false
}

func validPrivateNetworkOwner(ownerType, ownerID string) bool {
	return strings.TrimSpace(ownerType) != "" && strings.TrimSpace(ownerID) != ""
}

func privateNetworkSort(networks []PrivateNetwork) {
	sort.Slice(networks, func(i, j int) bool {
		if networks[i].Name == networks[j].Name {
			return networks[i].ID < networks[j].ID
		}
		return networks[i].Name < networks[j].Name
	})
}

// CreatePrivateNetwork persists a network definition and rejects overlapping
// address spaces within an account and region. The in-memory implementation
// mirrors the Postgres transaction's conflict semantics.
func (m *MemStore) CreatePrivateNetwork(ctx context.Context, network PrivateNetwork) (PrivateNetwork, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetwork{}, err
	}
	var err error
	network, err = validatePrivateNetwork(network)
	if err != nil {
		return PrivateNetwork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.privateNetworks == nil {
		m.privateNetworks = map[string]PrivateNetwork{}
	}
	for _, existing := range m.privateNetworks {
		if existing.AccountID != network.AccountID {
			continue
		}
		if existing.Name == network.Name {
			return PrivateNetwork{}, fmt.Errorf("%w: private_network_account_name_key", ErrConflict)
		}
		if existing.Region == network.Region && (existing.CIDR.Contains(network.CIDR.Addr()) || network.CIDR.Contains(existing.CIDR.Addr())) {
			return PrivateNetwork{}, fmt.Errorf("%w: private_network_account_region_cidr_exclusion", ErrConflict)
		}
	}
	if network.ID == "" {
		network.ID = "net-" + uuid.NewString()
	}
	if network.CreatedAt.IsZero() {
		network.CreatedAt = time.Now().UTC()
	}
	network.UpdatedAt = time.Now().UTC()
	m.privateNetworks[network.ID] = network
	return clonePrivateNetwork(network), nil
}

func (m *MemStore) GetPrivateNetwork(ctx context.Context, accountID, id string) (PrivateNetwork, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetwork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network, ok := m.privateNetworks[id]
	if !ok || network.AccountID != accountID {
		return PrivateNetwork{}, ErrNotFound
	}
	return clonePrivateNetwork(network), nil
}

func (m *MemStore) ListPrivateNetworks(ctx context.Context, accountID string) ([]PrivateNetwork, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PrivateNetwork, 0)
	for _, network := range m.privateNetworks {
		if network.AccountID == accountID {
			out = append(out, clonePrivateNetwork(network))
		}
	}
	privateNetworkSort(out)
	return out, nil
}

func (m *MemStore) DeletePrivateNetwork(ctx context.Context, accountID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network, ok := m.privateNetworks[id]
	if !ok || network.AccountID != accountID {
		return ErrNotFound
	}
	for _, attachment := range m.privateNetworkAttachments {
		if attachment.AccountID == accountID && attachment.NetworkID == id {
			return ErrConflict
		}
	}
	delete(m.privateNetworks, id)
	for addressID, address := range m.privateNetworkAddresses {
		if address.NetworkID == id && address.AccountID == accountID {
			delete(m.privateNetworkAddresses, addressID)
		}
	}
	return nil
}

func (m *MemStore) UpdatePrivateNetworkPolicy(ctx context.Context, accountID, id string, allowedCIDRs []netip.Prefix) (PrivateNetwork, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetwork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network, ok := m.privateNetworks[id]
	if !ok || network.AccountID != accountID {
		return PrivateNetwork{}, ErrNotFound
	}
	raw := make([]string, 0, len(allowedCIDRs))
	for _, prefix := range allowedCIDRs {
		raw = append(raw, prefix.String())
	}
	policy, err := api.ValidatePrivateNetworkPolicyCIDRs(raw, []netip.Prefix{network.CIDR})
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	network.AllowedCIDRs = policy
	network.UpdatedAt = time.Now().UTC()
	m.privateNetworks[id] = network
	return clonePrivateNetwork(network), nil
}

func (m *MemStore) UpdatePrivateNetworkFirewallPolicy(ctx context.Context, accountID, id string, allowedCIDRs []netip.Prefix, rules []api.PrivateNetworkFirewallRule) (PrivateNetwork, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetwork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network, ok := m.privateNetworks[id]
	if !ok || network.AccountID != accountID {
		return PrivateNetwork{}, ErrNotFound
	}
	raw := make([]string, 0, len(allowedCIDRs))
	for _, prefix := range allowedCIDRs {
		raw = append(raw, prefix.String())
	}
	policy, err := api.ValidatePrivateNetworkPolicyCIDRs(raw, []netip.Prefix{network.CIDR})
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	validatedRules, err := api.ValidatePrivateNetworkFirewallRules(rules, network.CIDR)
	if err != nil {
		return PrivateNetwork{}, ErrInvalidArgument
	}
	network.AllowedCIDRs = policy
	network.FirewallRules = clonePrivateNetworkFirewallRules(validatedRules)
	network.UpdatedAt = time.Now().UTC()
	m.privateNetworks[id] = network
	return clonePrivateNetwork(network), nil
}

func (m *MemStore) AllocatePrivateNetworkAddress(ctx context.Context, accountID, networkID, ownerType, ownerID string) (PrivateNetworkAddress, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetworkAddress{}, err
	}
	ownerType, ownerID = strings.TrimSpace(ownerType), strings.TrimSpace(ownerID)
	if !validPrivateNetworkOwner(ownerType, ownerID) {
		return PrivateNetworkAddress{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	network, ok := m.privateNetworks[networkID]
	if !ok || network.AccountID != accountID {
		return PrivateNetworkAddress{}, ErrNotFound
	}
	if m.privateNetworkAddresses == nil {
		m.privateNetworkAddresses = map[string]PrivateNetworkAddress{}
	}
	key := privateNetworkAddressKey(networkID, ownerType, ownerID)
	for _, address := range m.privateNetworkAddresses {
		if privateNetworkAddressKey(address.NetworkID, address.OwnerType, address.OwnerID) == key {
			return clonePrivateNetworkAddress(address), nil
		}
	}
	used := map[netip.Addr]struct{}{}
	for _, address := range m.privateNetworkAddresses {
		if address.NetworkID == networkID {
			used[address.Address] = struct{}{}
		}
	}
	address, ok := allocatePrivateNetworkAddress(network.CIDR, used)
	if !ok {
		return PrivateNetworkAddress{}, ErrConflict
	}
	row := PrivateNetworkAddress{ID: uuid.NewString(), AccountID: accountID, NetworkID: networkID, OwnerType: ownerType, OwnerID: ownerID, Address: address, CreatedAt: time.Now().UTC()}
	m.privateNetworkAddresses[row.ID] = row
	return clonePrivateNetworkAddress(row), nil
}

func (m *MemStore) ReleasePrivateNetworkAddress(ctx context.Context, accountID, networkID, ownerType, ownerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ownerType, ownerID = strings.TrimSpace(ownerType), strings.TrimSpace(ownerID)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, address := range m.privateNetworkAddresses {
		if address.AccountID == accountID && address.NetworkID == networkID && address.OwnerType == ownerType && address.OwnerID == ownerID {
			delete(m.privateNetworkAddresses, id)
			return nil
		}
	}
	return ErrNotFound
}
