package gatewayconfirmation

import (
	"context"
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"github.com/onebox-faas/faas/pkg/state"
)

type DrainRecorder interface {
	RecordRuntimeUpgradeGatewayDrain(context.Context, state.RuntimeUpgradeGatewayDrainObservation) error
}

// RecordDrain runs under the gateway's per-app weight-refresh stripe, after the
// picker swaps the SAME snapshot. Only a closed, known zero can be published.
// Publication failure keeps admission closed and remains retryable.
func RecordDrain(ctx context.Context, store DrainRecorder, tracker *activity.Tracker, slotID, appID string, snapshot gateway.DeploymentWeightsSnapshot) error {
	if tracker == nil || snapshot.RoutingRevision == "" {
		return state.ErrInvalidArgument
	}
	plan := snapshot.Drain
	if plan == nil {
		tracker.ReconcileRouting(appID, snapshot.RoutingRevision)
		return nil
	}
	if !drainInstalledWeightsMatch(snapshot.Rows, plan) {
		return state.ErrConflict
	}
	binding := plan.OperationID + ":" + plan.GatewayRosterRevision
	installed, err := tracker.InstallFence(appID, plan.ServingDeploymentID, snapshot.RoutingRevision, binding)
	if err != nil {
		return fmt.Errorf("install runtime forwarding fence: %w", err)
	}
	fence, observation, closed := tracker.ObserveFence(appID)
	if !closed || fence != installed || !observation.CoverageKnown || observation.ActiveForwards != 0 || plan.GatewayRosterRevision == "" {
		return nil
	}
	return store.RecordRuntimeUpgradeGatewayDrain(ctx, state.RuntimeUpgradeGatewayDrainObservation{
		AppID: appID, SlotID: slotID, SessionID: observation.SessionID, FenceID: fence.ID, RoutingRevision: snapshot.RoutingRevision, ActivityVersion: strconv.FormatUint(observation.ActivityVersion, 10),
		RuntimeUpgradeDrainPlan: state.RuntimeUpgradeDrainPlan{OperationID: plan.OperationID, DeploymentID: plan.DeploymentID, ServingDeploymentID: plan.ServingDeploymentID,
			GatewayRosterRevision: plan.GatewayRosterRevision, CutoverAt: plan.CutoverAt},
	})
}

func drainInstalledWeightsMatch(rows []gateway.DeploymentWeightsRow, plan *gateway.RuntimeUpgradeDrainPlan) bool {
	positive, previous := 0, false
	for _, r := range rows {
		if r.TrafficPercent > 0 {
			if r.ID != plan.DeploymentID || r.TrafficPercent != 100 {
				return false
			}
			positive++
		}
		if r.ID == plan.ServingDeploymentID {
			previous = r.TrafficPercent == 0
		}
	}
	return positive == 1 && previous && plan.DeploymentID != plan.ServingDeploymentID
}
