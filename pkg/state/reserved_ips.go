package state

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

// ReservedIP is the provider-neutral control-plane lease for one public
// address. A connector owns the physical routing step; the store owns
// account-scoped ownership and the assignment state machine.
type ReservedIP struct {
	ID           string
	AccountID    string
	Region       string
	Address      netip.Addr
	Status       networkip.Status
	AppID        string
	NodeID       string
	Generation   int64
	StatusDetail string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ReservedIPStore is an optional Store extension. Keeping it separate from
// Store lets callers adopt reserved-IP leases without breaking older test
// doubles or unrelated state implementations.
type ReservedIPStore interface {
	UpsertReservedIP(context.Context, ReservedIP) (ReservedIP, error)
	GetReservedIP(context.Context, string, string) (ReservedIP, error)
	ListReservedIPs(context.Context, string, string) ([]ReservedIP, error)
	AssignReservedIP(context.Context, string, string, string, string) (ReservedIP, error)
	ReleaseReservedIP(context.Context, string, string, string) error
	UpdateReservedIPStatus(context.Context, string, string, networkip.Status, string, string) (ReservedIP, error)
}

var _ ReservedIPStore = (*MemStore)(nil)
var _ ReservedIPStore = (*PgStore)(nil)

func validateReservedIP(in ReservedIP) (ReservedIP, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.Region = strings.TrimSpace(in.Region)
	in.AppID = strings.TrimSpace(in.AppID)
	in.NodeID = strings.TrimSpace(in.NodeID)
	in.StatusDetail = strings.TrimSpace(in.StatusDetail)
	if in.AccountID == "" || in.Region == "" || !in.Address.IsValid() {
		return ReservedIP{}, ErrInvalidArgument
	}
	if err := api.ValidatePrivateNetworkIdentifier(in.Region); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	if err := networkip.ValidateAddress(in.Address); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	if in.ID != "" {
		if _, err := uuid.Parse(in.ID); err != nil {
			return ReservedIP{}, ErrInvalidArgument
		}
	}
	if in.AppID != "" {
		if _, err := uuid.Parse(in.AppID); err != nil {
			return ReservedIP{}, ErrInvalidArgument
		}
	}
	if in.Status == "" {
		in.Status = networkip.StatusAvailable
	}
	switch in.Status {
	case networkip.StatusAvailable, networkip.StatusPending, networkip.StatusAssigned, networkip.StatusError:
	default:
		return ReservedIP{}, ErrInvalidArgument
	}
	if in.Generation < 0 {
		return ReservedIP{}, ErrInvalidArgument
	}
	if in.Status == networkip.StatusAssigned && in.AppID == "" {
		return ReservedIP{}, ErrInvalidArgument
	}
	return in, nil
}

func cloneReservedIP(in ReservedIP) ReservedIP { return in }

func reservedIPSort(leases []ReservedIP) {
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].Region != leases[j].Region {
			return leases[i].Region < leases[j].Region
		}
		if leases[i].Address != leases[j].Address {
			return leases[i].Address.String() < leases[j].Address.String()
		}
		return leases[i].ID < leases[j].ID
	})
}

func (m *MemStore) UpsertReservedIP(ctx context.Context, lease ReservedIP) (ReservedIP, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIP{}, err
	}
	var err error
	lease, err = validateReservedIP(lease)
	if err != nil {
		return ReservedIP{}, err
	}
	if lease.ID == "" {
		lease.ID = uuid.NewString()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reservedIPLeases == nil {
		m.reservedIPLeases = map[string]ReservedIP{}
	}
	if existing, ok := m.reservedIPLeases[lease.ID]; ok {
		if existing.AccountID != lease.AccountID {
			return ReservedIP{}, ErrConflict
		}
		if existing.AppID != "" && lease.AppID != "" && existing.AppID != lease.AppID {
			return ReservedIP{}, ErrConflict
		}
		if existing.AppID != "" && lease.Status == networkip.StatusAvailable {
			lease.Status = existing.Status
		}
		lease.CreatedAt = existing.CreatedAt
		if lease.AppID == "" {
			lease.AppID = existing.AppID
		}
		if lease.NodeID == "" {
			lease.NodeID = existing.NodeID
		}
		if lease.Generation < existing.Generation {
			lease.Generation = existing.Generation
		}
	}
	for id, existing := range m.reservedIPLeases {
		if id != lease.ID && existing.Address == lease.Address {
			return ReservedIP{}, fmt.Errorf("%w: reserved_ip_leases_address_key", ErrConflict)
		}
		if lease.AppID != "" && id != lease.ID && existing.AppID == lease.AppID {
			return ReservedIP{}, fmt.Errorf("%w: reserved_ip_leases_app_key", ErrConflict)
		}
	}
	if lease.CreatedAt.IsZero() {
		lease.CreatedAt = time.Now().UTC()
	}
	lease.UpdatedAt = time.Now().UTC()
	m.reservedIPLeases[lease.ID] = lease
	return cloneReservedIP(lease), nil
}

func (m *MemStore) GetReservedIP(ctx context.Context, accountID, id string) (ReservedIP, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIP{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.reservedIPLeases[strings.TrimSpace(id)]
	if !ok || lease.AccountID != strings.TrimSpace(accountID) {
		return ReservedIP{}, ErrNotFound
	}
	return cloneReservedIP(lease), nil
}

func (m *MemStore) ListReservedIPs(ctx context.Context, accountID, region string) ([]ReservedIP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accountID, region = strings.TrimSpace(accountID), strings.TrimSpace(region)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ReservedIP, 0)
	for _, lease := range m.reservedIPLeases {
		if lease.AccountID == accountID && (region == "" || lease.Region == region) {
			out = append(out, cloneReservedIP(lease))
		}
	}
	reservedIPSort(out)
	return out, nil
}

func (m *MemStore) AssignReservedIP(ctx context.Context, accountID, ipID, appID, nodeID string) (ReservedIP, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIP{}, err
	}
	accountID, ipID, appID, nodeID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(appID), strings.TrimSpace(nodeID)
	if accountID == "" || ipID == "" || appID == "" {
		return ReservedIP{}, ErrInvalidArgument
	}
	if _, err := uuid.Parse(appID); err != nil {
		return ReservedIP{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.reservedIPLeases[ipID]
	if !ok || lease.AccountID != accountID {
		return ReservedIP{}, ErrNotFound
	}
	if lease.AppID != "" && lease.AppID != appID {
		return ReservedIP{}, ErrConflict
	}
	for id, existing := range m.reservedIPLeases {
		if id != ipID && existing.AppID == appID {
			return ReservedIP{}, fmt.Errorf("%w: reserved_ip_leases_app_key", ErrConflict)
		}
	}
	if lease.Status == networkip.StatusAssigned && lease.AppID == appID {
		if nodeID != "" && lease.NodeID != nodeID {
			lease.NodeID = nodeID
			lease.Generation++
			lease.UpdatedAt = time.Now().UTC()
			m.reservedIPLeases[ipID] = lease
		}
		return cloneReservedIP(lease), nil
	}
	if err := networkip.ValidateTransition(lease.Status, networkip.StatusPending); err != nil {
		return ReservedIP{}, ErrConflict
	}
	lease.Status, lease.AppID, lease.NodeID = networkip.StatusPending, appID, nodeID
	lease.StatusDetail = "assignment pending"
	lease.Generation++
	lease.UpdatedAt = time.Now().UTC()
	m.reservedIPLeases[ipID] = lease
	return cloneReservedIP(lease), nil
}

func (m *MemStore) ReleaseReservedIP(ctx context.Context, accountID, ipID, appID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	accountID, ipID, appID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(appID)
	if accountID == "" || ipID == "" || appID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.reservedIPLeases[ipID]
	if !ok || lease.AccountID != accountID {
		return ErrNotFound
	}
	if lease.AppID != appID {
		return ErrConflict
	}
	if err := networkip.ValidateTransition(lease.Status, networkip.StatusAvailable); err != nil {
		return ErrConflict
	}
	lease.Status, lease.AppID, lease.NodeID, lease.StatusDetail = networkip.StatusAvailable, "", "", ""
	lease.Generation++
	lease.UpdatedAt = time.Now().UTC()
	m.reservedIPLeases[ipID] = lease
	return nil
}

func (m *MemStore) UpdateReservedIPStatus(ctx context.Context, accountID, ipID string, status networkip.Status, detail, nodeID string) (ReservedIP, error) {
	if err := ctx.Err(); err != nil {
		return ReservedIP{}, err
	}
	accountID, ipID, detail, nodeID = strings.TrimSpace(accountID), strings.TrimSpace(ipID), strings.TrimSpace(detail), strings.TrimSpace(nodeID)
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.reservedIPLeases[ipID]
	if !ok || lease.AccountID != accountID {
		return ReservedIP{}, ErrNotFound
	}
	if err := networkip.ValidateTransition(lease.Status, status); err != nil {
		return ReservedIP{}, ErrConflict
	}
	if status == networkip.StatusAssigned && lease.AppID == "" {
		return ReservedIP{}, ErrInvalidArgument
	}
	if status == networkip.StatusAvailable {
		lease.AppID, lease.NodeID = "", ""
	} else if nodeID != "" {
		lease.NodeID = nodeID
	}
	lease.Status, lease.StatusDetail = status, detail
	lease.Generation++
	lease.UpdatedAt = time.Now().UTC()
	m.reservedIPLeases[ipID] = lease
	return cloneReservedIP(lease), nil
}
