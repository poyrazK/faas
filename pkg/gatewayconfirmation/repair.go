// Package gatewayconfirmation provides bounded private cache-repair polling.
// It creates no traffic intent, VM calls or automatic rollback.
package gatewayconfirmation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Store interface {
	ListRuntimeUpgradeGatewayRepairApps(context.Context, string) ([]string, error)
	PruneExpiredRuntimeUpgradeGatewayReceipts(context.Context) error
}

type Refresher interface {
	RefreshDeploymentWeights(context.Context, string) error
}

// Repair advances only after a whole bounded page installs and confirms. Failed
// reads, installation or publication retain the cursor for the next retry.
func Repair(ctx context.Context, store Store, refresher Refresher, cursor string) (string, error) {
	apps, err := store.ListRuntimeUpgradeGatewayRepairApps(ctx, cursor)
	if err != nil {
		return cursor, fmt.Errorf("read runtime gateway repair page: %w", err)
	}
	for _, appID := range apps {
		if err := refresher.RefreshDeploymentWeights(ctx, appID); err != nil {
			return cursor, fmt.Errorf("install runtime gateway repair weights: %w", err)
		}
	}
	if err := store.PruneExpiredRuntimeUpgradeGatewayReceipts(ctx); err != nil {
		return cursor, fmt.Errorf("prune runtime gateway repair receipts: %w", err)
	}
	if len(apps) < api.RuntimeUpgradeGatewayRepairBatch {
		return "", nil
	}
	return apps[len(apps)-1], nil
}

// Run is wired only by the private opt-in gateway flag. The stable session ID
// belongs to one daemon process; a restart must be reviewed as a new session.
func Run(ctx context.Context, store Store, refresher Refresher, log *slog.Logger) {
	ticker := time.NewTicker(api.RuntimeUpgradeGatewayRepairInterval)
	defer ticker.Stop()
	cursor := ""
	for {
		pollCtx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeGatewayRepairTimeout)
		next, err := Repair(pollCtx, store, refresher, cursor)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("gatewayd: private runtime routing confirmation retry")
		} else {
			cursor = next
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
