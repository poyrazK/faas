package state

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppByServiceAddressIndex implements Store (ADR-576). Allocation itself
// lives in the apps insert/update triggers (migration
// 20261004141639518_app_service_address_index.sql), so no Go create path
// can forget it.
func (s *PgStore) AppByServiceAddressIndex(ctx context.Context, accountID string, index int) (App, error) {
	if !validServiceAddressLookup(accountID, index) {
		return App{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+appsSelectColumns+` from apps
		  where account_id = $1 and service_address_index = $2 and status <> 'deleted'`,
		accountID, index)
	return scanApp(row)
}

// AppByServiceAddressIndex implements Store (ADR-576).
func (m *MemStore) AppByServiceAddressIndex(_ context.Context, accountID string, index int) (App, error) {
	if !validServiceAddressLookup(accountID, index) {
		return App{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, app := range m.apps {
		if app.AccountID == accountID && m.serviceAddressIndex[id] == index && app.Status != AppDeleted {
			return app, nil
		}
	}
	return App{}, ErrNotFound
}

// AppServiceAddressIndex implements Store (ADR-576).
func (s *PgStore) AppServiceAddressIndex(ctx context.Context, appID string) (int, error) {
	if uuid.Validate(appID) != nil {
		return 0, ErrNotFound
	}
	var index int
	if err := s.pool.QueryRow(ctx,
		`select coalesce(service_address_index, 0) from apps where id = $1`, appID).Scan(&index); err != nil {
		return 0, mapErr(err)
	}
	return index, nil
}

// AppServiceAddressIndex implements Store (ADR-576).
func (m *MemStore) AppServiceAddressIndex(_ context.Context, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return 0, ErrNotFound
	}
	return m.serviceAddressIndex[appID], nil
}

func validServiceAddressLookup(accountID string, index int) bool {
	return accountID != "" && index >= api.ServiceAddressIndexMin && index <= api.ServiceAddressIndexMax
}

// ensureServiceAddressIndexLocked mirrors the PgStore trigger
// assign_app_service_address_index for an app being inserted or restored:
// an app that already holds an index keeps it. Caller holds m.mu.
func (m *MemStore) ensureServiceAddressIndexLocked(app *App) {
	if app.ID == "" || app.Status == AppDeleted || app.AccountID == "" || m.serviceAddressIndex[app.ID] != 0 {
		return
	}
	if index := m.allocateServiceAddressIndexLocked(app.AccountID); index != 0 {
		m.serviceAddressIndex[app.ID] = index
	}
}

// allocateServiceAddressIndexLocked mirrors allocate_app_service_address_index:
// a cursor that never moves backwards, then, once the account has used the
// whole range, any index that is free or held only by a tombstone past the
// reuse quarantine. 0 means the range is exhausted.
func (m *MemStore) allocateServiceAddressIndexLocked(accountID string) int {
	if last := m.serviceAddressCursors[accountID]; last < api.ServiceAddressIndexMax {
		m.serviceAddressCursors[accountID] = last + 1
		return last + 1
	}
	reclaimBefore := time.Now().Add(-api.ServiceAddressReuseQuarantine)
	blocked := make(map[int]struct{})
	holders := make(map[int]string)
	for id, app := range m.apps {
		index := m.serviceAddressIndex[id]
		if app.AccountID != accountID || index == 0 {
			continue
		}
		holders[index] = id
		reclaimable := app.Status == AppDeleted && (app.DeletedAt == nil || app.DeletedAt.Before(reclaimBefore))
		if !reclaimable {
			blocked[index] = struct{}{}
		}
	}
	for index := api.ServiceAddressIndexMin; index <= api.ServiceAddressIndexMax; index++ {
		if _, taken := blocked[index]; taken {
			continue
		}
		if holderID, held := holders[index]; held {
			delete(m.serviceAddressIndex, holderID)
		}
		return index
	}
	return 0
}

// SetComputeNodeServiceAddressReady implements Store (ADR-576).
func (s *PgStore) SetComputeNodeServiceAddressReady(ctx context.Context, nodeID string, ready bool) (*time.Time, error) {
	if uuid.Validate(nodeID) != nil {
		return nil, ErrNotFound
	}
	var at *time.Time
	err := s.pool.QueryRow(ctx, `
		update compute_nodes
		   set service_address_ready_at = case when $2 then coalesce(service_address_ready_at, now()) else null end
		 where id = $1
		returning service_address_ready_at`, nodeID, ready).Scan(&at)
	if err != nil {
		return nil, mapErr(err)
	}
	return at, nil
}

// ComputeNodeServiceAddressReadyAt implements Store (ADR-576).
func (s *PgStore) ComputeNodeServiceAddressReadyAt(ctx context.Context, nodeID string) (*time.Time, error) {
	if uuid.Validate(nodeID) != nil {
		return nil, ErrNotFound
	}
	var at *time.Time
	if err := s.pool.QueryRow(ctx,
		`select service_address_ready_at from compute_nodes where id = $1`, nodeID).Scan(&at); err != nil {
		return nil, mapErr(err)
	}
	return at, nil
}

// SetComputeNodeServiceAddressReady implements Store (ADR-576).
func (m *MemStore) SetComputeNodeServiceAddressReady(_ context.Context, nodeID string, ready bool) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return nil, ErrNotFound
	}
	if !ready {
		delete(m.serviceAddressReadyAt, nodeID)
		return nil, nil
	}
	at, ok := m.serviceAddressReadyAt[nodeID]
	if !ok {
		at = time.Now().UTC()
		m.serviceAddressReadyAt[nodeID] = at
	}
	return &at, nil
}

// ComputeNodeServiceAddressReadyAt implements Store (ADR-576).
func (m *MemStore) ComputeNodeServiceAddressReadyAt(_ context.Context, nodeID string) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return nil, ErrNotFound
	}
	at, ok := m.serviceAddressReadyAt[nodeID]
	if !ok {
		return nil, nil
	}
	return &at, nil
}

// ServiceAddressCaller is what service DNS needs about the live instance
// behind a tenant source address (ADR-576). StartedAt is the instance row's
// creation time and NodeReadyAt the node's readiness stamp; both come from
// the database clock, so they compare without skew.
type ServiceAddressCaller struct {
	AppID       string
	AccountID   string
	StartedAt   time.Time
	NodeReadyAt *time.Time
}

// ServiceAddressCapable reports whether the caller's namespace was created
// after its node began admitting service addresses.
func (c ServiceAddressCaller) ServiceAddressCapable() bool {
	return c.NodeReadyAt != nil && !c.StartedAt.Before(*c.NodeReadyAt)
}

// ServiceAddressCallerByHostIP implements Store (ADR-576). The earliest
// instance on the address wins, so the readiness gate stays conservative.
func (s *PgStore) ServiceAddressCallerByHostIP(ctx context.Context, nodeName, hostIP string) (ServiceAddressCaller, error) {
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return ServiceAddressCaller{}, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		select i.app_id::text, a.account_id::text, i.started_at, n.service_address_ready_at
		  from instances i
		  join apps a on a.id = i.app_id
		  join compute_nodes n on n.id = i.node_id
		 where i.host_ip = $1::inet
		   and i.state in ('running', 'draining')
		   and ($2 = '' or n.name = $2)
		 order by i.started_at, i.id
		 limit 2`, address.String(), nodeName)
	if err != nil {
		return ServiceAddressCaller{}, fmt.Errorf("state: service address caller lookup: %w", err)
	}
	defer rows.Close()
	var found []ServiceAddressCaller
	for rows.Next() {
		var caller ServiceAddressCaller
		if err := rows.Scan(&caller.AppID, &caller.AccountID, &caller.StartedAt, &caller.NodeReadyAt); err != nil {
			return ServiceAddressCaller{}, fmt.Errorf("state: scan service address caller: %w", err)
		}
		found = append(found, caller)
	}
	if err := rows.Err(); err != nil {
		return ServiceAddressCaller{}, fmt.Errorf("state: service address caller rows: %w", err)
	}
	return singleServiceAddressCaller(found)
}

// ServiceAddressCallerByHostIP implements Store (ADR-576).
func (m *MemStore) ServiceAddressCallerByHostIP(_ context.Context, nodeName, hostIP string) (ServiceAddressCaller, error) {
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return ServiceAddressCaller{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var found []ServiceAddressCaller
	for _, instance := range m.instances {
		if instance.State != string(StateRunning) && instance.State != string(StateDraining) {
			continue
		}
		if host, err := netip.ParseAddr(instance.HostIP); err != nil || host != address {
			continue
		}
		node, ok := m.computeNodes[instance.NodeID]
		if !ok || (nodeName != "" && node.Name != nodeName) {
			continue
		}
		app, ok := m.apps[instance.AppID]
		if !ok {
			continue
		}
		caller := ServiceAddressCaller{AppID: app.ID, AccountID: app.AccountID, StartedAt: instance.StartedAt}
		if at, ok := m.serviceAddressReadyAt[node.ID]; ok {
			caller.NodeReadyAt = &at
		}
		found = append(found, caller)
	}
	slices.SortFunc(found, func(a, b ServiceAddressCaller) int { return a.StartedAt.Compare(b.StartedAt) })
	return singleServiceAddressCaller(found)
}

func singleServiceAddressCaller(found []ServiceAddressCaller) (ServiceAddressCaller, error) {
	if len(found) == 0 {
		return ServiceAddressCaller{}, ErrNotFound
	}
	for _, other := range found[1:] {
		if other.AppID != found[0].AppID {
			return ServiceAddressCaller{}, ErrConflict
		}
	}
	return found[0], nil
}
