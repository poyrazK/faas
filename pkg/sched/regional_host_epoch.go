package sched

// adr: 494
import "github.com/onebox-faas/faas/pkg/runtimequota"

// RegionalRuntimeEpochForNode refuses absent discovery and cross-node samples.
// The caller must use the registered compute node ID, never a caller-supplied
// destination. This sample cannot replace host admission or verify native stop.
func (s *StatsSnapshot) RegionalRuntimeEpochForNode(nodeID string) (runtimequota.HostEpoch, error) {
	if s == nil || s.RegionalRuntimeHostEpoch == nil {
		return runtimequota.HostEpoch{}, runtimequota.ErrTrust
	}
	epoch := *s.RegionalRuntimeHostEpoch
	if err := epoch.Validate(); err != nil {
		return runtimequota.HostEpoch{}, err
	}
	if epoch.NodeID != nodeID {
		return runtimequota.HostEpoch{}, runtimequota.ErrBinding
	}
	return epoch, nil
}
