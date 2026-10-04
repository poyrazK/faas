//go:build linux && metal

// adr: 532 — anonymous private inodes recover through original owned mounts.
package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

func TestMetalNativeWritableImageRecovery(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires dedicated native KVM acceptance host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_WRITABLE_ACCEPTANCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeWritableImageRecovery$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_WRITABLE_ACCEPTANCE_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated writable image acceptance: %v\n%s", err, out)
		} else {
			t.Logf("%s", out)
		}
		return
	}
	if root := os.Getenv("GREGALE_NATIVE_WRITABLE_CRASH_ROOT"); root != "" {
		nativeMetalWritableCrashChild(t, ctx, root, os.Getenv("GREGALE_NATIVE_WRITABLE_CRASH_PHASE"))
		return
	}
	// Preserve this owned tree on uncertain retirement; never remove a mount.
	root, err := os.MkdirTemp("", "gregale-native-writable-")
	if err != nil {
		t.Fatal(err)
	}
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, root)) {
		t.Fatal("native writable acceptance requires an ext4, XFS or Btrfs temporary disk directory")
	}
	backend := newNativeImageSourceBackend(root)
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), imageSources: backend}
	images := &nativeImageSourceJournal{owner: j, backend: backend}
	var owners []nativeLaunchRecord
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, expected := range owners {
			owner, err := j.revoke(cleanup, expected.Lease.Instance)
			if err == nil {
				err = j.confirmExit(cleanup, owner)
			}
			if err == nil {
				owner, err = j.read(expected.Lease.Instance)
			}
			if err == nil {
				err = images.retireAll(cleanup, owner)
			}
			if err == nil {
				err = images.require(cleanup, owner, true)
			}
			if err != nil {
				t.Error("native writable fixture cleanup:", err)
				return
			}
		}
		mounts, err := nativeJailMounts(root)
		if err != nil || len(mounts) != 0 {
			t.Errorf("native writable fixture retains mounts: %v %v", mounts, err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	source := filepath.Join(root, "immutable.img")
	input, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := input.Write([]byte("original-native-layer")); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(input.Truncate(4096), input.Sync()); err != nil {
		t.Fatal(err)
	}
	original, originalMetadata, err := nativeImageFileMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	for i, phase := range []string{"anchor", "binding"} {
		lease := leaseForSlot("native-writable-"+phase, i)
		lease.Plan, lease.Networkless = api.PlanHobby, true
		if err := j.prepare(ctx, lease); err != nil {
			t.Fatal(err)
		}
		owner, err := j.read(lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, owner)
		if err := os.MkdirAll(filepath.Join(root, "firecracker", lease.Instance, "root"), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativeWritableImageRecovery$", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_WRITABLE_CRASH_ROOT="+root, "GREGALE_NATIVE_WRITABLE_CRASH_PHASE="+phase, "GORACE=atexit_sleep_ms=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("writable %s crash producer: %v\n%s", phase, err, out)
		}
	}
	records, err := images.records()
	if err != nil || len(records) != 2 {
		t.Fatalf("surviving anonymous epochs: %+v %v", records, err)
	}
	if records[0].Identity == records[1].Identity {
		t.Fatal("two native writable copies shared an inode")
	}
	// Cache path replacement cannot redirect either retained private inode.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	restartedOwner := &nativeLaunchJournal{root: j.root, imageSources: backend}
	restarted := &nativeImageSourceJournal{owner: restartedOwner, backend: backend}
	if err := restarted.inventory(ctx, owners); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Identity == original || len(record.References) != 1 || record.References[0].Ready {
			t.Fatalf("crash promoted or aliased a native copy: %+v", record)
		}
		file, err := openNativeImageAnchor(record, restarted.anchor(record))
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		body := make([]byte, len("original-native-layer"))
		_, readErr := file.ReadAt(body, 0)
		statErr := unix.Fstat(int(file.Fd()), &stat)
		if err := errors.Join(readErr, statErr, file.Close()); err != nil {
			t.Fatal(err)
		}
		if string(body) != "original-native-layer" || stat.Nlink != 0 {
			t.Fatalf("retained clone was redirected or named: %q links=%d", body, stat.Nlink)
		}
	}
	identity, metadata, err := nativeImageFileMetadata(input)
	if err != nil || identity != original || metadata != originalMetadata {
		t.Fatalf("native copy mutated shared source: %+v %+v %v", identity, metadata, err)
	}
	for _, expected := range owners {
		owner, err := restartedOwner.revoke(ctx, expected.Lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		if err := restartedOwner.confirmExit(ctx, owner); err != nil {
			t.Fatal(err)
		}
		owner, err = restartedOwner.read(expected.Lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		if err := restartedOwner.confirmResourcesRemoved(ctx, owner); err == nil {
			t.Fatal("retained anonymous mount released native ownership")
		}
		if err := restarted.retireAll(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := restartedOwner.confirmResourcesRemoved(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	if mounts, err := nativeJailMounts(root); err != nil || len(mounts) != 0 {
		t.Fatalf("anonymous producer mounts leaked: %v %v", mounts, err)
	}
}

func nativeMetalWritableCrashChild(t *testing.T, ctx context.Context, root, phase string) {
	t.Helper()
	if phase != "anchor" && phase != "binding" {
		t.Fatal("unknown owned crash phase")
	}
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes")}
	owner, err := j.read("native-writable-" + phase)
	if err != nil {
		t.Fatal(err)
	}
	images := &nativeImageSourceJournal{owner: j, backend: newNativeImageSourceBackend(root)}
	images.writeValue = func(path string, record nativeImageSourceRecord) error {
		ref := record.References[len(record.References)-1]
		if phase == "anchor" && record.Ready && ref.Target.Inode == 0 || phase == "binding" && ref.Ready {
			// Kernel ownership exists; leave the last durable intent and exit
			// before its acknowledgement or any producer file defer executes.
			os.Exit(0)
		}
		return writeNativeJournalValue(path, record)
	}
	if _, err := images.stageWritable(ctx, owner, filepath.Join(root, "firecracker", owner.Lease.Instance, "root"), filepath.Join(root, "immutable.img"), layerImageName); err != nil {
		t.Fatal(err)
	}
	t.Fatal("native writable crash fixture did not interrupt its acknowledgement")
}
