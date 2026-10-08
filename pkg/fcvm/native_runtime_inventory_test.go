//go:build linux || darwin

package fcvm

// adr: 769
import (
	"context"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

type nativeRuntimeGroupsFixture struct {
	groups []NativeRuntimeCgroup
	calls  int
	hook   func(int)
	err    error
}

func (f *nativeRuntimeGroupsFixture) Inventory(ctx context.Context) ([]NativeRuntimeCgroup, error) {
	f.calls++
	if f.hook != nil {
		f.hook(f.calls)
	}
	if err := errors.Join(f.err, ctx.Err()); err != nil {
		return nil, err
	}
	groups := make([]NativeRuntimeCgroup, len(f.groups))
	for i, group := range f.groups {
		groups[i] = cloneNativeRuntimeCgroup(group)
	}
	return groups, nil
}

func nativeRuntimeFixture(t *testing.T) (*JailerVMM, Lease, *nativeMachineFixture, *nativeMachineHandleFixture, *nativeRuntimeGroupsFixture) {
	t.Helper()
	v, lease, machine, handle := nativeMachineAPIFixture(t)
	machine.mu.Lock()
	machine.state = "Paused"
	machine.mu.Unlock()
	memory, cpu := uint64(api.BillableRAMMB(lease.MemoryMaxMiB))<<20, uint64(25000)
	groups := &nativeRuntimeGroupsFixture{groups: []NativeRuntimeCgroup{{Path: ParentCgroupFor(lease.Plan) + "/" + lease.Instance, Device: 1, Inode: 2, MountID: 3, MemoryMaxBytes: &memory, CPUQuotaUS: &cpu, CPUPeriodUS: 100000, PIDs: []int{42}, Populated: true}}}
	r := v.nativeRecovery
	r.runtimeGroups, r.support = groups, func() error { return nil }
	r.resources = func(Lease, netns.Config) error { return nil }
	r.inventory = func([]Lease) error { return nil }
	return v, lease, machine, handle, groups
}

func TestNativeRuntimeObservationReadsRecoveredFencedAndHistoricalOwners(t *testing.T) {
	for _, scenario := range []string{"recovered", "fenced", "historical"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, _, handle, _ := nativeRuntimeFixture(t)
			r := v.nativeRecovery
			owner, err := r.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			delete(r.owned, lease.Instance)
			if scenario == "fenced" {
				owner.Revoked = true
			}
			if scenario == "historical" {
				if err := os.Remove(r.journal.executablePath(owner)); err != nil {
					t.Fatal(err)
				}
				owner.Version, owner.RegionalResources = 2, nil
			}
			if err := r.journal.write(owner); err != nil {
				t.Fatal(err)
			}
			called := false
			err = v.withNativeInventory(t.Context(), func(ctx context.Context) error {
				return v.withObservedNativeMachine(ctx, owner, func(s *nativeMachineAPI) error {
					called = true
					shape, err := s.readShape(ctx, "Paused")
					if err != nil || shape.MemSizeMib != lease.MemoryMaxMiB || handle.closed {
						t.Fatalf("unmeasured original task: %+v %v", shape, err)
					}
					for _, path := range []string{"/vm", "/snapshot/load", "/machine-config", "/"} {
						if _, err := s.request(ctx, http.MethodPatch, path, nil, http.StatusNoContent); !errors.Is(err, errNativeInventoryMutation) {
							t.Fatalf("write permitted: %s %v", path, err)
						}
					}
					if _, err := s.request(ctx, http.MethodGet, "/drives", nil, http.StatusOK); !errors.Is(err, errNativeInventoryMutation) {
						t.Fatal("unqualified read allowed", err)
					}
					if err := s.resume(ctx); !errors.Is(err, errNativeInventoryMutation) {
						t.Fatal("resume permitted", err)
					}
					if err := v.withNativeMachineAPI(ctx, lease, func(*nativeMachineAPI) error { t.Fatal("producer consumed inventory context"); return nil }); !errors.Is(err, errNativeInventoryMutation) {
						t.Fatal(err)
					}
					return nil
				})
			})
			if err != nil || !called || !handle.closed || r.generation(lease.Instance) != "" {
				t.Fatal("observation invented authority or leaked handle", err)
			}
		})
	}
}

func TestNativeRuntimeObservationRequiresActiveOriginalInventory(t *testing.T) {
	for _, scenario := range []string{"absent", "producer", "expired", "foreign", "cancelled", "changed-owner", "lost-daemon"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, _, _, _ := nativeRuntimeFixture(t)
			owner, err := v.nativeRecovery.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			var finish func()
			switch scenario {
			case "producer":
				ctx, finish, err = v.beginNativeEffects(ctx)
			case "foreign":
				other, _, _, _, _ := nativeRuntimeFixture(t)
				ctx, finish, err = other.beginNativeInventory(ctx)
			case "expired", "cancelled", "changed-owner", "lost-daemon":
				ctx, finish, err = v.beginNativeInventory(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if finish != nil {
				defer finish()
			}
			if scenario == "expired" {
				finish()
			}
			if scenario == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "changed-owner" {
				owner.StartTime++
			}
			if scenario == "lost-daemon" {
				if err := v.nativeRecovery.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := v.withObservedNativeMachine(ctx, owner, func(*nativeMachineAPI) error { t.Fatal("invalid observer reached consumer"); return nil }); err == nil {
				t.Fatal("invalid observation context accepted")
			}
		})
	}
}

func TestNativeRuntimeInventoryIsFreshAfterCachedRecovery(t *testing.T) {
	v, lease, machine, handle, groups := nativeRuntimeFixture(t)
	delete(v.nativeRecovery.owned, lease.Instance)
	manager := &Manager{vmm: v, nativeRecoveryReady: true}
	for iteration := 0; iteration < 2; iteration++ {
		handle.closed = false // A new modeled pidfd for this independent observation.
		if iteration == 1 {
			machine.mu.Lock()
			machine.machine.VcpuCount++
			machine.mu.Unlock()
		}
		called := false
		deadline, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		err := manager.WithNativeRuntimeInventory(deadline, func(ctx context.Context, inventory NativeRuntimeInventory) error {
			called = true
			if len(inventory.Owners) != 1 || len(inventory.Cgroups) != 1 || len(inventory.ManagedUIDTasks) != 1 || len(inventory.Leases) < 1 || handle.closed {
				t.Fatalf("incomplete or unpinned observation: %+v", inventory)
			}
			observed := inventory.Owners[0]
			if observed.Machine == nil || observed.OriginalResources == nil || observed.Cgroup == nil || (observed.Unresolved == "") != (iteration == 0) {
				t.Fatalf("requested resources replaced actual observation: %+v", observed)
			}
			if iteration == 1 && observed.Machine.VcpuCount == observed.OriginalResources.VCPU {
				t.Fatal("machine drift hidden")
			}
			if observed.OriginalResources.CPUMillicores <= int(*observed.Cgroup.CPUQuotaUS*1000/observed.Cgroup.CPUPeriodUS) {
				t.Fatal("fixture must retain an original peak above current quota")
			}
			// Mutation of an advisory copy cannot change the retained kernel/journal sample.
			inventory.Owners[0].OriginalResources.VCPU = 999
			inventory.Owners[0].Machine.VcpuCount = 999
			*inventory.Owners[0].Cgroup.MemoryMaxBytes = 0
			*inventory.Cgroups[0].CPUQuotaUS = 0
			inventory.ManagedUIDTasks[0].StartTime = 0
			if _, _, err := v.beginNativeEffects(ctx); !errors.Is(err, errNativeInventoryMutation) {
				t.Fatal("inventory gained producer authority", err)
			}
			wait, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
			defer cancel()
			lock, err := v.nativeRecovery.journal.lock(wait, lease.Instance)
			if lock != nil {
				_ = lock.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("original per-VM lock not held through consumer", err)
			}
			return nil
		})
		if err != nil || !called || !handle.closed || groups.calls != (iteration+1)*3 || manager.LiveCount() != 0 || v.nativeRecovery.generation(lease.Instance) != "" {
			t.Fatal("cached recovery replaced a fresh read-only scan", err, groups.calls)
		}
	}
}

func TestNativeRuntimeInventoryRejectsChangingSample(t *testing.T) {
	for _, scenario := range []string{"cgroup-before", "cgroup-after", "machine-after", "uid-after", "owner-after", "exit-after", "cancel-before", "cancel-after", "backend", "consumer"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, machine, handle, groups := nativeRuntimeFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("consumer refused sample")
			if scenario == "backend" {
				groups.err = cause
			}
			groups.hook = func(call int) {
				if call != 2 {
					return
				}
				if scenario == "cgroup-before" {
					groups.groups[0].Inode++
				}
				if scenario == "cancel-before" {
					cancel()
				}
			}
			called := false
			err := v.withNativeRuntimeInventory(ctx, func(_ context.Context, _ NativeRuntimeInventory) error {
				called = true
				switch scenario {
				case "cgroup-after":
					groups.groups[0].MemoryMaxBytes = nil
				case "machine-after":
					machine.mu.Lock()
					machine.machine.MemSizeMib++
					machine.mu.Unlock()
				case "uid-after":
					writeNativeRecoveryProcess(t, v.nativeRecovery.retirer.probe.root, 43, "foreign", lease.UID+1, 102)
				case "owner-after":
					owner, err := v.nativeRecovery.journal.read(lease.Instance)
					if err != nil {
						return err
					}
					owner.Revoked = true
					return v.nativeRecovery.journal.write(owner)
				case "exit-after":
					handle.setLiveError(os.ErrProcessDone)
				case "cancel-after":
					cancel()
				case "consumer":
					return cause
				}
				return nil
			})
			before := scenario == "cgroup-before" || scenario == "cancel-before" || scenario == "backend"
			if err == nil || called == before {
				t.Fatalf("changing sample reached or escaped callback: called=%v err=%v", called, err)
			}
			if scenario == "consumer" && !errors.Is(err, cause) {
				t.Fatal("consumer error lost", err)
			}
			if !before && !handle.closed {
				t.Fatal("pin leaked after failed observation")
			}
		})
	}
}

func TestNativeRuntimeInventoryUnknownOwnersRemainUnresolved(t *testing.T) {
	for _, scenario := range []string{"missing-machine", "foreign-pid", "unbounded-memory", "widened-memory", "unbounded-cpu", "widened-cpu", "burst", "mount", "extra-occupant", "orphan", "populated-descendant", "empty-descendant", "duplicate-task", "historical"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, machine, _, groups := nativeRuntimeFixture(t)
			group := &groups.groups[0]
			switch scenario {
			case "missing-machine":
				machine.mu.Lock()
				machine.raw["GET /machine-config"] = `{}`
				machine.mu.Unlock()
			case "foreign-pid":
				group.PIDs = []int{43}
			case "unbounded-memory":
				group.MemoryMaxBytes = nil
			case "widened-memory":
				*group.MemoryMaxBytes++
			case "unbounded-cpu":
				group.CPUQuotaUS = nil
			case "widened-cpu":
				*group.CPUQuotaUS = 1000000
			case "burst":
				group.BurstUS = 1
			case "mount":
				group.MountID = 0
			case "extra-occupant":
				group.PIDs = []int{42, 43}
			case "orphan":
				group.Path += "-foreign"
			case "populated-descendant", "empty-descendant", "duplicate-task":
				child := cloneNativeRuntimeCgroup(*group)
				child.Path += "/child"
				child.Inode++
				if scenario != "duplicate-task" {
					child.PIDs = nil
				}
				child.Populated = scenario != "empty-descendant"
				groups.groups = append(groups.groups, child)
			case "historical":
				owner, err := v.nativeRecovery.journal.read(lease.Instance)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(v.nativeRecovery.journal.executablePath(owner)); err != nil {
					t.Fatal(err)
				}
				owner.Version, owner.RegionalResources = 2, nil
				if err := v.nativeRecovery.journal.write(owner); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			err := v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, sample NativeRuntimeInventory) error {
				called = true
				if len(sample.Owners) != 1 || (sample.Owners[0].Unresolved == "") != (scenario == "populated-descendant" || scenario == "empty-descendant") {
					t.Fatalf("unknown ownership became reconciled: %+v", sample.Owners)
				}
				if (len(sample.Unresolved) != 0) != (scenario == "orphan" || scenario == "populated-descendant" || scenario == "duplicate-task") {
					t.Fatalf("unknown cgroup hidden: %v", sample.Unresolved)
				}
				return nil
			})
			if err != nil || !called {
				t.Fatal("advisory unknown sample unavailable", err)
			}
		})
	}
}

func TestNativeRuntimeLimitParsingRejectsAmbiguousKernelValues(t *testing.T) {
	for _, raw := range []string{"", "+1", "-1", "1.5", "1 2", "Max", "18446744073709551616"} {
		if _, err := parseNativeRuntimeLimit(raw); err == nil {
			t.Fatalf("invalid limit accepted: %q", raw)
		}
	}
	for _, raw := range []string{"max", "0", "18446744073709551615\n"} {
		if _, err := parseNativeRuntimeLimit(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"max", "0 100000", "1 0", "1 max", "+1 100", "1 2 3"} {
		if _, _, err := parseNativeRuntimeCPU(raw); err == nil {
			t.Fatalf("invalid CPU accepted: %q", raw)
		}
	}
	quota, period, err := parseNativeRuntimeCPU("max 100000\n")
	if err != nil || quota != nil || period != 100000 {
		t.Fatal("unbounded CPU hidden", err)
	}
	for _, raw := range []string{"0", "-1", "+1", "42 42", "2147483648", "1.0"} {
		if _, err := parseNativeRuntimePIDs(raw); err == nil {
			t.Fatalf("changing PID list accepted: %q", raw)
		}
	}
	if pids, err := parseNativeRuntimePIDs("43\n42\n"); err != nil || !slices.Equal(pids, []int{42, 43}) {
		t.Fatal("kernel PIDs not normalized", pids, err)
	}
	for _, test := range []struct {
		quota, period, want uint64
		valid               bool
	}{{1, 3, 334, true}, {100000, 100000, 1000, true}, {math.MaxUint64, 1, 0, false}, {0, 1, 0, false}, {1, 0, 0, false}} {
		value, err := nativeObservedMillicores(&test.quota, test.period)
		if (err == nil) != test.valid || test.valid && value != test.want {
			t.Fatal("CPU ceiling rounded down or overflowed", test, value, err)
		}
	}
}

func TestNativeRuntimeInventoryRequiresIndependentCapabilities(t *testing.T) {
	v, _, _, _, _ := nativeRuntimeFixture(t)
	v.nativeRecovery.runtimeGroups = nil
	manager := &Manager{vmm: v, nativeRecoveryReady: true}
	if err := manager.WithNativeRuntimeInventory(t.Context(), func(context.Context, NativeRuntimeInventory) error { t.Fatal("partial inventory consumed"); return nil }); err == nil {
		t.Fatal("missing kernel capability accepted")
	}
	if err := (&Manager{vmm: &fakeVMM{}}).WithNativeRuntimeInventory(t.Context(), func(context.Context, NativeRuntimeInventory) error { t.Fatal("legacy inventory consumed"); return nil }); err == nil {
		t.Fatal("legacy path fabricated native observation")
	}
	if err := manager.WithNativeRuntimeInventory(t.Context(), nil); err == nil {
		t.Fatal("nil consumer accepted")
	}
}

func TestNativeRuntimeInventoryUnobservedOwnerGrantsNoAbsenceEvidence(t *testing.T) {
	v, _, _, _, _ := nativeRuntimeFixture(t)
	lease := leaseForSlot("unstarted-owner", 4)
	lease.Plan = api.PlanHobby
	if err := v.nativeRecovery.journal.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := v.withNativeRuntimeInventory(deadline, func(_ context.Context, sample NativeRuntimeInventory) error {
		if len(sample.Owners) != 2 || !slices.Contains(sample.Leases, lease) {
			t.Fatalf("unobserved lease disappeared: %+v", sample)
		}
		found := false
		for _, owner := range sample.Owners {
			if owner.Instance == lease.Instance {
				found = true
				if owner.Authorized || owner.ExitConfirmed || owner.ResourcesRemoved || owner.Unresolved == "" || owner.Machine != nil {
					t.Fatalf("unstarted task invented absence evidence: %+v", owner)
				}
			}
		}
		if !found {
			t.Fatal("unobserved owner omitted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeRuntimeInventoryRejectsUnrecognizedManagedUID(t *testing.T) {
	v, lease, _, _, _ := nativeRuntimeFixture(t)
	dir := writeNativeRecoveryProcess(t, v.nativeRecovery.retirer.probe.root, 43, "escaped", lease.UID+1, 102)
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/bin/sleep\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{vmm: v, nativeRecoveryReady: true}
	if err := manager.WithNativeRuntimeInventory(t.Context(), func(context.Context, NativeRuntimeInventory) error {
		t.Fatal("foreign jail UID hidden from inventory")
		return nil
	}); err == nil {
		t.Fatal("unknown producer accepted")
	}
}
