package gatewayconfirmation

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
)

type DrainRepairStore interface {
	Store
	ListRuntimeUpgradeGatewayDrainRepairApps(context.Context, string) ([]string, error)
	PruneExpiredRuntimeUpgradeGatewayDrains(context.Context) error
}

// DrainRepair preserves bounded cursor polling while retaining every locally
// held fence, including rollback and cutovers older than the recent SQL scan.
type DrainRepair struct {
	Store   DrainRepairStore
	Tracker *activity.Tracker
}

func (r DrainRepair) ListRuntimeUpgradeGatewayRepairApps(ctx context.Context, after string) ([]string, error) {
	rows, err := r.Store.ListRuntimeUpgradeGatewayDrainRepairApps(ctx, after)
	if err != nil {
		return nil, err
	}
	for _, id := range r.Tracker.FenceApps() {
		if id > after {
			rows = append(rows, id)
		}
	}
	slices.Sort(rows)
	rows = slices.Compact(rows)
	if len(rows) > api.RuntimeUpgradeGatewayRepairBatch {
		rows = rows[:api.RuntimeUpgradeGatewayRepairBatch]
	}
	return rows, nil
}

func (r DrainRepair) PruneExpiredRuntimeUpgradeGatewayReceipts(ctx context.Context) error {
	if err := r.Store.PruneExpiredRuntimeUpgradeGatewayReceipts(ctx); err != nil {
		return err
	}
	return r.Store.PruneExpiredRuntimeUpgradeGatewayDrains(ctx)
}
