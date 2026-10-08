package state

// adr: 762
import "github.com/onebox-faas/faas/pkg/runtimequota"

func RegionalRuntimeAttemptMatches(source runtimequota.LaunchSource, ins Instance) bool {
	p := source.Permit
	return source.Validate() == nil && runtimequota.MatchesID(p.InstanceID, ins.ID) &&
		runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, ins.AppID) && runtimequota.MatchesID(p.DeploymentID, ins.DeploymentID) &&
		runtimequota.MatchesID(p.NodeID, ins.NodeID) && runtimequota.MatchesID(p.RunID, ins.WakeID)
}

func checkRegionalAttemptFailure(source runtimequota.LaunchSource, launch RegionalRuntimeLaunchSource, receipt RegionalRuntimeCapacityReceipt) error {
	if source.Validate() != nil || launch.Source != source || receipt.Permit != source.Permit {
		return ErrConflict
	}
	if receipt.IssuedAt == nil || receipt.FenceID == "" || receipt.ReleasedAt == nil || receipt.Stop == nil || receipt.StopEnvelope == nil ||
		receipt.Stop.Validate() != nil || receipt.Stop.Permit != source.Permit || receipt.Stop.FenceID != receipt.FenceID {
		return ErrRegionalRuntimePlacementUnavailable
	}
	return nil
}

// A snapshotting prime has no FAILED edge. Native stop permits its ordinary
// STOPPED edge; no new lifecycle transition is introduced by cleanup.
func RegionalRuntimeFailureState(from State) State {
	if from == StateFailed || CanTransition(from, StateFailed) {
		return StateFailed
	}
	if from == StateStopped || CanTransition(from, StateStopped) {
		return StateStopped
	}
	return ""
}
