package fcvm

// adr: 641, 648
import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Kernel limits are observations, not requested guest resources or sampled RSS.
// Nil quota/limit means the kernel reports "max"; it is never a zero charge.
type NativeRuntimeCgroup struct {
	Path                   string
	Device, Inode, MountID uint64
	MemoryMaxBytes         *uint64
	CPUQuotaUS             *uint64
	CPUPeriodUS, BurstUS   uint64
	PIDs                   []int
	Populated              bool
}

type NativeRuntimeObservation struct {
	Instance, Generation, KernelBootID                   string
	Lease                                                Lease
	Placement                                            *runtimequota.Placement
	OriginalResources                                    *state.RegionalRuntimeResources
	PID                                                  int
	StartTime                                            uint64
	VMState                                              string
	Machine                                              *Machine
	Cgroup                                               *NativeRuntimeCgroup
	Authorized, Revoked, ExitConfirmed, ResourcesRemoved bool
	// Unresolved remains charged/quarantined. An observation mismatch does not
	// replace original provenance, acknowledge absence or authorize a launch.
	Unresolved string
}

type NativeRuntimeInventory struct {
	KernelBootID    string
	Leases          []Lease
	Owners          []NativeRuntimeObservation
	Cgroups         []NativeRuntimeCgroup
	ManagedUIDTasks []NativeRuntimeTask
	LaunchTasks     []NativeRuntimeLaunchTask
	Unresolved      []string
}

type nativeRuntimeCgroups interface {
	Inventory(context.Context) ([]NativeRuntimeCgroup, error)
}

type nativeRuntimeInventoryVMM interface {
	withNativeRuntimeInventory(context.Context, func(context.Context, NativeRuntimeInventory) error) error
}

type nativeRuntimeObservedSession struct {
	api      *nativeMachineAPI
	state    string
	machine  Machine
	measured bool
}

// WithNativeRuntimeInventory always scans afresh and invokes the consumer under
// the exclusive native barrier. Startup's cached recovery flag is insufficient.
// The payload is advisory observation, not a signed reconciliation or stop ACK.
func (m *Manager) WithNativeRuntimeInventory(ctx context.Context, consume func(context.Context, NativeRuntimeInventory) error) error {
	if m == nil || consume == nil {
		return errors.New("native inventory: manager and consumer are required")
	}
	v, ok := m.vmm.(nativeRuntimeInventoryVMM)
	if !ok || m.nativeVMM() == nil {
		return errors.New("native inventory: complete native observation capability is unavailable")
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return err
	}
	return v.withNativeRuntimeInventory(ctx, consume)
}

func (v *JailerVMM) withNativeRuntimeInventory(ctx context.Context, consume func(context.Context, NativeRuntimeInventory) error) error {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || r.runtimeGroups == nil || r.support == nil || r.resources == nil || r.inventory == nil || consume == nil {
		return errors.New("native inventory: independent cgroup capability is unavailable")
	}
	return v.withNativeInventory(ctx, func(ctx context.Context) error {
		leases, err := v.scanNativeRecoveryLeases(ctx)
		if err != nil {
			return err
		}
		boot, err := r.journal.currentBootID()
		if err != nil {
			return err
		}
		records, err := r.journal.records(ctx)
		if err != nil {
			return err
		}
		groups, err := r.runtimeGroups.Inventory(ctx)
		if err != nil {
			return err
		}
		tasks, err := r.retirer.probe.managedUIDTasks(ctx)
		if err != nil {
			return err
		}
		launchTasks, err := r.retirer.probe.launchTasks(ctx)
		if err != nil {
			return err
		}
		inventory := NativeRuntimeInventory{KernelBootID: boot, Leases: leases, Cgroups: groups, ManagedUIDTasks: tasks, LaunchTasks: launchTasks}
		return v.withNativeRuntimeOwners(ctx, records, groups, &inventory, nil, func(sessions []nativeRuntimeObservedSession) error {
			inventory.Unresolved = unresolvedNativeCgroups(groups, inventory.Owners)
			inventory.Unresolved = append(inventory.Unresolved, unresolvedNativeLaunchTasks(launchTasks, inventory.Owners)...)
			if err := v.recheckNativeRuntimeInventory(ctx, records, groups, tasks, launchTasks, sessions); err != nil {
				return err
			}
			if err := consume(ctx, cloneNativeRuntimeInventory(inventory)); err != nil {
				return err
			}
			return v.recheckNativeRuntimeInventory(ctx, records, groups, tasks, launchTasks, sessions)
		})
	})
}

// Iteration skips unresolved/nonresident records without growing the stack.
// Only observed live sessions remain nested, retaining every pidfd and original
// VM lock until the complete inventory consumer has finished.
func (v *JailerVMM) withNativeRuntimeOwners(ctx context.Context, records []nativeLaunchRecord, groups []NativeRuntimeCgroup, inventory *NativeRuntimeInventory, sessions []nativeRuntimeObservedSession, finish func([]nativeRuntimeObservedSession) error) error {
	for i, owner := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		result := newNativeRuntimeObservation(owner)
		if !owner.Authorized || owner.ResourcesRemoved || owner.ExitConfirmed {
			result.Unresolved = "native owner has no observable live task; absence still requires retirement evidence"
			inventory.Owners = append(inventory.Owners, result)
			continue
		}
		called := false
		err := v.withObservedNativeMachine(ctx, owner, func(s *nativeMachineAPI) error {
			called = true
			observed := nativeRuntimeObservedSession{api: s}
			if err := fillNativeRuntimeObservation(ctx, s, groups, &result); err != nil {
				result.Machine, result.Cgroup = nil, nil
				result.Unresolved = "original native task could not be independently observed"
			} else {
				observed.state, observed.machine, observed.measured = result.VMState, *result.Machine, true
			}
			sessions = append(sessions, observed)
			inventory.Owners = append(inventory.Owners, result)
			return v.withNativeRuntimeOwners(ctx, records[i+1:], groups, inventory, sessions, finish)
		})
		if called || ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		result.Unresolved = "original native task could not be independently observed"
		inventory.Owners = append(inventory.Owners, result)
	}
	return finish(sessions)
}

func newNativeRuntimeObservation(owner nativeLaunchRecord) NativeRuntimeObservation {
	return NativeRuntimeObservation{Instance: owner.Lease.Instance, Generation: owner.Generation, KernelBootID: owner.KernelBootID, Lease: owner.Lease,
		Placement: cloneRuntimePlacement(owner.RegionalRuntime), OriginalResources: cloneNativeRegionalResources(owner.RegionalResources), PID: owner.PID, StartTime: owner.StartTime,
		Authorized: owner.Authorized, Revoked: owner.Revoked, ExitConfirmed: owner.ExitConfirmed, ResourcesRemoved: owner.ResourcesRemoved}
}

func fillNativeRuntimeObservation(ctx context.Context, s *nativeMachineAPI, groups []NativeRuntimeCgroup, result *NativeRuntimeObservation) error {
	vmState, err := s.state(ctx)
	if err != nil {
		return err
	}
	machine, err := s.readShape(ctx, vmState)
	if err != nil {
		return err
	}
	result.VMState, result.Machine = vmState, &machine
	for _, group := range groups {
		if slices.Contains(group.PIDs, s.owner.PID) {
			if result.Cgroup != nil {
				return errors.New("native inventory: task appears in multiple cgroups")
			}
			copy := cloneNativeRuntimeCgroup(group)
			result.Cgroup = &copy
		}
	}
	result.Unresolved = nativeRuntimeCompatibility(s.owner, machine, result.Cgroup)
	return s.check(ctx, true)
}

func (v *JailerVMM) recheckNativeRuntimeInventory(ctx context.Context, records []nativeLaunchRecord, groups []NativeRuntimeCgroup, tasks []NativeRuntimeTask, launchTasks []NativeRuntimeLaunchTask, sessions []nativeRuntimeObservedSession) error {
	for _, s := range sessions {
		if err := s.api.check(ctx, s.measured); err != nil {
			return err
		}
		if !s.measured {
			continue
		}
		machine, err := s.api.readShape(ctx, s.state)
		if err != nil || machine != s.machine {
			return errors.Join(err, errors.New("native inventory: machine changed during observation"))
		}
	}
	after, err := v.nativeRecovery.runtimeGroups.Inventory(ctx)
	if err != nil || !sameNativeRuntimeCgroups(groups, after) {
		return errors.Join(err, errors.New("native inventory: cgroups changed during observation"))
	}
	current, err := v.observedNativeRuntimeRecords(ctx, sessions)
	if err != nil || !sameNativeObservedRecords(records, current) {
		return errors.Join(err, errors.New("native inventory: journal changed during observation"))
	}
	currentTasks, err := v.nativeRecovery.retirer.probe.managedUIDTasks(ctx)
	if err != nil || !slices.Equal(tasks, currentTasks) {
		return errors.Join(err, errors.New("native inventory: managed UID task set changed during observation"))
	}
	currentLaunchTasks, err := v.nativeRecovery.retirer.probe.launchTasks(ctx)
	if err != nil || !slices.Equal(launchTasks, currentLaunchTasks) {
		return errors.Join(err, errors.New("native inventory: launch task set changed during observation"))
	}
	return ctx.Err()
}

func (v *JailerVMM) observedNativeRuntimeRecords(ctx context.Context, sessions []nativeRuntimeObservedSession) ([]nativeLaunchRecord, error) {
	j := v.nativeRecovery.journal
	held := make(map[string]*nativeMachineAPI, len(sessions))
	for _, session := range sessions {
		held[session.api.owner.Lease.Instance] = session.api
	}
	return j.ownerRecords(ctx, func(instance string) (nativeLaunchRecord, error) {
		s := held[instance]
		if s == nil {
			return j.lockedRecord(ctx, instance)
		}
		if s.v != v || s.inventory == nil {
			return nativeLaunchRecord{}, errors.New("native inventory: original observation lock is required")
		}
		if err := s.check(ctx, false); err != nil {
			return nativeLaunchRecord{}, err
		}
		return j.read(instance)
	})
}

func cloneNativeRuntimeInventory(source NativeRuntimeInventory) NativeRuntimeInventory {
	copy := source
	copy.Leases, copy.Unresolved = slices.Clone(source.Leases), slices.Clone(source.Unresolved)
	copy.ManagedUIDTasks = slices.Clone(source.ManagedUIDTasks)
	copy.LaunchTasks = slices.Clone(source.LaunchTasks)
	copy.Cgroups = make([]NativeRuntimeCgroup, len(source.Cgroups))
	for i, group := range source.Cgroups {
		copy.Cgroups[i] = cloneNativeRuntimeCgroup(group)
	}
	copy.Owners = slices.Clone(source.Owners)
	for i := range copy.Owners {
		owner := &copy.Owners[i]
		owner.Placement, owner.OriginalResources = cloneRuntimePlacement(owner.Placement), cloneNativeRegionalResources(owner.OriginalResources)
		if owner.Machine != nil {
			machine := *owner.Machine
			owner.Machine = &machine
		}
		if owner.Cgroup != nil {
			group := cloneNativeRuntimeCgroup(*owner.Cgroup)
			owner.Cgroup = &group
		}
	}
	return copy
}

func nativeRuntimeCompatibility(owner nativeLaunchRecord, machine Machine, group *NativeRuntimeCgroup) string {
	if owner.RegionalResources == nil {
		return "original regional resource provenance is unavailable"
	}
	resources := owner.RegionalResources
	if machine.MemSizeMib != resources.RAMMB || machine.VcpuCount != resources.VCPU {
		return "actual machine differs from original regional footprint"
	}
	if group == nil || group.Device == 0 || group.Inode == 0 || group.MountID == 0 || group.Path != ParentCgroupFor(owner.Lease.Plan)+"/"+owner.Lease.Instance || !group.Populated || !slices.Equal(group.PIDs, []int{owner.PID}) {
		return "native cgroup ownership or occupants differ from the original task"
	}
	if group.MemoryMaxBytes == nil || *group.MemoryMaxBytes != uint64(api.BillableRAMMB(machine.MemSizeMib))<<20 {
		return "actual native memory ceiling differs from the original footprint"
	}
	cpu, err := nativeObservedMillicores(group.CPUQuotaUS, group.CPUPeriodUS)
	if err != nil || cpu > uint64(resources.CPUMillicores) || group.BurstUS != 0 {
		return "actual native CPU ceiling exceeds or lacks original bounded provenance"
	}
	return ""
}

func nativeObservedMillicores(quota *uint64, period uint64) (uint64, error) {
	if quota == nil || *quota == 0 || period == 0 || *quota > math.MaxUint64/1000 {
		return 0, errors.New("native inventory: CPU ceiling is unbounded or invalid")
	}
	value := *quota * 1000
	return value/period + boolUint64(value%period != 0), nil
}

func boolUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func cloneNativeRuntimeCgroup(group NativeRuntimeCgroup) NativeRuntimeCgroup {
	group.PIDs = slices.Clone(group.PIDs)
	if group.MemoryMaxBytes != nil {
		value := *group.MemoryMaxBytes
		group.MemoryMaxBytes = &value
	}
	if group.CPUQuotaUS != nil {
		value := *group.CPUQuotaUS
		group.CPUQuotaUS = &value
	}
	return group
}

func sameNativeRuntimeCgroups(a, b []NativeRuntimeCgroup) bool {
	return slices.EqualFunc(a, b, func(a, b NativeRuntimeCgroup) bool {
		return a.Path == b.Path && a.Device == b.Device && a.Inode == b.Inode && a.MountID == b.MountID && a.Populated == b.Populated && a.CPUPeriodUS == b.CPUPeriodUS && a.BurstUS == b.BurstUS && slices.Equal(a.PIDs, b.PIDs) && sameNativeLimit(a.MemoryMaxBytes, b.MemoryMaxBytes) && sameNativeLimit(a.CPUQuotaUS, b.CPUQuotaUS)
	})
}

func sameNativeLimit(a, b *uint64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sameNativeObservedRecords(a, b []nativeLaunchRecord) bool {
	return slices.EqualFunc(a, b, sameNativeObservedOwner)
}

func unresolvedNativeCgroups(groups []NativeRuntimeCgroup, owners []NativeRuntimeObservation) []string {
	var unknown []string
	for _, group := range groups {
		known := false
		for _, owner := range owners {
			scope := ParentCgroupFor(owner.Lease.Plan) + "/" + owner.Instance
			if group.Path == scope || strings.HasPrefix(group.Path, scope+"/") && len(group.PIDs) == 0 && !group.Populated {
				known = true
				break
			}
		}
		if !known {
			unknown = append(unknown, group.Path)
		}
	}
	return unknown
}

func parseNativeRuntimeLimit(raw string) (*uint64, error) {
	value := strings.TrimSpace(raw)
	if value == "max" {
		return nil, nil
	}
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return nil, errors.New("native inventory: invalid kernel limit")
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return &n, err
}

func parseNativeRuntimeCPU(raw string) (*uint64, uint64, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 {
		return nil, 0, errors.New("native inventory: invalid cpu.max")
	}
	quota, err := parseNativeRuntimeLimit(fields[0])
	period, periodErr := parseNativeRuntimeLimit(fields[1])
	if err != nil || periodErr != nil || period == nil || *period == 0 || quota != nil && *quota == 0 {
		return nil, 0, errors.Join(err, periodErr, errors.New("native inventory: invalid CPU quota or period"))
	}
	return quota, *period, nil
}

func parseNativeRuntimePIDs(raw string) ([]int, error) {
	var pids []int
	for _, value := range strings.Fields(raw) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 || pid > math.MaxInt32 || strings.Trim(value, "0123456789") != "" || slices.Contains(pids, pid) {
			return nil, fmt.Errorf("native inventory: invalid or changing cgroup PID list")
		}
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	return pids, nil
}
