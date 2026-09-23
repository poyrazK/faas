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

// PrivateNetworkPeering is the durable control-plane intent connecting two
// Gregale-owned network route domains. Network IDs are canonicalized so the
// same relationship cannot be created twice in reverse order.
type PrivateNetworkPeering struct {
	ID             string
	AccountID      string
	LeftNetworkID  string
	RightNetworkID string
	Region         string
	Status         string
	StatusDetail   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
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

// PrivateNetworkDurableDeletionStore is the production-only atomic deletion
// path: PostgreSQL removes the network and records fabric teardown in one
// transaction. In-memory adapters continue using PrivateNetworkStore.
type PrivateNetworkDurableDeletionStore interface {
	DeletePrivateNetworkDurably(context.Context, string, string) error
}

// PrivateNetworkAddressListStore is the additive read surface for network
// member inventory. Keeping it separate lets older Store adapters continue to
// serve network creation and app attachments while the inventory endpoint is
// rolled out.
type PrivateNetworkAddressListStore interface {
	PrivateNetworkStore
	ListPrivateNetworkAddresses(context.Context, string, string) ([]PrivateNetworkAddress, error)
}

// PrivateNetworkPeeringStore is an additive extension for the customer-facing
// peering lifecycle. Keeping it separate preserves compatibility with older
// stores and test doubles that only implement network attachments.
type PrivateNetworkPeeringStore interface {
	PrivateNetworkStore
	CreatePrivateNetworkPeering(context.Context, PrivateNetworkPeering) (PrivateNetworkPeering, error)
	ListPrivateNetworkPeerings(context.Context, string, string) ([]PrivateNetworkPeering, error)
	GetPrivateNetworkPeering(context.Context, string, string) (PrivateNetworkPeering, error)
	DeletePrivateNetworkPeering(context.Context, string, string) error
}

// PrivateNetworkPeeringReconcileStore is the durable worker surface for the
// provider-neutral peering convergence loop. API callers keep using the
// account-scoped methods above; the scheduler needs a bounded, status-filtered
// view across all accounts plus an idempotent status transition.
type PrivateNetworkPeeringReconcileStore interface {
	PrivateNetworkPeeringStore
	ListPrivateNetworkPeeringsForReconcile(context.Context, []string, int) ([]PrivateNetworkPeering, error)
	UpdatePrivateNetworkPeeringStatus(context.Context, string, string, string, string) (PrivateNetworkPeering, error)
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
var _ PrivateNetworkAddressListStore = (*MemStore)(nil)
var _ PrivateNetworkAddressListStore = (*PgStore)(nil)
var _ PrivateNetworkPeeringStore = (*MemStore)(nil)
var _ PrivateNetworkPeeringStore = (*PgStore)(nil)
var _ PrivateNetworkPeeringReconcileStore = (*MemStore)(nil)
var _ PrivateNetworkPeeringReconcileStore = (*PgStore)(nil)
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

func clonePrivateNetworkPeering(in PrivateNetworkPeering) PrivateNetworkPeering { return in }

func privateNetworkPeeringKey(accountID, left, right string) string {
	return accountID + "\x00" + left + "\x00" + right
}

func validatePrivateNetworkPeering(in PrivateNetworkPeering) (PrivateNetworkPeering, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.LeftNetworkID = strings.TrimSpace(in.LeftNetworkID)
	in.RightNetworkID = strings.TrimSpace(in.RightNetworkID)
	in.Region = strings.TrimSpace(in.Region)
	if in.AccountID == "" || in.LeftNetworkID == "" || in.RightNetworkID == "" || in.Region == "" {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if in.ID != "" {
		if err := api.ValidatePrivateNetworkIdentifier(in.ID); err != nil {
			return PrivateNetworkPeering{}, ErrInvalidArgument
		}
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.LeftNetworkID); err != nil {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.RightNetworkID); err != nil {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.Region); err != nil {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if in.LeftNetworkID == in.RightNetworkID {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if in.RightNetworkID < in.LeftNetworkID {
		in.LeftNetworkID, in.RightNetworkID = in.RightNetworkID, in.LeftNetworkID
	}
	if in.Status == "" {
		in.Status = api.PrivateNetworkPeeringStatusPending
	}
	if in.Status != api.PrivateNetworkPeeringStatusPending && in.Status != api.PrivateNetworkPeeringStatusReady && in.Status != api.PrivateNetworkPeeringStatusError {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	return in, nil
}

func privateNetworkPeeringSort(peerings []PrivateNetworkPeering) {
	sort.Slice(peerings, func(i, j int) bool {
		if peerings[i].LeftNetworkID == peerings[j].LeftNetworkID {
			return peerings[i].RightNetworkID < peerings[j].RightNetworkID
		}
		return peerings[i].LeftNetworkID < peerings[j].LeftNetworkID
	})
}

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

// PrivateNetworkAddressCapacity returns the number of allocatable member
// addresses in a Gregale-owned network. The network address, gateway (+1),
// and broadcast address are reserved by the fabric.
func PrivateNetworkAddressCapacity(prefix netip.Prefix) int {
	first, last, ok := firstPrivateNetworkAddress(prefix)
	if !ok || last <= first {
		return 0
	}
	return int(last - first)
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
	for _, peering := range m.privateNetworkPeerings {
		if peering.AccountID == accountID && (peering.LeftNetworkID == id || peering.RightNetworkID == id) {
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

// CreatePrivateNetworkPeering records a pending symmetric route intent. The
// in-memory implementation mirrors the Postgres checks so API tests exercise
// account isolation, region matching, and CIDR safety.
func (m *MemStore) CreatePrivateNetworkPeering(ctx context.Context, peering PrivateNetworkPeering) (PrivateNetworkPeering, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetworkPeering{}, err
	}
	var err error
	peering, err = validatePrivateNetworkPeering(peering)
	if err != nil {
		return PrivateNetworkPeering{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	left, leftOK := m.privateNetworks[peering.LeftNetworkID]
	right, rightOK := m.privateNetworks[peering.RightNetworkID]
	if !leftOK || !rightOK || left.AccountID != peering.AccountID || right.AccountID != peering.AccountID {
		return PrivateNetworkPeering{}, ErrNotFound
	}
	if left.Region != peering.Region || right.Region != peering.Region {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	if left.CIDR.Overlaps(right.CIDR) {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	key := privateNetworkPeeringKey(peering.AccountID, peering.LeftNetworkID, peering.RightNetworkID)
	for _, existing := range m.privateNetworkPeerings {
		if privateNetworkPeeringKey(existing.AccountID, existing.LeftNetworkID, existing.RightNetworkID) == key {
			return PrivateNetworkPeering{}, ErrConflict
		}
	}
	if peering.ID == "" {
		peering.ID = "peer-" + uuid.NewString()
	}
	if _, exists := m.privateNetworkPeerings[peering.ID]; exists {
		return PrivateNetworkPeering{}, ErrConflict
	}
	if peering.StatusDetail == "" {
		peering.StatusDetail = "network route convergence is pending"
	}
	if peering.CreatedAt.IsZero() {
		peering.CreatedAt = time.Now().UTC()
	}
	peering.UpdatedAt = time.Now().UTC()
	m.privateNetworkPeerings[peering.ID] = peering
	return clonePrivateNetworkPeering(peering), nil
}

func (m *MemStore) ListPrivateNetworkPeerings(ctx context.Context, accountID, networkID string) ([]PrivateNetworkPeering, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PrivateNetworkPeering, 0)
	for _, peering := range m.privateNetworkPeerings {
		if peering.AccountID == accountID && (networkID == "" || peering.LeftNetworkID == networkID || peering.RightNetworkID == networkID) {
			out = append(out, clonePrivateNetworkPeering(peering))
		}
	}
	privateNetworkPeeringSort(out)
	return out, nil
}

func (m *MemStore) ListPrivateNetworkPeeringsForReconcile(ctx context.Context, statuses []string, limit int) ([]PrivateNetworkPeering, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidArgument
	}
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		if status != api.PrivateNetworkPeeringStatusPending && status != api.PrivateNetworkPeeringStatusReady && status != api.PrivateNetworkPeeringStatusError {
			return nil, ErrInvalidArgument
		}
		allowed[status] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PrivateNetworkPeering, 0, len(m.privateNetworkPeerings))
	for _, peering := range m.privateNetworkPeerings {
		if len(allowed) > 0 {
			if _, ok := allowed[peering.Status]; !ok {
				continue
			}
		}
		out = append(out, clonePrivateNetworkPeering(peering))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) GetPrivateNetworkPeering(ctx context.Context, accountID, id string) (PrivateNetworkPeering, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetworkPeering{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	peering, ok := m.privateNetworkPeerings[id]
	if !ok || peering.AccountID != accountID {
		return PrivateNetworkPeering{}, ErrNotFound
	}
	return clonePrivateNetworkPeering(peering), nil
}

func (m *MemStore) UpdatePrivateNetworkPeeringStatus(ctx context.Context, accountID, id, status, detail string) (PrivateNetworkPeering, error) {
	if err := ctx.Err(); err != nil {
		return PrivateNetworkPeering{}, err
	}
	if status != api.PrivateNetworkPeeringStatusPending && status != api.PrivateNetworkPeeringStatusReady && status != api.PrivateNetworkPeeringStatusError {
		return PrivateNetworkPeering{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	peering, ok := m.privateNetworkPeerings[id]
	if !ok || peering.AccountID != accountID {
		return PrivateNetworkPeering{}, ErrNotFound
	}
	peering.Status = status
	peering.StatusDetail = detail
	peering.UpdatedAt = time.Now().UTC()
	m.privateNetworkPeerings[id] = peering
	return clonePrivateNetworkPeering(peering), nil
}

func (m *MemStore) DeletePrivateNetworkPeering(ctx context.Context, accountID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	peering, ok := m.privateNetworkPeerings[id]
	if !ok || peering.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.privateNetworkPeerings, id)
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

func (m *MemStore) ListPrivateNetworkAddresses(ctx context.Context, accountID, networkID string) ([]PrivateNetworkAddress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accountID = strings.TrimSpace(accountID)
	networkID = strings.TrimSpace(networkID)
	if accountID == "" || networkID == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PrivateNetworkAddress, 0)
	for _, address := range m.privateNetworkAddresses {
		if address.AccountID == accountID && address.NetworkID == networkID {
			out = append(out, clonePrivateNetworkAddress(address))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Address.String() == out[j].Address.String() {
			return out[i].ID < out[j].ID
		}
		return out[i].Address.String() < out[j].Address.String()
	})
	return out, nil
}
