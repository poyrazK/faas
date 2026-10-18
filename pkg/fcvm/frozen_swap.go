package fcvm

// Compressed swap for frozen VMs (ADR-906 decision 6, ADR-1151).
//
// A running guest's memory stays resident: vmmd writes memory.swap.max=0 on
// every app VM scope. A frozen guest's scope gets "max", so under memory
// pressure the kernel, or a reclaim schedd asks for, can move its cold
// anonymous pages to zswap, and "0" again before the guest resumes. Swap
// opens only when tenant memory cannot reach a disk: zswap is enabled, the
// host has a swap device for zswap's slots, and an ancestor of the VM scope
// has memory.zswap.writeback=0 (the zswap Ansible role sets all three).
// Otherwise a frozen VM keeps its memory, as on a host without swap.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	swapClosed = "0"
	swapOpen   = "max"
)

// zswapEnabledPath is replaced in tests.
var zswapEnabledPath = "/sys/module/zswap/parameters/enabled"

// ErrCompressUnavailable refuses to compress a frozen guest on a host whose
// swap is not memory-only.
var ErrCompressUnavailable = errors.New("compressed swap is not available on this host")

// swapMaxSupported reports whether the scope has memory.swap.max: hosts
// without swap accounting never swap a cgroup's pages.
func swapMaxSupported(scope string) bool {
	_, err := os.Stat(filepath.Join(scope, "memory.swap.max"))
	return err == nil
}

func writeSwapMax(scope, value string) error {
	path := filepath.Join(scope, "memory.swap.max")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		return fmt.Errorf("fcvm: cgroup: write %s: %w", path, err)
	}
	return nil
}

// frozenSwapReady reports whether a frozen VM under parent (a path relative
// to the cgroup root) may swap without its memory reaching a disk.
func frozenSwapReady(parent string) bool {
	enabled, err := os.ReadFile(zswapEnabledPath)
	if err != nil || strings.TrimSpace(string(enabled)) != "Y" || hostSwapTotalKB() <= 0 {
		return false
	}
	// memory.zswap.writeback is hierarchical: 0 on any ancestor keeps every
	// descendant's swapped pages in the compressed pool.
	for dir := parent; dir != "." && dir != "/" && dir != ""; dir = filepath.Dir(dir) {
		raw, err := os.ReadFile(filepath.Join(cgroupRoot, dir, "memory.zswap.writeback"))
		if err == nil && strings.TrimSpace(string(raw)) == "0" {
			return true
		}
	}
	return false
}

func hostSwapTotalKB() int64 {
	meminfo, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(meminfo), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "SwapTotal:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb
		}
	}
	return 0
}

// FrozenSwapReady reports whether this host can compress frozen VMs of l's
// plan.
func (v *JailerVMM) FrozenSwapReady(l Lease) bool {
	return !l.IsBuilder && frozenSwapReady(vmCgroupParent(l))
}

// SetFrozenSwap opens swap to a frozen VM's scope, when the host's swap is
// memory-only, and closes it before the VM runs again. The fence record
// follows, so a repair keeps the current value.
func (v *JailerVMM) SetFrozenSwap(l Lease, frozen bool) error {
	scope := vmCgroupScope(l)
	if l.IsBuilder || !swapMaxSupported(scope) {
		return nil
	}
	value := swapClosed
	if frozen {
		if !v.FrozenSwapReady(l) {
			return nil
		}
		value = swapOpen
	}
	revert := v.trackFenceValue(l.Instance, fenceSwapMax, value)
	if err := writeSwapMax(scope, value); err != nil {
		revert()
		return err
	}
	return nil
}

// frozenSwapper is the JailerVMM side of compressed swap for frozen VMs.
type frozenSwapper interface {
	FrozenSwapReady(l Lease) bool
	SetFrozenSwap(l Lease, frozen bool) error
}

// setFrozenSwap opens or closes a frozen VM's swap. A failure only costs
// compression: the VM stays frozen without swap, and the fence repair
// writes the recorded value again.
func (m *Manager) setFrozenSwap(l Lease, frozen bool) {
	s, ok := m.vmm.(frozenSwapper)
	if !ok {
		return
	}
	if err := s.SetFrozenSwap(l, frozen); err != nil {
		m.log.Warn("freeze: set VM swap limit", "instance", l.Instance, "frozen", frozen, "err", err)
	}
}

// compressFrozen reclaims an already-frozen guest's memory into compressed
// swap, the step schedd takes before memory pressure would park it. It
// fails with ErrCompressUnavailable on a host whose swap is not
// memory-only; the caller holds the instance flight.
func (m *Manager) compressFrozen(f vmFreezer, inst *Instance) (FreezeReport, error) {
	s, ok := m.vmm.(frozenSwapper)
	if !ok || !s.FrozenSwapReady(inst.Lease) {
		return FreezeReport{}, ErrCompressUnavailable
	}
	// A VM frozen before the host's swap became ready opens it now.
	m.setFrozenSwap(inst.Lease, true)
	started := time.Now()
	reclaim, err := f.ReclaimVMMemory(inst.Lease)
	if err != nil {
		return FreezeReport{}, err
	}
	rep := FreezeReport{Reclaim: time.Since(started), BeforeBytes: reclaim.BeforeBytes, AfterBytes: reclaim.AfterBytes, SwappedBytes: reclaim.SwappedBytes}
	m.log.Info("compressed frozen instance", "instance", inst.Lease.Instance,
		"before_bytes", rep.BeforeBytes, "after_bytes", rep.AfterBytes, "swapped_bytes", rep.SwappedBytes)
	return rep, nil
}
