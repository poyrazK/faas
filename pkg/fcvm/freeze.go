package fcvm

// Frozen instances (ADR-984, prototype). Freeze pauses a running VM and asks
// the kernel to take its memory back, but keeps the Firecracker process, the
// lease, the network namespace and the guest's page tables. Thaw is a vCPU
// resume: no snapshot restore, no network setup and no resume hook. The guest
// was never cloned, so its RNG state stays unique, and kvm-clock keeps its
// clock on host time across the pause.
//
// What the kernel can reclaim depends on the host. Clean pages mapped from a
// snapshot memory file are dropped and re-read on access. Pages the guest has
// written are anonymous; they leave RAM only when the host has swap or zswap,
// otherwise they stay resident.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FreezeReport describes one Freeze. Byte counts are the VM cgroup's charge
// before and after reclaim, and the part of it moved to swap (zero on a host
// without swap or zswap).
type FreezeReport struct {
	Pause        time.Duration
	Reclaim      time.Duration
	BeforeBytes  int64
	AfterBytes   int64
	SwappedBytes int64
}

// VMMemoryReclaim is one memory.reclaim pass over a VM's cgroup.
type VMMemoryReclaim struct {
	BeforeBytes  int64
	AfterBytes   int64
	SwappedBytes int64
}

// vmFreezer is the optional VMM capability Freeze needs; Thaw uses the
// VMM's ResumeVM.
type vmFreezer interface {
	PauseVM(ctx context.Context, l Lease) error
	ReclaimVMMemory(l Lease) (VMMemoryReclaim, error)
}

// Freeze pauses a running instance and returns its reclaimable memory to the
// host. Probes stop first because a paused guest cannot answer them; Thaw
// restarts them. The instance stays live and keeps its admission charge.
func (m *Manager) Freeze(ctx context.Context, instance string) (FreezeReport, error) {
	f, ok := m.vmm.(vmFreezer)
	if !ok {
		return FreezeReport{}, fmt.Errorf("freeze %s: VMM cannot freeze", instance)
	}
	operationCtx, inst, flight, err := m.beginLiveInstanceFlight(ctx, instance)
	if err != nil {
		return FreezeReport{}, fmt.Errorf("freeze %s: %w", instance, err)
	}
	defer m.finishInstanceFlight(instance, flight)
	if inst.AppTaskOnly {
		return FreezeReport{}, fmt.Errorf("freeze %s: app task instances cannot be frozen", instance)
	}
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)
	m.cancelReadinessLoop(instance)
	m.cancelFrameworkReadyLoop(instance)

	var rep FreezeReport
	started := time.Now()
	if err := f.PauseVM(operationCtx, inst.Lease); err != nil {
		return rep, errors.Join(fmt.Errorf("freeze %s: %w", instance, err), m.unfreeze(ctx, inst))
	}
	paused := time.Now()
	rep.Pause = paused.Sub(started)
	reclaim, err := f.ReclaimVMMemory(inst.Lease)
	rep.Reclaim = time.Since(paused)
	if err != nil {
		// The guest is intact; resume it rather than leave it unreachable
		// because the host could not reclaim its memory.
		return rep, errors.Join(fmt.Errorf("freeze %s: %w", instance, err), m.unfreeze(ctx, inst))
	}
	rep.BeforeBytes, rep.AfterBytes, rep.SwappedBytes = reclaim.BeforeBytes, reclaim.AfterBytes, reclaim.SwappedBytes
	m.log.Info("frozen", "instance", instance, "pause_ms", rep.Pause.Milliseconds(),
		"reclaim_ms", rep.Reclaim.Milliseconds(), "before_bytes", rep.BeforeBytes,
		"after_bytes", rep.AfterBytes, "swapped_bytes", rep.SwappedBytes)
	return rep, nil
}

// unfreeze resumes a guest after a failed Freeze and restarts its probes.
func (m *Manager) unfreeze(ctx context.Context, inst *Instance) error {
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.vmm.ResumeVM(resumeCtx, inst.Lease); err != nil {
		return fmt.Errorf("resume after failed freeze: %w", err)
	}
	instance := inst.Lease.Instance
	m.startLivenessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.LivenessProbe)
	m.startReadinessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.ReadinessProbe)
	m.startFrameworkReadyLoop(context.WithoutCancel(ctx), instance)
	return nil
}

// Thaw resumes a frozen instance and restarts its probes. Memory the host
// reclaimed faults back in as the guest touches it.
func (m *Manager) Thaw(ctx context.Context, instance string) error {
	if err := m.ResumeVM(ctx, instance); err != nil {
		return fmt.Errorf("thaw %s: %w", instance, err)
	}
	return nil
}

// PauseVM pauses the guest's vCPUs through the Firecracker API.
func (v *JailerVMM) PauseVM(ctx context.Context, l Lease) error {
	if l.Instance == "" {
		return fmt.Errorf("vmm: PauseVM: empty instance")
	}
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return fmt.Errorf("vmm: pause: %w", err)
	}
	return nil
}

// A memory.reclaim write can stay in the kernel for minutes: on the n2 node
// the kernel kept retrying pages it could not free, with and without swap.
// ReclaimVMMemory therefore asks only for reclaimable pages, in chunks, stops
// at the first EAGAIN, and interrupts a write still running at reclaimBudget.
const (
	reclaimChunkBytes = 16 << 20
	reclaimBudget     = 2 * time.Second
)

// procMeminfoPath is replaced in tests.
var procMeminfoPath = "/proc/meminfo"

// ReclaimVMMemory asks the kernel to reclaim the VM cgroup's file pages, and
// its anonymous pages too when the cgroup can swap.
func (v *JailerVMM) ReclaimVMMemory(l Lease) (VMMemoryReclaim, error) {
	parent := ParentCgroupFor(l.Plan)
	if l.IsBuilder {
		parent = BuilderCgroupParent
	}
	scope := filepath.Join(cgroupRoot, parent, PerInstanceScope(l.Instance))
	var r VMMemoryReclaim
	var err error
	if r.BeforeBytes, err = readCgroupInt(scope, "memory.current"); err != nil {
		return r, err
	}
	stat, err := readCgroupStat(scope)
	if err != nil {
		return r, err
	}
	// tmpfs pages count as file pages but, like anonymous ones, can only
	// go to swap.
	target := stat["file"] - stat["shmem"]
	if canSwap(scope) {
		target += stat["anon"] + stat["shmem"]
	}
	deadline := time.Now().Add(reclaimBudget)
	for requested := int64(0); requested < target; {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		n := min(int64(reclaimChunkBytes), target-requested)
		err := writeMemoryReclaim(filepath.Join(scope, "memory.reclaim"), n, remaining)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			// Nothing more to reclaim, or the budget ran out mid-write.
			break
		}
		if err != nil {
			return r, fmt.Errorf("vmm: reclaim %s: %w", l.Instance, err)
		}
		requested += n
	}
	if r.AfterBytes, err = readCgroupInt(scope, "memory.current"); err != nil {
		return r, err
	}
	// memory.swap.current is absent without swap accounting.
	r.SwappedBytes, _ = readCgroupInt(scope, "memory.swap.current")
	return r, nil
}

// canSwap reports whether the kernel may move this cgroup's anonymous pages
// to swap: the host has free swap and memory.swap.max is not 0.
func canSwap(scope string) bool {
	if limit, err := os.ReadFile(filepath.Join(scope, "memory.swap.max")); err != nil || strings.TrimSpace(string(limit)) == "0" {
		return false
	}
	meminfo, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(meminfo), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "SwapFree:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			return err == nil && kb > 0
		}
	}
	return false
}

func readCgroupStat(scope string) (map[string]int64, error) {
	raw, err := os.ReadFile(filepath.Join(scope, "memory.stat"))
	if err != nil {
		return nil, fmt.Errorf("vmm: read memory.stat: %w", err)
	}
	stat := map[string]int64{}
	for _, line := range strings.Split(string(raw), "\n") {
		if key, value, ok := strings.Cut(line, " "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				stat[key] = n
			}
		}
	}
	return stat, nil
}

func readCgroupInt(scope, name string) (int64, error) {
	raw, err := os.ReadFile(filepath.Join(scope, name))
	if err != nil {
		return 0, fmt.Errorf("vmm: read %s: %w", name, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("vmm: parse %s: %w", name, err)
	}
	return n, nil
}
