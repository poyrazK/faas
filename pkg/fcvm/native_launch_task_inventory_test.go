//go:build linux || darwin

package fcvm

// adr: 648
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func writeNativeInventoryRootLauncher(t *testing.T, probe nativeProcessProbe, pid int, instance string, uid int, start uint64, helper bool) string {
	t.Helper()
	dir := writeNativeRecoveryProcess(t, probe.root, pid, instance, 0, start)
	args := JailerCommand(JailerSpec{Instance: instance, UID: uid, GID: uid, Plan: api.PlanHobby, ExecFile: "/usr/bin/firecracker-v1.7.0"})
	for i, arg := range args {
		if arg == "--chroot-base-dir" {
			args[i+1] = probe.chrootBase
		}
	}
	args[0] = "/usr/bin/jailer-v1.7.0"
	if helper {
		args = append([]string{"/usr/bin/vmmd-jail-helper", "--launch-jailer", "3", "--"}, args...)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNativeLaunchTaskInventoryIncludesRootAndDuplicateInstanceTasks(t *testing.T) {
	probe := nativeProcessProbe{root: t.TempDir(), chrootBase: t.TempDir()}
	writeNativeInventoryRootLauncher(t, probe, 101, "same-instance", JailUIDBase+4, 201, true)
	writeNativeInventoryRootLauncher(t, probe, 9, "same-instance", JailUIDBase+4, 202, false)
	writeNativeRecoveryProcess(t, probe.root, 42, "same-instance", JailUIDBase+4, 203)
	tasks, err := probe.launchTasks(t.Context())
	if err != nil || len(tasks) != 3 {
		t.Fatal("root launcher hidden or duplicate identity collapsed", tasks, err)
	}
	if !slices.Equal([]int{tasks[0].PID, tasks[1].PID, tasks[2].PID}, []int{9, 42, 101}) {
		t.Fatal("inventory order depends on lexical process paths", tasks)
	}
	for _, task := range tasks {
		if task.Instance != "same-instance" || task.TargetUID != JailUIDBase+4 || task.Plan != api.PlanHobby || task.IsBuilder ||
			task.Jailer != (task.PID != 42) || task.ObservedUID != map[bool]int{true: 0, false: JailUIDBase + 4}[task.Jailer] {
			t.Fatal("original task scope changed", task)
		}
	}
}

func TestNativeLaunchTaskInventoryRejectsChangesBetweenScans(t *testing.T) {
	for _, scenario := range []string{"exit", "pid-reuse", "exec", "target-uid", "plan", "unreadable", "root-replacement", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			probe := nativeProcessProbe{root: t.TempDir(), chrootBase: t.TempDir()}
			dir := writeNativeInventoryRootLauncher(t, probe, 42, "root-launch", JailUIDBase+4, 101, true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			probe.readFile = func(path string) ([]byte, error) {
				if filepath.Base(path) == "cmdline" {
					reads++
					if reads == 2 {
						switch scenario {
						case "exit":
							if err := os.RemoveAll(dir); err != nil {
								t.Fatal(err)
							}
						case "pid-reuse":
							writeNativeInventoryRootLauncher(t, probe, 42, "root-launch", JailUIDBase+4, 102, true)
						case "exec":
							writeNativeRecoveryProcess(t, probe.root, 42, "root-launch", JailUIDBase+4, 101)
						case "target-uid":
							writeNativeInventoryRootLauncher(t, probe, 42, "root-launch", JailUIDBase+5, 101, true)
						case "plan":
							raw, err := os.ReadFile(path)
							if err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), ParentCgroupFor(api.PlanHobby), ParentCgroupFor(api.PlanPro))), 0o600); err != nil {
								t.Fatal(err)
							}
						case "unreadable":
							return nil, os.ErrPermission
						case "root-replacement":
							if err := os.Rename(probe.root, probe.root+"-original"); err != nil {
								t.Fatal(err)
							}
							writeNativeInventoryRootLauncher(t, probe, 42, "root-launch", JailUIDBase+4, 101, true)
						case "cancel":
							cancel()
						}
					}
				}
				return os.ReadFile(path)
			}
			if tasks, err := probe.launchTasks(ctx); err == nil || len(tasks) != 0 {
				t.Fatal("unstable task accepted as an observation", tasks, err)
			}
		})
	}
}

func TestNativeLaunchTaskInventoryRejectsNoncanonicalProcessDirectories(t *testing.T) {
	for _, pid := range []string{"0042", "2147483648"} {
		t.Run(pid, func(t *testing.T) {
			probe := nativeProcessProbe{root: t.TempDir()}
			if err := os.Mkdir(filepath.Join(probe.root, pid), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := probe.launchTasks(t.Context()); err == nil {
				t.Fatal("ambiguous process path accepted")
			}
		})
	}
}

func TestNativeRuntimeInventoryRetainsRootLauncherAsUnresolved(t *testing.T) {
	for _, helper := range []bool{false, true} {
		t.Run(map[bool]string{false: "jailer", true: "helper"}[helper], func(t *testing.T) {
			v, lease, _, _, _ := nativeRuntimeFixture(t)
			v.nativeRecovery.retirer.probe.chrootBase = v.chrootBase
			probe := v.nativeRecovery.retirer.probe
			writeNativeInventoryRootLauncher(t, probe, 43, lease.Instance, lease.UID, 102, helper)
			called := false
			err := v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, sample NativeRuntimeInventory) error {
				called = true
				if len(sample.LaunchTasks) != 2 || len(sample.ManagedUIDTasks) != 1 || len(sample.Unresolved) != 1 || sample.LaunchTasks[1].ObservedUID != 0 ||
					!strings.Contains(sample.Unresolved[0], "43") || sample.Owners[0].Unresolved != "" {
					t.Fatal("root launcher became a reconciled VM or disappeared", sample)
				}
				// Callback mutations cannot rewrite the retained task sample.
				sample.LaunchTasks[1].StartTime = 999
				return nil
			})
			if err != nil || !called {
				t.Fatal("stable unresolved launcher not observable", err)
			}
		})
	}
}

func TestNativeRuntimeInventoryRejectsRootLaunchersAroundConsumer(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		for _, scenario := range []string{"appeared", "exited", "pid-reuse", "exec", "target-uid"} {
			t.Run(phase+"/"+scenario, func(t *testing.T) {
				v, lease, _, handle, groups := nativeRuntimeFixture(t)
				v.nativeRecovery.retirer.probe.chrootBase = v.chrootBase
				probe := v.nativeRecovery.retirer.probe
				if scenario != "appeared" {
					writeNativeInventoryRootLauncher(t, probe, 43, lease.Instance, lease.UID, 102, true)
				}
				change := func() {
					switch scenario {
					case "appeared":
						writeNativeInventoryRootLauncher(t, probe, 43, lease.Instance, lease.UID, 102, true)
					case "exited":
						if err := os.RemoveAll(filepath.Join(probe.root, "43")); err != nil {
							t.Fatal(err)
						}
					case "pid-reuse":
						writeNativeInventoryRootLauncher(t, probe, 43, lease.Instance, lease.UID, 103, true)
					case "exec":
						writeNativeRecoveryProcess(t, probe.root, 43, lease.Instance, lease.UID, 102)
					case "target-uid":
						writeNativeInventoryRootLauncher(t, probe, 43, lease.Instance, lease.UID+1, 102, true)
					}
				}
				groups.hook = func(call int) {
					if phase == "before" && call == 2 {
						change()
					}
				}
				called := false
				err := v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, _ NativeRuntimeInventory) error {
					called = true
					if phase == "after" {
						change()
					}
					return nil
				})
				if err == nil || called != (phase == "after") || !handle.closed || len(handle.signals) != 0 || handle.waited {
					t.Fatal("unstable root launcher escaped observation or granted cleanup authority", called, err)
				}
			})
		}
	}
}

func TestNativeLaunchTaskInventoryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (nativeProcessProbe{root: t.TempDir()}).launchTasks(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation became an empty observation", err)
	}
}
