package dbsched

import (
	"context"
	"fmt"
)

// Resize changes a database's VM shape and grows its volume (ADR-1027).
// Firecracker hotplugs neither, so a running database restarts on the new
// shape, a few seconds of downtime after a clean stop; a parked one takes it
// at its next wake. The volume grows offline on its node and never shrinks.
// The record carries ResizePending until the VM is stopped on the new shape
// and its volume grown, so repeating the call resumes an interrupted resize.
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
	if err := s.store.Resize(ctx, id, shape, s.now()); err != nil {
		return VM{}, err
	}
	vm, err := s.store.Get(ctx, id)
	if err != nil || !vm.ResizePending {
		return s.withTarget(ctx, vm), err
	}
	restart := vm.State == StateRunning
	if restart {
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
	s.log.Info("dbsched: resized", "database_id", id, "vcpus", shape.VcpuCount, "mem_mib", shape.MemMiB, "volume_bytes", shape.VolumeBytes, "restarted", restart)
	if restart || vm.AlwaysOn {
		return s.wakeLocked(ctx, id)
	}
	return s.Get(ctx, id)
}
