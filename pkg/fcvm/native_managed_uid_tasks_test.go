package fcvm

// adr: 769
import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeRuntimeManagedUIDScanIsIndependentOfExecutableNames(t *testing.T) {
	root := t.TempDir()
	writeNativeRecoveryProcess(t, root, 42, "known", JailUIDBase+3, 101)
	unknown := writeNativeRecoveryProcess(t, root, 43, "unknown", JailUIDBase+4, 102)
	writeNativeRecoveryProcess(t, root, 44, "ordinary", 1000, 103)
	if err := os.WriteFile(filepath.Join(unknown, "cmdline"), []byte("/bin/sleep\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := nativeProcessProbe{root: root}
	tasks, err := probe.managedUIDTasks(t.Context())
	want := []NativeRuntimeTask{{PID: 42, UID: JailUIDBase + 3, StartTime: 101}, {PID: 43, UID: JailUIDBase + 4, StartTime: 102}}
	if err != nil || !reflect.DeepEqual(tasks, want) {
		t.Fatal("name filter hid a managed UID", tasks, err)
	}
	if err := probe.requireManagedUIDProvenance(t.Context()); err == nil {
		t.Fatal("unrecognized executable inherited native provenance")
	}
	if err := os.RemoveAll(unknown); err != nil {
		t.Fatal(err)
	}
	if err := probe.requireManagedUIDProvenance(t.Context()); err != nil {
		t.Fatal("known legacy owner rejected", err)
	}
}

func TestNativeRuntimeManagedUIDScanRefusesChangingIncarnation(t *testing.T) {
	for _, scenario := range []string{"uid", "start", "missing-status", "missing-stat", "unreadable", "root-gone", "root-replaced", "task-gone"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			dir := writeNativeRecoveryProcess(t, root, 42, "known", JailUIDBase+3, 101)
			calls := make(map[string]int)
			probe := nativeProcessProbe{root: root, readFile: func(path string) ([]byte, error) {
				name := filepath.Base(path)
				calls[name]++
				if scenario == "missing-status" && name == "status" {
					return nil, os.ErrNotExist
				}
				if scenario == "missing-stat" && name == "stat" {
					return nil, os.ErrNotExist
				}
				if scenario == "unreadable" {
					return nil, os.ErrPermission
				}
				if scenario == "task-gone" && name == "status" {
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
					return nil, os.ErrNotExist
				}
				if scenario == "uid" && name == "status" && calls[name] == 2 {
					return []byte(fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", JailUIDBase+4, JailUIDBase+4, JailUIDBase+4, JailUIDBase+4)), nil
				}
				if scenario == "start" && name == "stat" && calls[name] == 2 {
					writeNativeRecoveryProcess(t, root, 42, "known", JailUIDBase+3, 102)
				}
				data, err := os.ReadFile(path)
				if name == "stat" && calls[name] == 2 {
					if scenario == "root-gone" {
						if err := os.RemoveAll(root); err != nil {
							t.Fatal(err)
						}
					}
					if scenario == "root-replaced" {
						if err := os.Rename(root, root+"-old"); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.RemoveAll(root + "-old") })
						if err := os.Mkdir(root, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				}
				return data, err
			}}
			tasks, err := probe.managedUIDTasks(t.Context())
			if scenario == "task-gone" {
				if err != nil || len(tasks) != 0 {
					t.Fatal("fully disappeared task remained unknown", tasks, err)
				}
				return
			}
			if err == nil || tasks != nil {
				t.Fatal("changing task became a stable inventory", tasks, err)
			}
			if scenario == "unreadable" && !errors.Is(err, os.ErrPermission) {
				t.Fatal("read failure lost", err)
			}
		})
	}
}

func TestNativeRuntimeManagedUIDScanAllowsChangingStatusStatistics(t *testing.T) {
	root := t.TempDir()
	writeNativeRecoveryProcess(t, root, 42, "known", JailUIDBase+3, 101)
	count := 0
	probe := nativeProcessProbe{root: root, readFile: func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
		if filepath.Base(path) == "status" {
			count++
			data = append(data, []byte(fmt.Sprintf("voluntary_ctxt_switches:\t%d\n", count))...)
		}
		return data, err
	}}
	if tasks, err := probe.managedUIDTasks(t.Context()); err != nil || len(tasks) != 1 || count != 2 {
		t.Fatal("natural statistics confused UID provenance", tasks, err)
	}
}

func TestNativeRuntimeManagedUIDTupleDoesNotHidePartialJailIdentity(t *testing.T) {
	for _, test := range []struct {
		raw            string
		managed, valid bool
	}{
		{"Uid:\t1000\t0\t0\t1000\n", false, true},
		{"Uid:\t20003\t20003\t20003\t20003\n", true, true},
		{"Uid:\t1000\t20003\t0\t1000\n", true, false},
		{"Uid:\t20003\t0\t20003\t20003\n", true, false},
		{"Uid:\t0\t0\t0\t0\nUid:\t20003\t20003\t20003\t20003\n", false, false},
		{"Uid:\t0\t0\n", false, false},
		{"Uid:\t0\t0\t0\t4294967296\n", false, false},
		{"Name:\tprocess\n", false, false},
	} {
		_, managed, err := nativeManagedStatusUID(test.raw)
		if (err == nil) != test.valid || test.valid && managed != test.managed {
			t.Fatal("UID tuple hid a managed identity or rejected a host process", test, managed, err)
		}
	}
}
