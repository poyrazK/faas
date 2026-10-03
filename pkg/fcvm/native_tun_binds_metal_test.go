//go:build linux && metal

// adr: 521 — host TUN binding survives producer death with original cleanup authority.
package fcvm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

func TestMetalNativeTunBindRecovery(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires dedicated native KVM acceptance host")
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Fatal("native acceptance host has no TUN device:", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_TUN_ACCEPTANCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeTunBindRecovery$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_TUN_ACCEPTANCE_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated TUN acceptance: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	if root := os.Getenv("GREGALE_NATIVE_TUN_CRASH_ROOT"); root != "" {
		nativeMetalTunCrashChild(t, ctx, root)
		return
	}
	root, err := os.MkdirTemp("", "gregale-native-tun-")
	if err != nil {
		t.Fatal(err)
	}
	backend := newNativeTunBindBackend(root)
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), tunBinds: backend}
	tun := &nativeTunBindJournal{owner: j, backend: backend}
	lease := leaseForSlot("native-tun-crash", 0)
	lease.Plan = api.PlanHobby
	if err := j.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	jailRoot := filepath.Join(root, "firecracker", lease.Instance, "root")
	if err := os.MkdirAll(jailRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		retired, err := j.revoke(cleanupCtx, lease.Instance)
		if err == nil {
			err = j.confirmExit(cleanupCtx, retired)
		}
		if err == nil {
			retired, err = j.read(lease.Instance)
		}
		if err == nil {
			err = tun.retire(cleanupCtx, retired)
		}
		if err == nil {
			err = tun.require(retired, true)
		}
		if err != nil {
			t.Error("native TUN fixture cleanup:", err)
			return
		}
		mounts, err := nativeJailMounts(root)
		if err != nil || len(mounts) != 0 {
			t.Errorf("native fixture retains mounts: %v %v", mounts, err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	// The child shares this isolated original mount namespace; this is producer
	// crash evidence, not a production systemd namespace-restart acceptance.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativeTunBindRecovery$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "GREGALE_NATIVE_TUN_CRASH_ROOT="+root, "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("TUN producer crash: %v %s", err, out)
	}
	records, err := tun.records()
	if err != nil || len(records) != 1 {
		t.Fatalf("surviving TUN ownership: %+v %v", records, err)
	}
	record := records[0]
	if record.Ready || record.MountID != 0 || record.Placeholder.Inode == 0 {
		t.Fatal("child did not interrupt final binding publication")
	}
	point := filepath.Join(jailRoot, nativeTunTargetName)
	id, err := nativeImageMountID(point)
	if err != nil || id == 0 {
		t.Fatalf("real TUN bind did not survive: %d %v", id, err)
	}
	device, err := os.OpenFile(point, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	actual, statErr := nativeTunFileSource(device)
	if closeErr := device.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if statErr != nil || actual != record.Source {
		t.Fatalf("bind lost original device: %+v %v", actual, statErr)
	}
	for _, change := range []string{"device", "mount", "namespace"} {
		changed := record
		switch change {
		case "device":
			changed.Source.Identity.Inode++
		case "mount":
			changed.MountID = id + 1
		case "namespace":
			changed.Namespace.Inode++
		}
		if err := backend.Retire(changed); err == nil {
			t.Fatalf("changed %s authorized unmount", change)
		}
		if current, err := nativeImageMountID(point); err != nil || current != id {
			t.Fatal("refused retirement changed real mount:", err)
		}
	}
	owner, err = j.revoke(ctx, owner.Lease.Instance)
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
	if err := j.confirmResourcesRemoved(ctx, owner); err == nil {
		t.Fatal("unretired real TUN bind released ownership")
	}
	restartedOwner := &nativeLaunchJournal{root: j.root, tunBinds: backend}
	restarted := &nativeTunBindJournal{owner: restartedOwner, backend: backend}
	if err := restarted.inventory(ctx, []nativeLaunchRecord{owner}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.retire(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := restarted.require(owner, true); err != nil {
		t.Fatal(err)
	}
	if err := restartedOwner.confirmResourcesRemoved(ctx, owner); err != nil {
		t.Fatal(err)
	}
}

func nativeMetalTunCrashChild(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes")}
	owner, err := j.read("native-tun-crash")
	if err != nil {
		t.Fatal(err)
	}
	journal := &nativeTunBindJournal{owner: j, backend: newNativeTunBindBackend(root)}
	journal.writeValue = func(path string, record nativeTunBindRecord) error {
		if record.Ready {
			os.Exit(0)
		}
		return writeNativeJournalValue(path, record)
	}
	if err := journal.stage(ctx, owner, filepath.Join(root, "firecracker", owner.Lease.Instance, "root")); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash fixture did not interrupt TUN acknowledgement")
}
