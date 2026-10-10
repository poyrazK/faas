package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServiceWakeAheadSetting is an app's ADR-946 opt-in. A zero UpdatedAt means
// the app never changed it from the default (off).
type ServiceWakeAheadSetting struct {
	Enabled   bool
	UpdatedAt time.Time
}

// ServiceWakeAheadStore persists the wake-ahead opt-in (written by apid) and
// answers the two reads gatewayd-internal needs before a wake-ahead.
type ServiceWakeAheadStore interface {
	GetServiceWakeAhead(ctx context.Context, accountID, appID string) (ServiceWakeAheadSetting, error)
	SetServiceWakeAhead(ctx context.Context, accountID, appID string, enabled bool) (ServiceWakeAheadSetting, error)
	ServiceWakeAheadEnabled(ctx context.Context, appID string) (bool, error)
	// ServiceWakeAheadFleetResidency returns billable RAM of live instances
	// and the summed admission ceilings of active nodes, in MB.
	ServiceWakeAheadFleetResidency(ctx context.Context) (residentMB, ceilingMB int64, err error)
}

var serviceWakeAheadLiveStates = map[State]bool{StateWaking: true, StateColdBooting: true, StateRunning: true, StateDraining: true, StateWarm: true}

func (s *PgStore) GetServiceWakeAhead(ctx context.Context, accountID, appID string) (ServiceWakeAheadSetting, error) {
	row, err := (&sqlc.Queries{}).GetServiceWakeAhead(ctx, s.pool, sqlc.GetServiceWakeAheadParams{AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceWakeAheadSetting{}, nil
	}
	if err != nil {
		return ServiceWakeAheadSetting{}, err
	}
	return ServiceWakeAheadSetting{Enabled: row.Enabled, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (s *PgStore) SetServiceWakeAhead(ctx context.Context, accountID, appID string, enabled bool) (ServiceWakeAheadSetting, error) {
	row, err := (&sqlc.Queries{}).SetServiceWakeAhead(ctx, s.pool, sqlc.SetServiceWakeAheadParams{AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID), Enabled: enabled})
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceWakeAheadSetting{}, ErrNotFound
	}
	if err != nil {
		return ServiceWakeAheadSetting{}, err
	}
	return ServiceWakeAheadSetting{Enabled: row.Enabled, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (s *PgStore) ServiceWakeAheadEnabled(ctx context.Context, appID string) (bool, error) {
	return (&sqlc.Queries{}).ServiceWakeAheadEnabled(ctx, s.pool, mustPgUUID(appID))
}

func (s *PgStore) ServiceWakeAheadFleetResidency(ctx context.Context) (int64, int64, error) {
	row, err := (&sqlc.Queries{}).ServiceWakeAheadFleetResidency(ctx, s.pool, api.PerVMOverheadMB)
	return row.ResidentMb, row.CeilingMb, err
}

type memServiceWakeAhead struct {
	accountID string
	setting   ServiceWakeAheadSetting
}

func (m *MemStore) GetServiceWakeAhead(_ context.Context, accountID, appID string) (ServiceWakeAheadSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.serviceWakeAhead[appID]; ok && v.accountID == accountID {
		return v.setting, nil
	}
	return ServiceWakeAheadSetting{}, nil
}

func (m *MemStore) SetServiceWakeAhead(_ context.Context, accountID, appID string, enabled bool) (ServiceWakeAheadSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return ServiceWakeAheadSetting{}, ErrNotFound
	}
	if m.serviceWakeAhead == nil {
		m.serviceWakeAhead = map[string]memServiceWakeAhead{}
	}
	v := memServiceWakeAhead{accountID: accountID, setting: ServiceWakeAheadSetting{Enabled: enabled, UpdatedAt: time.Now().UTC()}}
	m.serviceWakeAhead[appID] = v
	return v.setting, nil
}

func (m *MemStore) ServiceWakeAheadEnabled(_ context.Context, appID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serviceWakeAhead[appID].setting.Enabled, nil
}

func (m *MemStore) ServiceWakeAheadFleetResidency(context.Context) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var resident int64
	for _, ins := range m.instances {
		if serviceWakeAheadLiveStates[State(ins.State)] {
			resident += int64(ins.RAMMB + api.PerVMOverheadMB)
		}
	}
	return resident, api.RAMAdmissionCeilingMB, nil
}
