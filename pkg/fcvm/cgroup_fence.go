package fcvm

// Per-VM cgroup fence registry and repair (spec §4.4, §11).
//
// systemd owns the tenant and builder slices but not the per-VM scopes that
// jailer creates under them. A daemon-reload makes systemd recompute each
// slice's cgroup.subtree_control from the slice's own child units. A slice
// with no child units gets an empty set, and the kernel removes memory.max,
// cpu.weight and cpu.max from every VM scope below it. When vmmd later
// re-enables the controllers (ensureSubtreeControllers on the next wake) the
// files come back at their defaults: memory.max "max", cpu.weight 100 and
// cpu.max "max". Any daemon-reload, including one from an unrelated Ansible
// role, therefore strips the fence from every VM running at that moment.
//
// The systemd_slices role keeps an empty child slice active under each
// parent, so systemd keeps those controllers enabled. This registry is the
// second line of defence: JailerVMM records the fence it wrote for every
// live VM, and RepairCgroupFences rewrites any value that no longer matches.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupFence is the host-side fence vmmd wrote for one live VM scope.
type cgroupFence struct {
	lease     Lease
	parent    string // absolute path of the parent slice
	scope     string // absolute path of the per-VM scope
	memoryMax string // bytes; raised while a snapshot is captured
	cpuWeight string
	cpuMax    string // "<quota> <period>"
	// swapMax is "0", or "max" while the VM is frozen on a host with
	// memory-only swap; empty where the fence does not manage it (builders,
	// hosts without swap accounting).
	swapMax string
}

// CgroupFenceRepairReport records one RepairCgroupFences pass.
type CgroupFenceRepairReport struct {
	Checked  int
	Repaired int
	Failed   int
	// Gone counts records whose scope no longer exists: the VM was torn
	// down without passing through Kill, so the record is dropped.
	Gone int
}

func vmCgroupParent(l Lease) string {
	if l.IsBuilder {
		return BuilderCgroupParent
	}
	return ParentCgroupFor(l.Plan)
}

func memoryMaxValue(memoryMB int) string {
	return strconv.FormatInt(int64(memoryMB)<<20, 10)
}

// newCgroupFence returns the fence applyPreBootCgroupFence writes for l.
func newCgroupFence(l Lease) (*cgroupFence, error) {
	parent := filepath.Join(cgroupRoot, vmCgroupParent(l))
	f := &cgroupFence{
		lease:     l,
		parent:    parent,
		scope:     filepath.Join(parent, PerInstanceScope(l.Instance)),
		cpuWeight: strconv.Itoa(vmCPUWeight(l.Plan, l.IsBuilder)),
	}
	if l.IsBuilder {
		f.memoryMax = fenceMemoryMaxFor(l)
		f.cpuMax = builderCPUMax
		return f, nil
	}
	cpuMax, err := appCPUMax(l.Plan, l.CPUMillicores)
	if err != nil {
		return nil, err
	}
	f.memoryMax = fenceMemoryMaxFor(l)
	f.cpuMax = cpuMax
	if swapMaxSupported(f.scope) {
		f.swapMax = swapClosed
	}
	return f, nil
}

type fenceFile struct{ name, want string }

func (f *cgroupFence) files() []fenceFile {
	files := []fenceFile{
		{"memory.max", f.memoryMax},
		{"cpu.weight", f.cpuWeight},
		{"cpu.max", f.cpuMax},
	}
	if f.swapMax != "" {
		files = append(files, fenceFile{"memory.swap.max", f.swapMax})
	}
	return files
}

// drift names the fence files whose content differs from the record. A
// missing file means the controller is disabled on the parent slice.
func (f *cgroupFence) drift() []string {
	var out []string
	for _, file := range f.files() {
		raw, err := os.ReadFile(filepath.Join(f.scope, file.name))
		if err != nil || strings.TrimSpace(string(raw)) != file.want {
			out = append(out, file.name)
		}
	}
	return out
}

// apply re-enables the parent's controllers, which recreates missing files at
// their defaults, then writes every recorded value.
func (f *cgroupFence) apply() error {
	if err := ensureSubtreeControllers(f.parent, perInstanceControllers); err != nil {
		return err
	}
	for _, file := range f.files() {
		path := filepath.Join(f.scope, file.name)
		if err := os.WriteFile(path, []byte(file.want+"\n"), 0o644); err != nil {
			return fmt.Errorf("fcvm: cgroup: write %s: %w", path, err)
		}
	}
	return nil
}

// trackCgroupFence records the fence applyPreBootCgroupFence just wrote.
func (v *JailerVMM) trackCgroupFence(l Lease) error {
	f, err := newCgroupFence(l)
	if err != nil {
		return err
	}
	v.fenceMu.Lock()
	if v.fences == nil {
		v.fences = make(map[string]*cgroupFence)
	}
	v.fences[l.Instance] = f
	v.fenceMu.Unlock()
	return nil
}

func (v *JailerVMM) untrackCgroupFence(instance string) {
	v.fenceMu.Lock()
	delete(v.fences, instance)
	v.fenceMu.Unlock()
}

// trackFenceValue changes one recorded value before the caller writes the
// same value to the cgroup, so a concurrent repair can only ever write the
// newer value. The returned function puts the previous value back for a
// caller whose write failed, unless a later change already replaced it.
func (v *JailerVMM) trackFenceValue(instance string, field func(*cgroupFence) *string, value string) (revert func()) {
	v.fenceMu.Lock()
	defer v.fenceMu.Unlock()
	f := v.fences[instance]
	if f == nil {
		return func() {}
	}
	slot := field(f)
	previous := *slot
	*slot = value
	return func() {
		v.fenceMu.Lock()
		if v.fences[instance] == f && *slot == value {
			*slot = previous
		}
		v.fenceMu.Unlock()
	}
}

func fenceCPUMax(f *cgroupFence) *string    { return &f.cpuMax }
func fenceMemoryMax(f *cgroupFence) *string { return &f.memoryMax }
func fenceSwapMax(f *cgroupFence) *string   { return &f.swapMax }

// widenSnapshotFence raises memory.max for a snapshot capture and keeps the
// record in step, so a repair during the capture restores the widened limit
// rather than the ordinary one that would OOM the capture.
func (v *JailerVMM) widenSnapshotFence(l Lease) (func() error, error) {
	if l.MemoryMaxMiB < 1 {
		return widenSnapshotMemoryCgroup(l)
	}
	_, _, widenedMB := snapshotMemoryLimits(l)
	revert := v.trackFenceValue(l.Instance, fenceMemoryMax, memoryMaxValue(widenedMB))
	restore, err := widenSnapshotMemoryCgroup(l)
	if err != nil {
		revert()
		return nil, err
	}
	return func() error {
		revert()
		return restore()
	}, nil
}

// RepairCgroupFences rewrites the recorded fence of every live VM whose
// cgroup no longer matches it. A VM whose fence cannot be restored is killed:
// it must not keep running without its memory and CPU limits.
func (v *JailerVMM) RepairCgroupFences(ctx context.Context) CgroupFenceRepairReport {
	var rep CgroupFenceRepairReport
	var unfenced []Lease
	v.fenceMu.Lock()
	for instance, f := range v.fences {
		rep.Checked++
		drift := f.drift()
		if len(drift) == 0 {
			continue
		}
		if _, err := os.Stat(f.scope); errors.Is(err, os.ErrNotExist) {
			delete(v.fences, instance)
			rep.Gone++
			continue
		}
		if err := f.apply(); err != nil {
			rep.Failed++
			slog.Error("vmm: restore cgroup fence", "instance", instance, "drift", drift, "err", err)
			unfenced = append(unfenced, f.lease)
			continue
		}
		rep.Repaired++
		slog.Warn("vmm: restored cgroup fence", "instance", instance, "drift", drift)
	}
	v.fenceMu.Unlock()
	for _, l := range unfenced {
		if err := v.Kill(context.WithoutCancel(ctx), l); err != nil {
			slog.Error("vmm: kill instance after cgroup fence restore failure", "instance", l.Instance, "err", err)
		}
	}
	return rep
}

// applyAdoptedFence rewrites an adopted guest's recorded fence where it
// drifted (adoption ADR §2 proof step 4 and "Loops").
func (v *JailerVMM) applyAdoptedFence(instance string) error {
	v.fenceMu.Lock()
	defer v.fenceMu.Unlock()
	f := v.fences[instance]
	if f == nil || len(f.drift()) == 0 {
		return nil
	}
	return f.apply()
}
