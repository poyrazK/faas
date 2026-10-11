package gatewayconfirmation

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type Recorder interface {
	RecordRuntimeUpgradeGateway(context.Context, string, string, string) error
}

// RecordInstalled ignores non-upgrade weight shapes. The store must repeat the
// authoritative app/cutover/traffic fence before acknowledging this candidate.
func RecordInstalled(ctx context.Context, store Recorder, sessionID, appID string, rows []gateway.DeploymentWeightsRow) error {
	if sessionID == "" {
		return nil
	}
	candidate := ""
	for _, row := range rows {
		if row.TrafficPercent <= 0 {
			continue
		}
		if candidate != "" || row.TrafficPercent != 100 {
			return nil
		}
		candidate = row.ID
	}
	if candidate == "" {
		return nil
	}
	return store.RecordRuntimeUpgradeGateway(ctx, appID, sessionID, candidate)
}
