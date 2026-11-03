package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FinancialComputeInstanceKey names an app instance whose latest compute
// lease FinancialComputeLatestInstanceLeases reads.
type FinancialComputeInstanceKey struct {
	AccountID  string
	AppID      string
	InstanceID string
}

// FinancialComputeInstanceLeasesReader reads the latest compute lease of many
// app instances in one query (ADR 20261102160622). The reaper's strict compute
// sweep read FinancialComputeInstanceLease once per resident instance on
// every tick, and every boot writes a lease, budgeted or not. The result maps
// an instance ID to the lease FinancialComputeInstanceLease returns for its
// key; an instance without one is absent. Callers fall back to
// FinancialComputeInstanceLease when the store does not implement it.
type FinancialComputeInstanceLeasesReader interface {
	FinancialComputeLatestInstanceLeases(ctx context.Context, keys []FinancialComputeInstanceKey) (map[string]financial.ComputeLease, error)
}

// FinancialComputeLatestInstanceLeases reads every key's latest lease through
// financial_compute_instance_history. A row whose account or app is not its
// key's (instance IDs are unique, so this does not happen) is read again with
// FinancialComputeInstanceLease, which filters by both.
func (s *PgStore) FinancialComputeLatestInstanceLeases(ctx context.Context, keys []FinancialComputeInstanceKey) (map[string]financial.ComputeLease, error) {
	byInstance := make(map[string]FinancialComputeInstanceKey, len(keys))
	params := sqlc.FinancialComputeLatestAppInstanceLeasesParams{}
	for _, key := range keys {
		account, accountErr := parsePgUUID(key.AccountID)
		app, appErr := parsePgUUID(key.AppID)
		if accountErr != nil || appErr != nil || key.InstanceID == "" {
			continue
		}
		if _, dup := byInstance[key.InstanceID]; dup {
			continue
		}
		byInstance[key.InstanceID] = key
		params.InstanceIds = append(params.InstanceIds, key.InstanceID)
		params.AccountIds = append(params.AccountIds, account)
		params.AppIds = append(params.AppIds, app)
	}
	out := make(map[string]financial.ComputeLease, len(byInstance))
	if len(byInstance) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().FinancialComputeLatestAppInstanceLeases(ctx, s.pool, params)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		key := byInstance[row.InstanceID]
		var l financial.ComputeLease
		if err := json.Unmarshal(row.Payload, &l); err != nil {
			return nil, err
		}
		if l.AccountID != key.AccountID || l.AppID != key.AppID {
			exact, ok, err := s.FinancialComputeInstanceLease(ctx, key.AccountID, key.AppID, key.InstanceID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			l = exact
		}
		out[row.InstanceID] = l
	}
	return out, nil
}

// FinancialComputeLatestInstanceLeases mirrors
// PgStore.FinancialComputeLatestInstanceLeases.
func (m *MemStore) FinancialComputeLatestInstanceLeases(ctx context.Context, keys []FinancialComputeInstanceKey) (map[string]financial.ComputeLease, error) {
	out := make(map[string]financial.ComputeLease, len(keys))
	for _, key := range keys {
		l, ok, err := m.FinancialComputeInstanceLease(ctx, key.AccountID, key.AppID, key.InstanceID)
		if err != nil {
			return nil, err
		}
		if ok {
			out[key.InstanceID] = l
		}
	}
	return out, nil
}
