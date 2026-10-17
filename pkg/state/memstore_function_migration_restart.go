package state

// adr: 1353

import (
	"context"
	"slices"
	"strings"
	"time"
)

var _ FunctionMigrationRestartStore = (*MemStore)(nil)

func (m *MemStore) ListRestartFunctionMigrationDestinations(ctx context.Context, node, after string, limit int) ([]FunctionMigrationDestination, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validRestartMigrationUUID(node) || after != "" && !validRestartMigrationUUID(after) || limit <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []FunctionMigrationDestination
	for _, d := range m.functionMigrationDestinations {
		lease, held := m.migrationLeases[d.Grant.Source.LeaseToken]
		if !held || lease.InstanceID != d.Grant.Binding.InstanceID || d.Grant.Binding.InstanceID <= after || d.Grant.Binding.NodeID != node ||
			(d.Phase != FunctionDestinationCommitted && (d.Phase != FunctionDestinationReady || !lease.LeaseExpiresAt.After(time.Now()))) {
			continue
		}
		result = append(result, d.Clone())
	}
	slices.SortFunc(result, func(a, b FunctionMigrationDestination) int {
		return strings.Compare(a.Grant.Binding.InstanceID, b.Grant.Binding.InstanceID)
	})
	return result[:min(limit, len(result))], nil
}
