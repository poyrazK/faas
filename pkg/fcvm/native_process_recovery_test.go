package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func writeNativeRecoveryProcess(t *testing.T, root string, pid int, id string, uid int, start uint64) string {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	fields := append([]string{"S"}, strings.Fields(strings.Repeat("0 ", 18))...)
	fields = append(fields, strconv.FormatUint(start, 10))
	files := map[string]string{
		"cmdline": "/usr/bin/firecracker-v1.7.0\x00--id\x00" + id + "\x00",
		"status":  fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid),
		"stat":    fmt.Sprintf("%d (firecracker worker)) %s", pid, strings.Join(fields, " ")),
		"cgroup":  "0::/" + ParentCgroupFor(api.PlanHobby) + "/" + id + "\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestNativeRecoveryDiscoveryKeepsExactIncarnationsAndDuplicateIDs(t *testing.T) {
	root := t.TempDir()
	id := "qualification-recovery"
	writeNativeRecoveryProcess(t, root, 41, id, JailUIDBase+3, 101)
	writeNativeRecoveryProcess(t, root, 42, id, JailUIDBase+3, 102)
	writeNativeRecoveryProcess(t, root, 43, id+"-other", JailUIDBase+4, 103)
	processes, err := (nativeProcessProbe{root: root}).processes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []nativeProcessIdentity{
		{PID: 41, Instance: id, UID: JailUIDBase + 3, ObservedUID: JailUIDBase + 3, StartTime: 101, Plan: api.PlanHobby},
		{PID: 42, Instance: id, UID: JailUIDBase + 3, ObservedUID: JailUIDBase + 3, StartTime: 102, Plan: api.PlanHobby},
		{PID: 43, Instance: id + "-other", UID: JailUIDBase + 4, ObservedUID: JailUIDBase + 4, StartTime: 103, Plan: api.PlanHobby},
	}
	if !reflect.DeepEqual(processes, want) || processes[0].lease().Slot != 3 {
		t.Fatalf("discovered=%+v, want=%+v", processes, want)
	}
}

func TestNativeRecoveryDiscoveryRejectsIncompleteOwnership(t *testing.T) {
	for _, corrupt := range []string{"duplicate_id", "trailing_id", "path_id", "uid_range", "mixed_uid", "foreign_cgroup", "wrong_pid", "missing_start", "missing_cmdline", "unreadable_cmdline"} {
		t.Run(corrupt, func(t *testing.T) {
			root := t.TempDir()
			dir := writeNativeRecoveryProcess(t, root, 42, "qualification-recovery", JailUIDBase+3, 101)
			probe := nativeProcessProbe{root: root}
			var name, value string
			switch corrupt {
			case "duplicate_id":
				name, value = "cmdline", "firecracker\x00--id\x00qualification-recovery\x00--id\x00other\x00"
			case "trailing_id":
				name, value = "cmdline", "firecracker\x00--id\x00qualification-recovery\x00--id"
			case "path_id":
				name, value = "cmdline", "firecracker\x00--id\x00../other\x00"
			case "uid_range":
				name, value = "status", "Uid:\t1000\t1000\t1000\t1000\n"
			case "mixed_uid":
				name, value = "status", "Uid:\t0\t20003\t0\t20003\n"
			case "foreign_cgroup":
				name, value = "cgroup", "0::/user.slice/qualification-recovery\n"
			case "wrong_pid":
				name, value = "stat", "43 (firecracker) S 0 0"
			case "missing_start":
				name, value = "stat", "42 (firecracker) S 0 0"
			case "missing_cmdline":
				if err := os.Remove(filepath.Join(dir, "cmdline")); err != nil {
					t.Fatal(err)
				}
			case "unreadable_cmdline":
				probe.readFile = func(path string) ([]byte, error) {
					if filepath.Base(path) == "cmdline" {
						return nil, os.ErrPermission
					}
					return os.ReadFile(path)
				}
			}
			if name != "" {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if processes, err := probe.processes(t.Context()); err == nil {
				t.Fatalf("incomplete ownership reported a trustworthy scan: %+v", processes)
			}
		})
	}
}

func TestNativeRecoveryDiscoveryRootJailerBeforePrivilegeDrop(t *testing.T) {
	root, jail := t.TempDir(), t.TempDir()
	dir := writeNativeRecoveryProcess(t, root, 42, "qualification-preexec", 0, 101)
	args := JailerCommand(JailerSpec{Instance: "qualification-preexec", UID: JailUIDBase + 4, GID: JailUIDBase + 4, Plan: api.PlanHobby, ExecFile: "/usr/bin/firecracker-v1.7.0"})
	for i, arg := range args {
		if arg == "--chroot-base-dir" {
			args[i+1] = jail
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	processes, err := (nativeProcessProbe{root: root, chrootBase: jail}).processes(t.Context())
	if err != nil || len(processes) != 1 || processes[0].UID != JailUIDBase+4 || !processes[0].Jailer || processes[0].ObservedUID != 0 {
		t.Fatalf("root jailer ownership: processes=%+v err=%v", processes, err)
	}
	if _, err := (nativeProcessProbe{root: root, chrootBase: jail + "-other"}).processes(t.Context()); err == nil {
		t.Fatal("root jailer with a foreign chroot was adopted")
	}
	args[0] = "/usr/bin/jailer-v1.7.0"
	helperArgs := append([]string{"vmmd-jail-helper", "--launch-jailer", "3", "--"}, args...)
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(helperArgs, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	processes, err = (nativeProcessProbe{root: root, chrootBase: jail}).processes(t.Context())
	if err != nil || len(processes) != 1 || !processes[0].Jailer || processes[0].UID != JailUIDBase+4 {
		t.Fatalf("helper before exec: processes=%+v err=%v", processes, err)
	}
}

func TestNativeRecoveryDiscoveryDoesNotCollapseFailedScanIntoAbsence(t *testing.T) {
	root := t.TempDir()
	if _, err := (nativeProcessProbe{root: filepath.Join(root, "missing")}).processes(t.Context()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing process root: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (nativeProcessProbe{root: root}).processes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled empty scan: %v", err)
	}
	writeNativeRecoveryProcess(t, root, 42, "qualification-recovery", JailUIDBase, 101)
	if _, err := (nativeProcessProbe{root: root}).processes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan: %v", err)
	}
	probe := nativeProcessProbe{root: root, readFile: func(path string) ([]byte, error) {
		if err := os.RemoveAll(filepath.Dir(path)); err != nil {
			return nil, err
		}
		return nil, os.ErrNotExist
	}}
	if processes, err := probe.processes(t.Context()); err != nil || len(processes) != 0 {
		t.Fatalf("process exiting during scan: processes=%+v err=%v", processes, err)
	}
	writeNativeRecoveryProcess(t, root, 42, "qualification-recovery", JailUIDBase, 101)
	probe.readFile = func(_ string) ([]byte, error) {
		if err := os.RemoveAll(root); err != nil {
			return nil, err
		}
		return nil, os.ErrNotExist
	}
	if _, err := probe.processes(t.Context()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root disappearing during scan reported absence: %v", err)
	}
}
