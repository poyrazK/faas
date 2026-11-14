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
// after the restart or the next wake. Turning it off checks admission
// before a clean stop of the primary and standby. The standby's volume
// stays until the new shape is committed; its slot is dropped once the
// primary runs again (CheckStandbys).
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
	retiring := vm.Replicated && !shape.Replicated
	if retiring {
		if err := s.checkRetiringResize(ctx, vm, shape); err != nil {
			return vm, err
		}
		// A clean park keeps the old standby synchronized and its volume
		// intact until the store admits and records the requested shape.
		if restart {
			if err := s.parkLocked(ctx, id); err != nil {
				return vm, err
			}
		}
	}
	if err := s.store.Resize(ctx, id, shape, s.now()); err != nil {
		if retiring && restart {
			// Capacity or placement can change after the preflight. Resume
			// the unchanged shape and trusted standby on a failed admission.
			_, wakeErr := s.wakeLocked(context.WithoutCancel(ctx), id)
			err = errors.Join(err, wakeErr)
		}
		return VM{}, err
	}
	if retiring {
		s.deleteStandby(ctx, vm) // admitted: failed cleanup is retried by the orphan sweep
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

// checkRetiringResize rejects invalid or unplaceable changes before any
// VM stops. The store repeats admission atomically after the clean park.
func (s *Scheduler) checkRetiringResize(ctx context.Context, vm VM, shape Shape) error {
	checking := vm
	checking.StandbyRunning = false // the clean park stops it before committing
	if err := checkResize(checking, shape); err != nil {
		return err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return err
	}
	load, err := s.store.Loads(ctx)
	if err != nil {
		return err
	}
	return resizeFits(node, nodeUsage{volumeBytes: load[node.ID].VolumeBytes, activeMB: load[node.ID].ActiveMB}, vm, shape)
}
