package dbsched

import (
	"context"
	"errors"
	"fmt"
)

// Resize changes a database's VM shape and grows its volume (ADR-1027).
// Firecracker hotplugs neither, so a running database restarts on the new
// shape, a few seconds of downtime after a clean stop; a parked one takes it
// at its next wake. The volume grows offline on its node and never shrinks.
// The record carries ResizePending until the VM is stopped on the new shape
// and its volume grown, so repeating the call resumes an interrupted resize.
//
// The shape also says whether the database keeps a synchronous standby
// (the production class). Turning it on places the standby, which attaches
// after the restart or the next wake. Turning it off first retires the
// standby: the database stops cleanly, then the standby, and the standby's
// volume goes; the primary's slot for it is dropped once the primary runs
// again (CheckStandbys).
func (s *Scheduler) Resize(ctx context.Context, id string, shape Shape) (VM, error) {
	switch {
	case shape.VcpuCount < 1 || shape.VcpuCount > 32:
		return VM{}, fmt.Errorf("%w: vcpu count %d out of range", ErrInvalid, shape.VcpuCount)
	case shape.MemMiB < 128 || shape.MemMiB > 65536:
		return VM{}, fmt.Errorf("%w: memory %d MiB out of range", ErrInvalid, shape.MemMiB)
	case shape.VolumeBytes < minVolumeBytes || shape.VolumeBytes > maxVolumeBytes:
		return VM{}, fmt.Errorf("%w: volume size %d out of range", ErrInvalid, shape.VolumeBytes)
	}
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return VM{}, err
	}
	restart := vm.State == StateRunning
	if vm.Replicated && !shape.Replicated {
		if err := s.retireStandby(ctx, vm); err != nil {
			return vm, fmt.Errorf("dbsched: resize %s: retire the standby: %w", id, err)
		}
	}
	if err := s.store.Resize(ctx, id, shape, s.now()); err != nil {
		return VM{}, err
	}
	vm, err = s.store.Get(ctx, id)
	if err != nil || !vm.ResizePending {
		return s.withTarget(ctx, vm), err
	}
	if vm.State == StateRunning {
		if err := s.parkLocked(ctx, id); err != nil {
			return vm, fmt.Errorf("dbsched: resize %s: stop: %w", id, err)
		}
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return vm, err
	}
	if err := s.vmm.GrowVolume(ctx, node, id, vm.VolumeBytes); err != nil {
		return vm, fmt.Errorf("dbsched: resize %s: grow volume: %w", id, err)
	}
	if err := s.store.FinishResize(ctx, id); err != nil {
		return vm, err
	}
	s.log.Info("dbsched: resized", "database_id", id, "vcpus", shape.VcpuCount, "mem_mib", shape.MemMiB, "volume_bytes", shape.VolumeBytes,
		"replicated", shape.Replicated, "restarted", restart)
	if restart || vm.AlwaysOn {
		return s.wakeLocked(ctx, id)
	}
	return s.Get(ctx, id)
}

// retireStandby removes a replicated database's standby before its
// replication is turned off: the database stops cleanly (the primary
// first, so its standby keeps every commit to the end), then the standby
// VM, then its volume. A standby node that is unavailable or gone keeps
// the volume for the orphan sweep. The database's lock is held.
func (s *Scheduler) retireStandby(ctx context.Context, vm VM) error {
	if vm.State == StateRunning {
		if err := s.parkLocked(ctx, vm.DatabaseID); err != nil {
			return err
		}
		cur, err := s.store.Get(ctx, vm.DatabaseID)
		if err != nil {
			return err
		}
		vm = cur
	}
	if err := s.store.MarkStandbySynced(ctx, vm.DatabaseID, false); err != nil {
		return err
	}
	if vm.StandbyRunning {
		s.stopStandby(ctx, vm, false)
	}
	if vm.StandbyNode == "" {
		return nil
	}
	node, err := s.node(ctx, vm.StandbyNode)
	if errors.Is(err, ErrNodeUnavailable) || err == nil && node.Lifecycle == NodeUnavailable {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
		return err
	}
	return s.vmm.DiscardVolume(ctx, node, vm.DatabaseID)
}
