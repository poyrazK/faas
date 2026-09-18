package state

// adr: 184 — operator-managed reserved public IP inventory and tenant claims.

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/networkip"
)

// ReservedIPInventory is an operator-owned public address. It is deliberately
// not account-scoped: a provider connector imports the address once, then a
// tenant claim creates the account-scoped ReservedIP lease that workloads use.
type ReservedIPInventory struct {
	ID           string
	Region       string
	Address      netip.Addr
	ProviderRef  string
	Status       networkip.InventoryStatus
	LeaseID      string
	StatusDetail string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ReservedIPInventoryStore is the operator-side extension used to import
// addresses and atomically turn available inventory into tenant leases.
type ReservedIPInventoryStore interface {
	UpsertReservedIPInventory(context.Context, ReservedIPInventory) (ReservedIPInventory, error)
	GetReservedIPInventory(context.Context, string) (ReservedIPInventory, error)
	ListReservedIPInventory(context.Context, string, networkip.InventoryStatus) ([]ReservedIPInventory, error)
	ClaimReservedIP(context.Context, string, string) (ReservedIP, error)
	ReleaseReservedIPClaim(context.Context, string, string) error
}

var _ ReservedIPInventoryStore = (*MemStore)(nil)
var _ ReservedIPInventoryStore = (*PgStore)(nil)

func validateReservedIPInventory(in ReservedIPInventory) (ReservedIPInventory, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.Region = strings.TrimSpace(in.Region)
	in.ProviderRef = strings.TrimSpace(in.ProviderRef)
	in.LeaseID = strings.TrimSpace(in.LeaseID)
	in.StatusDetail = strings.TrimSpace(in.StatusDetail)
	if in.Region == "" || !in.Address.IsValid() {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.Region); err != nil {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	if err := networkip.ValidateAddress(in.Address); err != nil {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	if in.ID != "" {
		if _, err := uuid.Parse(in.ID); err != nil {
			return ReservedIPInventory{}, ErrInvalidArgument
		}
	}
	if len(in.ProviderRef) > 255 || len(in.StatusDetail) > 1000 {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	if in.Status == "" {
		in.Status = networkip.InventoryAvailable
	}
	if err := networkip.ValidateInventoryStatus(in.Status); err != nil {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	if in.LeaseID != "" {
		if _, err := uuid.Parse(in.LeaseID); err != nil {
			return ReservedIPInventory{}, ErrInvalidArgument
		}
	}
	if in.Status == networkip.InventoryClaimed && in.LeaseID == "" {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	if in.Status != networkip.InventoryClaimed && in.LeaseID != "" {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	return in, nil
}

func reservedIPInventorySort(items []ReservedIPInventory) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Region != items[j].Region {
			return items[i].Region < items[j].Region
		}
		if items[i].Address != items[j].Address {
			return items[i].Address.String() < items[j].Address.String()
		}
		return items[i].ID < items[j].ID
	})
}

func (m *MemStore) UpsertReservedIPInventory(ctx context.Context, inventory ReservedIPInventory) (ReservedIPInventory, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIPInventory{}, err
	}
	var err error
	inventory, err = validateReservedIPInventory(inventory)
	if err != nil {
		return ReservedIPInventory{}, err
	}
	if inventory.ID == "" {
		inventory.ID = uuid.NewString()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reservedIPInventory == nil {
		m.reservedIPInventory = map[string]ReservedIPInventory{}
	}
	if existing, ok := m.reservedIPInventory[inventory.ID]; ok {
		if existing.Status == networkip.InventoryClaimed {
			if existing.Address != inventory.Address || existing.Region != inventory.Region ||
				(inventory.LeaseID != "" && inventory.LeaseID != existing.LeaseID) ||
				(inventory.Status != networkip.InventoryClaimed && inventory.Status != networkip.InventoryAvailable) {
				return ReservedIPInventory{}, ErrConflict
			}
			inventory.Status, inventory.LeaseID = existing.Status, existing.LeaseID
		}
		if inventory.ProviderRef == "" {
			inventory.ProviderRef = existing.ProviderRef
		}
		inventory.CreatedAt = existing.CreatedAt
	}
	for id, existing := range m.reservedIPInventory {
		if id != inventory.ID && existing.Address == inventory.Address {
			return ReservedIPInventory{}, fmt.Errorf("%w: reserved_ip_inventory_address_key", ErrConflict)
		}
		if inventory.ProviderRef != "" && id != inventory.ID && existing.ProviderRef == inventory.ProviderRef {
			return ReservedIPInventory{}, fmt.Errorf("%w: reserved_ip_inventory_provider_ref_key", ErrConflict)
		}
	}
	if inventory.CreatedAt.IsZero() {
		inventory.CreatedAt = time.Now().UTC()
	}
	inventory.UpdatedAt = time.Now().UTC()
	m.reservedIPInventory[inventory.ID] = inventory
	return inventory, nil
}

func (m *MemStore) GetReservedIPInventory(ctx context.Context, id string) (ReservedIPInventory, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIPInventory{}, err
	}
	id = strings.TrimSpace(id)
	if _, err := uuid.Parse(id); err != nil {
		return ReservedIPInventory{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inventory, ok := m.reservedIPInventory[id]
	if !ok {
		return ReservedIPInventory{}, ErrNotFound
	}
	return inventory, nil
}

func (m *MemStore) ListReservedIPInventory(ctx context.Context, region string, status networkip.InventoryStatus) ([]ReservedIPInventory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	region = strings.TrimSpace(region)
	if region != "" {
		if err := api.ValidatePrivateNetworkIdentifier(region); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	if status != "" {
		if err := networkip.ValidateInventoryStatus(status); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ReservedIPInventory, 0)
	for _, inventory := range m.reservedIPInventory {
		if (region == "" || inventory.Region == region) && (status == "" || inventory.Status == status) {
			out = append(out, inventory)
		}
	}
	reservedIPInventorySort(out)
	return out, nil
}

func (m *MemStore) ClaimReservedIP(ctx context.Context, accountID, inventoryID string) (ReservedIP, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIP{}, err
	}
	accountID, inventoryID = strings.TrimSpace(accountID), strings.TrimSpace(inventoryID)
	if _, err := uuid.Parse(accountID); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	if _, err := uuid.Parse(inventoryID); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inventory, ok := m.reservedIPInventory[inventoryID]
	if !ok {
		return ReservedIP{}, ErrNotFound
	}
	if inventory.Status == networkip.InventoryClaimed {
		lease, leaseOK := m.reservedIPLeases[inventory.LeaseID]
		if !leaseOK {
			return ReservedIP{}, fmt.Errorf("%w: inventory claim has no lease", ErrConflict)
		}
		if lease.AccountID != accountID {
			return ReservedIP{}, ErrConflict
		}
		return lease, nil
	}
	if inventory.Status == networkip.InventoryRetired {
		return ReservedIP{}, ErrConflict
	}
	for _, existing := range m.reservedIPLeases {
		if existing.Address == inventory.Address {
			return ReservedIP{}, fmt.Errorf("%w: reserved_ip_leases_address_key", ErrConflict)
		}
	}
	now := time.Now().UTC()
	lease := ReservedIP{
		ID:           uuid.NewString(),
		AccountID:    accountID,
		Region:       inventory.Region,
		Address:      inventory.Address,
		Status:       networkip.StatusAvailable,
		StatusDetail: "claimed from operator inventory",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if m.reservedIPLeases == nil {
		m.reservedIPLeases = map[string]ReservedIP{}
	}
	m.reservedIPLeases[lease.ID] = lease
	inventory.Status = networkip.InventoryClaimed
	inventory.LeaseID = lease.ID
	inventory.StatusDetail = "claimed"
	inventory.UpdatedAt = now
	m.reservedIPInventory[inventory.ID] = inventory
	return lease, nil
}

func (m *MemStore) ReleaseReservedIPClaim(ctx context.Context, accountID, ipID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	accountID, ipID = strings.TrimSpace(accountID), strings.TrimSpace(ipID)
	if _, err := uuid.Parse(accountID); err != nil || ipID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.reservedIPLeases[ipID]
	if !ok || lease.AccountID != accountID {
		return ErrNotFound
	}
	if lease.AppID != "" {
		return ErrConflict
	}
	var inventoryID string
	for id, inventory := range m.reservedIPInventory {
		if inventory.LeaseID == lease.ID {
			inventoryID = id
			inventory.Status = networkip.InventoryAvailable
			inventory.LeaseID = ""
			inventory.StatusDetail = ""
			inventory.UpdatedAt = time.Now().UTC()
			m.reservedIPInventory[id] = inventory
			break
		}
	}
	if inventoryID == "" {
		return fmt.Errorf("%w: lease is not backed by inventory", ErrConflict)
	}
	delete(m.reservedIPLeases, lease.ID)
	return nil
}
