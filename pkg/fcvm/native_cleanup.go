package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func (v *JailerVMM) killNative(ctx context.Context, lease Lease) error {
	budget := v.destroyWait
	if budget <= 0 {
		budget = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	owned, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	if lease.UID != 0 && !sameNativeJournalLease(owned.Lease, lease) {
		return errors.New("vmm: native stop lease differs from launch ownership")
	}
	v.cancelStartupCPUBoostTail(lease.Instance)
	v.closeGuestVsockListeners(lease.Instance)
	v.unregisterRing(lease.Instance)
	v.mu.Lock()
	cmd, rec := v.proc[lease.Instance], v.recs[lease.Instance]
	if rec != nil {
		rec.stopping = true
	}
	v.mu.Unlock()
	record, err := v.nativeRetirementRecord(ctx, lease)
	if err != nil {
		return fmt.Errorf("vmm: native process retirement: %w", err)
	}
	helpers := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: v.nativeRecovery.helperGroups}
	if err := helpers.retireAll(ctx, record); err != nil {
		return fmt.Errorf("vmm: native host helper retirement: %w", err)
	}
	loops := nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.loopMounts}
	if err := loops.retireAll(ctx, record); err != nil {
		return fmt.Errorf("vmm: native staging retirement: %w", err)
	}
	tun := nativeTunBindJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.tunBinds, helperGroups: v.nativeRecovery.helperGroups}
	if err := tun.retire(ctx, record); err != nil {
		return fmt.Errorf("vmm: native TUN reference retirement: %w", err)
	}
	images := nativeImageSourceJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.imageSources}
	if err := images.retireAll(ctx, record); err != nil {
		return fmt.Errorf("vmm: native image reference retirement: %w", err)
	}
	if cmd != nil && cmd.Process != nil && (rec == nil || rec.done == nil) {
		return errors.New("vmm: native process retirement has no watchdog")
	}
	if rec != nil {
		if rec.done == nil {
			return errors.New("vmm: native record has no watchdog")
		}
		select {
		case <-rec.done:
		case <-ctx.Done():
			return fmt.Errorf("vmm: native watchdog retirement: %w", ctx.Err())
		}
	}
	if !record.ResourcesRemoved && v.nativeRecovery.generation(lease.Instance) != record.Generation {
		// Boot/provision/export helpers can outlive daemon death too. Their
		// durable incarnation/producer frames are not implemented yet. A
		// pinned Firecracker exit cannot authorize deleting their resources or
		// recycling their slot. Previously complete receipts remain retryable.
		return errors.New("native recovery: recovered host helper ownership is not yet attested")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	roots, err := v.nativeInstanceRoots(lease.Instance)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if err := v.unmountNativeBinds(ctx, lease.Instance, root); err != nil {
			return err
		}
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("vmm: remove native chroot: %w", err)
		}
	}
	if err := removeNativeCgroup(nativeCgroupScope(record.Lease)); err != nil {
		return fmt.Errorf("vmm: remove native cgroup: %w", err)
	}
	v.mu.Lock()
	if v.recs[lease.Instance] == rec {
		delete(v.recs, lease.Instance)
	}
	if v.proc[lease.Instance] == cmd {
		delete(v.proc, lease.Instance)
	}
	v.mu.Unlock()
	v.preBoot.forget(lease.Instance)
	v.closeClient(lease.Instance)
	v.sweepMaterialised(lease.Instance)
	return ctx.Err()
}

// Cgroup pseudo-files cannot be unlinked. Remove only directories; the kernel
// refuses rmdir while tasks/children remain. Every failure preserves ownership.
func removeNativeCgroup(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("native recovery: unexpected cgroup symlink")
		}
		if entry.IsDir() {
			if err := removeNativeCgroup(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	return os.Remove(path)
}

func (v *JailerVMM) unmountNativeBinds(ctx context.Context, instance, root string) error {
	r := v.nativeRecovery
	probe := r.mounts
	if probe == nil {
		probe = nativeJailMounts
	}
	unmount := r.unmount
	if unmount == nil {
		unmount = func(ctx context.Context, path string) error {
			return exec.CommandContext(ctx, "umount", "--", path).Run()
		}
	}
	v.mu.Lock()
	binds := append([]ephemeralBind(nil), v.bindMounts[instance]...)
	v.mu.Unlock()
	for i := len(binds) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		b := binds[i]
		mounts, err := probe(root)
		if err != nil {
			return err
		}
		for _, mount := range mounts {
			if mount == b.mountpoint {
				if err := unmount(ctx, mount); err != nil {
					return fmt.Errorf("native recovery: unmount owned bind: %w", err)
				}
			}
		}
		mounts, err = probe(root)
		if err != nil {
			return err
		}
		for _, mount := range mounts {
			if mount == b.mountpoint {
				return errors.New("native recovery: owned bind survived unmount")
			}
		}
		if err := os.Remove(b.mountpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := v.releaseNativeBindSource(b.source); err != nil {
			return err
		}
		v.mu.Lock()
		v.bindMounts[instance] = v.bindMounts[instance][:i]
		v.mu.Unlock()
	}
	mounts, err := probe(root)
	if err != nil {
		return err
	}
	if len(mounts) != 0 {
		return errors.New("native recovery: chroot retains mounts without recoverable bind provenance")
	}
	v.mu.Lock()
	delete(v.bindMounts, instance)
	v.mu.Unlock()
	return nil
}

func (v *JailerVMM) releaseNativeBindSource(source string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	state, found := v.bindSourceModes[source]
	if !found {
		return nil
	}
	if state.refs > 1 {
		state.refs--
		v.bindSourceModes[source] = state
		return nil
	}
	if err := os.Chmod(source, state.mode); err != nil {
		return fmt.Errorf("native recovery: restore bind source mode: %w", err)
	}
	delete(v.bindSourceModes, source)
	return nil
}
