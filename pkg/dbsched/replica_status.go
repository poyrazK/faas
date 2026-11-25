package dbsched

import (
	"context"
	"time"
)

// ReplicaStatus is a sample for an already-running database's standby.
type ReplicaStatus struct {
	Available      bool
	ReplayLagBytes *int64
	ObservedAt     time.Time
	StandbyNode    string
	ReplicaTarget  string
}

const replicaSampleInterval = time.Second

type replicaPlacement struct {
	primary, standby, target string
	stateChangedAt           time.Time
}

func replicaPlacementOf(vm VM) replicaPlacement {
	return replicaPlacement{primary: vm.NodeID, standby: vm.StandbyNode, target: vm.ReplicaTarget, stateChangedAt: vm.StateChangedAt}
}

type replicaSample struct {
	placement replicaPlacement
	expiresAt time.Time
	status    ReplicaStatus
}

// ReadReplica reads current placement and samples replay without waking
// either VM. Concurrent pool checkouts share a short control-plane probe.
func (s *Scheduler) ReadReplica(ctx context.Context, id string) (ReplicaStatus, error) {
	vm, err := s.Get(ctx, id)
	if err != nil {
		return ReplicaStatus{}, err
	}
	if vm.Held || vm.ReplicaTarget == "" || vm.NodeTarget == "" {
		return ReplicaStatus{}, nil
	}
	placement := replicaPlacementOf(vm)
	if sample, ok := s.cachedReplicaSample(id, placement); ok {
		return sample, nil
	}

	result := s.replicaProbes.DoChan(id, func() (any, error) {
		if sample, ok := s.cachedReplicaSample(id, placement); ok {
			return replicaSample{placement: placement, status: sample}, nil
		}
		probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), standbyProbeTimeout)
		defer cancel()
		now := s.now()
		repl, probeErr := s.vmm.Replication(probe, Node{ID: vm.NodeID, TargetURL: vm.NodeTarget}, id, "", "")
		sample := replicaSample{
			placement: placement,
			expiresAt: s.now().Add(replicaSampleInterval),
		}
		if probeErr == nil {
			sample.status = ReplicaStatus{
				ObservedAt:     now,
				Available:      repl.Synchronous && repl.Streaming,
				ReplayLagBytes: repl.ReplayLagBytes,
				StandbyNode:    vm.StandbyNode,
				ReplicaTarget:  vm.ReplicaTarget,
			}
		}
		current, err := s.Get(probe, id)
		if err != nil || current.Held || replicaPlacementOf(current) != placement {
			return replicaSample{}, nil
		}
		s.replicaSamples.Store(id, sample)
		return sample, nil
	})
	select {
	case <-ctx.Done():
		return ReplicaStatus{}, ctx.Err()
	case received := <-result:
		sample := received.Val.(replicaSample)
		if sample.placement != placement {
			return ReplicaStatus{}, nil
		}
		return sample.status, nil
	}
}

func (s *Scheduler) cachedReplicaSample(id string, placement replicaPlacement) (ReplicaStatus, bool) {
	if cached, ok := s.replicaSamples.Load(id); ok {
		sample := cached.(replicaSample)
		if sample.placement == placement && s.now().Before(sample.expiresAt) {
			return sample.status, true
		}
	}
	return ReplicaStatus{}, false
}
