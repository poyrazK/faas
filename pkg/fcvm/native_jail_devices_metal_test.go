//go:build linux && metal

// adr: 567 — native device handoff enters only the original retained namespace.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func TestMetalNativeJailDeviceHandoff(t *testing.T) {
	nativeMetalJailDeviceFixture(t, false)
}

func TestMetalNativeJailDeviceRecovery(t *testing.T) {
	nativeMetalJailDeviceFixture(t, true)
}

func nativeMetalJailDeviceFixture(t *testing.T, crash bool) {
	t.Helper()
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires dedicated native KVM acceptance host")
	}
	binary := os.Getenv("FAAS_TEST_VMMD_BINARY")
	if binary == "" {
		t.Skip("requires test-metal release-matched jail helper")
	}
	helper, err := resolveMountHelper(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_JAIL_ACCEPTANCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_JAIL_ACCEPTANCE_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated native device handoff: %v\n%s", err, out)
		} else {
			t.Logf("%s", out)
		}
		return
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	if root := os.Getenv("GREGALE_NATIVE_JAIL_CRASH_ROOT"); root != "" {
		nativeMetalJailCrashChild(t, ctx, root, helper)
		return
	}
	root, err := os.MkdirTemp("", "gregale-native-jail-")
	if err != nil {
		t.Fatal(err)
	}
	tunBackend := newNativeTunBindBackend(root)
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), tunBinds: tunBackend, jailDevices: newNativeJailDeviceBackend(root)}
	lease := leaseForSlot("native-jail-device", 0)
	lease.Plan = api.PlanHobby
	if err := j.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	jailRoot := filepath.Join(root, "firecracker", lease.Instance, "root")
	tun := &nativeTunBindJournal{owner: j, backend: tunBackend}
	var target *exec.Cmd
	helpers := &nativeHostHelperJournal{owner: j, groups: newNativeHostHelperGroups(), purpose: nativeHostHelperJailDevices, deviceRoot: jailRoot}
	targetJoined := false
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if target != nil && target.Process != nil && !targetJoined {
			_ = target.Process.Kill()
			_ = target.Wait()
		}
		retired, err := j.revoke(cleanup, lease.Instance)
		if err == nil {
			err = j.confirmExit(cleanup, retired)
		}
		if err == nil {
			retired, err = j.read(lease.Instance)
		}
		if err == nil {
			err = helpers.retireAll(cleanup, retired)
		}
		if err == nil {
			err = helpers.requireDeviceNamespacesRemoved(cleanup, retired)
		}
		if err == nil {
			err = tun.retire(cleanup, retired)
		}
		if err != nil {
			t.Error("native device fixture cleanup:", err)
			return
		}
		mounts, err := nativeJailMounts(root)
		if err != nil || len(mounts) != 0 {
			t.Errorf("fixture still retains mounts: %v %v", mounts, err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	if err := os.MkdirAll(filepath.Join(jailRoot, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(busybox)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jailRoot, "busybox"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tun.stage(ctx, owner, jailRoot); err != nil {
		t.Fatal(err)
	}
	// A static trusted target holds an original private namespace and chroot.
	// This fixture proves the host helper protocol, not guest serving readiness.
	target = exec.Command("/busybox", "sleep", "120")
	target.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS, Chroot: jailRoot}
	if err := target.Start(); err != nil {
		t.Fatal("acceptance busybox must be static:", err)
	}
	owner.Authorized, owner.PID = true, target.Process.Pid
	owner.StartTime, err = nativeHostHelperStartTime(owner.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.write(owner); err != nil {
		t.Fatal(err)
	}
	if crash {
		nativeMetalJailCrashRecovery(t, ctx, root, owner, helpers, helper)
	} else {
		command, err := newNativeJailDeviceCommand(helper)
		if err != nil {
			t.Fatal(err)
		}
		var output nativeHostHelperOutput
		command.Stdout, command.Stderr = &output, &output
		if err := helpers.run(ctx, owner, command, 10*time.Second); err != nil {
			t.Fatalf("native device handoff: %v %s", err, output.Bytes())
		}
		if err := helpers.confirmDeviceReceipt(ctx, owner, output.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := helpers.requireDeviceReady(owner); err != nil {
			t.Fatal(err)
		}
	}
	frames, err := helpers.records(owner)
	if err != nil || len(frames) != 1 || frames[0].JailDevice == nil {
		t.Fatalf("original setup frame: %+v %v", frames, err)
	}
	if err := j.jailDevices.NamespaceRemoved(ctx, frames[0].JailDevice.Scope); err == nil {
		t.Fatal("live original namespace was reported absent")
	}
	// Private /dev effects must not appear in the parent's jail mount table.
	if mounts, err := nativeJailMounts(jailRoot); err != nil || len(mounts) != 1 || mounts[0] != filepath.Join(jailRoot, nativeTunTargetName) {
		t.Fatalf("private setup escaped original namespace: %v %v", mounts, err)
	}
	if err := target.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	_ = target.Wait()
	targetJoined = true
	owner, err = j.revoke(ctx, lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.confirmExit(ctx, owner); err != nil {
		t.Fatal(err)
	}
	owner, err = j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := helpers.requireDeviceNamespacesRemoved(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := tun.retire(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := j.confirmResourcesRemoved(ctx, owner); err != nil {
		t.Fatal(err)
	}
}

func nativeMetalJailCrashChild(t *testing.T, ctx context.Context, root, helper string) {
	t.Helper()
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), tunBinds: newNativeTunBindBackend(root), jailDevices: newNativeJailDeviceBackend(root)}
	owner, err := j.read("native-jail-device")
	if err != nil {
		t.Fatal(err)
	}
	helpers := &nativeHostHelperJournal{owner: j, groups: newNativeHostHelperGroups(), purpose: nativeHostHelperJailDevices,
		deviceRoot: filepath.Join(root, "firecracker", owner.Lease.Instance, "root")}
	var output nativeHostHelperOutput
	helpers.writeValue = func(path string, frame nativeHostHelperRecord) error {
		if frame.Launch.ResourcesRemoved {
			var receipt jailsetup.DeviceSetupReceipt
			if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
				return err
			}
			if err := receipt.Validate(frame.JailDevice.Scope); err != nil {
				return err
			}
			// The helper has completed real device work and physically retired.
			// Kill this producer before acknowledging retirement or readiness.
			os.Exit(0)
		}
		return writeNativeJournalValue(path, frame)
	}
	command, err := newNativeJailDeviceCommand(helper)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = &output, &output
	if err := helpers.run(ctx, owner, command, 10*time.Second); err != nil {
		t.Fatalf("native device crash handoff: %v %s", err, output.Bytes())
	}
	t.Fatal("native producer did not reach interrupted retirement publication")
}

func nativeMetalJailCrashRecovery(t *testing.T, ctx context.Context, root string, owner nativeLaunchRecord, helpers *nativeHostHelperJournal, helper string) {
	t.Helper()
	// This producer shares the isolated host namespace; the VM's original
	// private namespace remains alive in the target held by this parent.
	producer := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.timeout=60s")
	producer.Env = append(os.Environ(), "GREGALE_NATIVE_JAIL_CRASH_ROOT="+root, "GORACE=atexit_sleep_ms=0")
	if out, err := producer.CombinedOutput(); err != nil {
		t.Fatalf("native device producer crash: %v %s", err, out)
	}
	frames, err := helpers.records(owner)
	if err != nil || len(frames) != 1 || frames[0].JailDevice == nil {
		t.Fatalf("surviving original device scope: %+v %v", frames, err)
	}
	frame := frames[0]
	if !frame.Launch.Authorized || frame.Launch.ResourcesRemoved || !frame.JailDevice.InputsClosed || frame.JailDevice.Receipt != nil || frame.JailDevice.Scope.ParentPID != producer.Process.Pid {
		t.Fatal("producer did not interrupt original device retirement publication")
	}
	if err := helpers.groups.Removed(frame.Group); err != nil {
		t.Fatal("physical producer retirement was not reached:", err)
	}
	if err := helpers.owner.jailDevices.InputsRemoved(ctx, frame.JailDevice.Scope); err != nil {
		t.Fatal("dead producer retained descriptors:", err)
	}
	if err := helpers.requireDeviceReady(owner); err == nil {
		t.Fatal("producer death supplied readiness")
	}
	retry, err := newNativeJailDeviceCommand(helper)
	if err != nil {
		t.Fatal(err)
	}
	if _, started, err := helpers.launch(ctx, owner, retry); err == nil || started {
		t.Fatal("interrupted setup authorized another producer")
	}
	if err := helpers.retireAll(ctx, owner); err != nil {
		t.Fatal("fresh journal failed original producer retirement:", err)
	}
	if err := helpers.requireDeviceReady(owner); err == nil {
		t.Fatal("producer retirement substituted for lost device receipt")
	}
	if err := helpers.requireDeviceNamespacesRemoved(ctx, owner); err == nil {
		t.Fatal("surviving private namespace released ownership")
	}
}
