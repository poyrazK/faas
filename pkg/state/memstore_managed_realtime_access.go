package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func copyRealtimeAccess(row ManagedRealtimeAccess) ManagedRealtimeAccess {
	row.DeniedChannels = append([]string{}, row.DeniedChannels...)
	row.SessionsRevokedBefore = cloneManagedRealtimeTime(row.SessionsRevokedBefore)
	return row
}
func (m *MemStore) ReadManagedRealtimeAccess(_ context.Context, ep, principal string) (ManagedRealtimeAccess, error) {
	if api.ValidateRealtimePrincipal(principal) != nil {
		return ManagedRealtimeAccess{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return copyRealtimeAccess(m.managedRealtimeAccess[ep][principal]), nil
}
func (m *MemStore) ChangeManagedRealtimeAccess(_ context.Context, ep, principal, action, channel string) (ManagedRealtimeAccess, error) {
	if err := validateRealtimeAccess(principal, action, channel); err != nil {
		return ManagedRealtimeAccess{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return ManagedRealtimeAccess{}, ErrNotFound
	}
	rows := m.managedRealtimeAccess[ep]
	if rows == nil {
		rows = map[string]ManagedRealtimeAccess{}
	}
	row := rows[principal]
	if row.Revision == 0 && len(rows) >= api.RealtimeAccessMaxPrincipalsPerEndpoint {
		return row, &ManagedRealtimeAccessLimitError{Limit: api.RealtimeAccessMaxPrincipalsPerEndpoint, Observed: len(rows) + 1}
	}
	row, err := changeRealtimeAccess(row, action, channel, time.Now().UTC())
	if err != nil {
		return row, err
	}
	if m.managedRealtimeAccess == nil {
		m.managedRealtimeAccess = map[string]map[string]ManagedRealtimeAccess{}
	}
	rows[principal] = copyRealtimeAccess(row)
	m.managedRealtimeAccess[ep] = rows
	return row, nil
}
