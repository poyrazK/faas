//go:build linux || darwin

package fcvm

// adr: 780
import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func nativeHelperTaskRecord(owner nativeLaunchRecord, pid int, start uint64) nativeHostHelperRecord {
	id := uuid.NewString()
	return nativeHostHelperRecord{OwnerGeneration: owner.Generation, SessionID: pid, SessionPolicy: 1,
		Launch: nativeLaunchRecord{Version: 1, Generation: id, KernelBootID: owner.KernelBootID, Lease: owner.Lease, Authorized: true, PID: pid, StartTime: start},
		Group:  nativeHostHelperGroup{Path: "fixture-service/" + nativeHostHelperScope + "/" + id, Device: 17, Inode: 1}}
}

func nativeHelperTaskProc(t *testing.T, root string, record nativeHostHelperRecord, uid int, group string) string {
	t.Helper()
	dir := writeNativeRecoveryProcess(t, root, record.Launch.PID, record.Launch.Lease.Instance, uid, record.Launch.StartTime)
	// The recorded original task remains visible even when exec changed argv.
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/bin/sleep\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/"+group+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNativeHelperTasksObserveOriginalIncarnationsWithoutNameOrGroupFilters(t *testing.T) {
	_, owner, _ := nativeHelperJournalFixture(t)
	root := t.TempDir()
	first, escaped := nativeHelperTaskRecord(owner, 43, 101), nativeHelperTaskRecord(owner, 42, 102)
	nativeHelperTaskProc(t, root, first, 0, first.Group.Path)
	escaped.Launch.Revoked, escaped.Launch.ExitConfirmed, escaped.Launch.ResourcesRemoved = true, true, true
	dir := nativeHelperTaskProc(t, root, escaped, 1000, "other.slice")
	if err := os.Remove(filepath.Join(dir, "cmdline")); err != nil {
		t.Fatal(err)
	}
	probe := nativeProcessProbe{root: root}
	tasks, err := probe.helperTasks(t.Context(), []nativeHostHelperRecord{first, escaped})
	if err != nil || len(tasks) != 2 || tasks[0].PID != 42 || tasks[0].ObservedUID != 1000 || tasks[0].ObservedCgroup != "other.slice" || !tasks[0].RetirementClaimed || tasks[1].ExpectedCgroup != tasks[1].ObservedCgroup {
		t.Fatal(tasks, err)
	}
	if unknown := unresolvedNativeHelperTasks(tasks); len(unknown) != 2 {
		t.Fatal("helper effects became resolved", unknown)
	}
	clone := cloneNativeRuntimeInventory(NativeRuntimeInventory{HelperTasks: tasks})
	clone.HelperTasks[0].ObservedUID++
	if clone.HelperTasks[0] == tasks[0] {
		t.Fatal("consumer aliases helper observation")
	}
	// A reused numerical PID supplies no current task for the old generation.
	writeNativeRecoveryProcess(t, root, escaped.Launch.PID, owner.Lease.Instance, 1000, 103)
	tasks, err = probe.helperTasks(t.Context(), []nativeHostHelperRecord{escaped})
	if err != nil || len(tasks) != 0 {
		t.Fatal("reused PID claimed as original helper", tasks, err)
	}
}

func TestNativeHelperTasksRefuseUnstableOrUninspectableOriginalTask(t *testing.T) {
	for _, scenario := range []string{"uid", "cgroup", "start", "missing-status", "missing-stat", "missing-cgroup", "duplicate-uid", "permission", "root-replaced", "exit-between-scans", "duplicate-owner", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			_, owner, _ := nativeHelperJournalFixture(t)
			record := nativeHelperTaskRecord(owner, 42, 101)
			root := t.TempDir()
			dir := nativeHelperTaskProc(t, root, record, 0, record.Group.Path)
			calls := map[string]int{}
			ctx := t.Context()
			records := []nativeHostHelperRecord{record}
			if scenario == "duplicate-owner" {
				records = append(records, record)
			}
			if scenario == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			probe := nativeProcessProbe{root: root, readFile: func(path string) ([]byte, error) {
				name := filepath.Base(path)
				calls[name]++
				if scenario == "permission" {
					return nil, os.ErrPermission
				}
				if scenario == "missing-"+name {
					return nil, os.ErrNotExist
				}
				if scenario == "duplicate-uid" && name == "status" {
					return []byte("Uid: 0 0 0 0\nUid: 1 1 1 1\n"), nil
				}
				if scenario == "uid" && name == "status" && calls[name] == 2 {
					return []byte("Uid: 1 1 1 1\n"), nil
				}
				if scenario == "cgroup" && name == "cgroup" && calls[name] == 2 {
					return []byte("0::/escaped\n"), nil
				}
				if scenario == "start" && name == "stat" && calls[name] == 2 {
					writeNativeRecoveryProcess(t, root, 42, owner.Lease.Instance, 0, 102)
				}
				if scenario == "exit-between-scans" && name == "stat" && calls[name] == 3 {
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
					return nil, os.ErrNotExist
				}
				b, err := os.ReadFile(path)
				if scenario == "root-replaced" && name == "stat" && calls[name] == 4 {
					if err := os.Rename(root, root+"-old"); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.RemoveAll(root + "-old") })
					if err := os.Mkdir(root, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				return b, err
			}}
			if tasks, err := probe.helperTasks(ctx, records); err == nil || tasks != nil {
				t.Fatal("unstable helper scan accepted", tasks, err)
			}
		})
	}
}

func writeNativeHelperTaskFrame(t *testing.T, j *nativeHostHelperJournal, owner nativeLaunchRecord, frame nativeHostHelperRecord) {
	t.Helper()
	if err := os.MkdirAll(j.root(owner.Generation), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := j.write(owner, frame); err != nil {
		t.Fatal(err)
	}
}

func TestNativeResourceAcknowledgementRequiresIndependentHelperLeaderAbsence(t *testing.T) {
	for _, scenario := range []string{"alive", "missing-observer", "missing-status", "absent", "reused"} {
		t.Run(scenario, func(t *testing.T) {
			j, owner, _ := nativeHelperJournalFixture(t)
			frame := nativeHelperTaskRecord(owner, 42, 101)
			frame.Launch.Revoked, frame.Launch.ExitConfirmed, frame.Launch.ResourcesRemoved = true, true, true
			writeNativeHelperTaskFrame(t, j, owner, frame)
			root := t.TempDir()
			j.owner.helperProbe = &nativeProcessProbe{root: root}
			if scenario != "absent" {
				dir := nativeHelperTaskProc(t, root, frame, 0, "escaped")
				if scenario == "missing-status" {
					if err := os.Remove(filepath.Join(dir, "status")); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "reused" {
					writeNativeRecoveryProcess(t, root, 42, owner.Lease.Instance, 0, 102)
				}
			}
			if scenario == "missing-observer" {
				j.owner.helperProbe = nil
			}
			owner.Revoked, owner.ExitConfirmed = true, true
			if err := j.owner.write(owner); err != nil {
				t.Fatal(err)
			}
			err := j.owner.confirmResourcesRemoved(t.Context(), owner)
			want := scenario == "absent" || scenario == "reused"
			if (err == nil) != want {
				t.Fatal("incorrect helper absence acknowledgement", err)
			}
			after, readErr := j.owner.read(owner.Lease.Instance)
			if readErr != nil || after.ResourcesRemoved != want {
				t.Fatal("uncertain helper released ownership", after, readErr)
			}
		})
	}
}

func TestNativeRuntimeInventoryRechecksOriginalHelperTasksAndFramesAroundConsumer(t *testing.T) {
	for _, scenario := range []string{"stable", "uid", "cgroup", "exit", "frame", "new-frame"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, _, _, _ := nativeRuntimeFixture(t)
			r := v.nativeRecovery
			owner, err := r.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			j := &nativeHostHelperJournal{owner: r.journal, groups: newNativeHelperGroupsFixture()}
			r.helperGroups = j.groups
			frame := nativeHelperTaskRecord(owner, 43, 102)
			writeNativeHelperTaskFrame(t, j, owner, frame)
			dir := nativeHelperTaskProc(t, r.retirer.probe.root, frame, 0, "escaped")
			called := false
			err = v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, sample NativeRuntimeInventory) error {
				called = true
				if len(sample.HelperTasks) != 1 || len(sample.Unresolved) != 1 || len(sample.Owners) != 1 || sample.Owners[0].Unresolved == "" {
					t.Fatal("original helper hidden", sample)
				}
				original := sample.HelperTasks[0]
				sample.HelperTasks[0].ObservedUID++
				switch scenario {
				case "uid":
					if err := os.WriteFile(filepath.Join(dir, "status"), []byte("Uid: 1 1 1 1\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "cgroup":
					if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/different\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "exit":
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
				case "frame":
					frame.Launch.Revoked = true
					writeNativeHelperTaskFrame(t, j, owner, frame)
				case "new-frame":
					other := nativeHelperTaskRecord(owner, 44, 103)
					writeNativeHelperTaskFrame(t, j, owner, other)
				}
				if original.PID != 43 {
					t.Fatal("original helper mutated")
				}
				return nil
			})
			if !called || (err == nil) != (scenario == "stable") {
				t.Fatalf("helper changed without invalidating scoped inventory: %v", err)
			}
		})
	}
}

func TestNativeRecoveryRejectsAcknowledgedHelperOutsideItsRemovedCgroup(t *testing.T) {
	v, lease, _, _, _ := nativeRuntimeFixture(t)
	r := v.nativeRecovery
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	j := &nativeHostHelperJournal{owner: r.journal}
	frame := nativeHelperTaskRecord(owner, 43, 102)
	frame.Launch.Revoked, frame.Launch.ExitConfirmed, frame.Launch.ResourcesRemoved = true, true, true
	writeNativeHelperTaskFrame(t, j, owner, frame)
	nativeHelperTaskProc(t, r.retirer.probe.root, frame, 0, "escaped")
	r.helperGroups = newNativeHelperGroupsFixture()
	if leases, err := v.scanNativeRecoveryLeases(t.Context()); err == nil || leases != nil || !strings.Contains(err.Error(), "acknowledged helper") {
		t.Fatal("removed helper group authorized recovery", leases, err)
	}
}

func TestNativeHelperTaskWirePreservesAbsenceAndTransportsObservations(t *testing.T) {
	s := NativeRuntimeInventorySample{Version: 1, Host: runtimequota.HostEpoch{NodeID: uuid.NewString(), HostEpochID: uuid.NewString()}, Challenge: uuid.NewString(), Inventory: NativeRuntimeInventory{KernelBootID: uuid.NewString()}}
	before, err := EncodeNativeRuntimeInventorySample(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "HelperTasks") {
		t.Fatal("old wire representation changed")
	}
	s.Inventory.HelperTasks = []NativeRuntimeHelperTask{{Instance: "original", OwnerGeneration: uuid.NewString(), HelperGeneration: uuid.NewString(), PID: 42, StartTime: 101, ObservedCgroup: "escaped", ExpectedCgroup: "owned"}}
	raw, err := EncodeNativeRuntimeInventorySample(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeNativeRuntimeInventorySample(raw)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatal(got, err)
	}
}
