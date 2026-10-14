package dbsched

// Store helpers for standbys (ADR-1027 production class), shared by both
// stores.

// errCodeFailedOver marks a database whose standby took over.
const errCodeFailedOver = "failed_over"

// pickStandby chooses a replicated database's standby node: the active
// node offering its major with the most free volume capacity that fits
// its volume, other than the excluded ones.
func pickStandby(nodes []Node, usage map[string]nodeUsage, vm VM, exclude ...string) (Node, bool) {
	others := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		skip := false
		for _, id := range exclude {
			skip = skip || n.ID == id
		}
		if !skip {
			others = append(others, n)
		}
	}
	return pickNode(others, usage, vm.VolumeBytes, "", vm.Major)
}

// claimStandbyOn checks a standby VM against its node.
func claimStandbyOn(node Node, usage nodeUsage, memMiB int) error {
	switch {
	case node.Lifecycle == NodeUnavailable:
		return ErrNodeUnavailable
	case !admits(node, usage, memMiB):
		return ErrNoCapacity
	}
	return nil
}

// canFailover reports whether a database's standby may take over.
func canFailover(vm VM) bool {
	switch vm.State {
	case StateParked, StateRunning, StateParking, StateFailed:
		return vm.StandbySynced && vm.StandbyNode != ""
	}
	return false
}
