//go:build linux && metal

// adr: 567 — daemon death retains recoverable loop ownership before resource release.
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

func TestMetalNativeLoopMountRecovery(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires the dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires the dedicated native KVM acceptance host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_LOOP_ACCEPTANCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeLoopMountRecovery$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_LOOP_ACCEPTANCE_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated loop acceptance: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	if root := os.Getenv("GREGALE_NATIVE_LOOP_CRASH_ROOT"); root != "" {
		nativeMetalLoopCrashChild(t, ctx, root)
		return
	}
	// Never register TempDir's recursive cleanup over a possibly surviving
	// mount. Preserve the journal and fixture tree if retirement is uncertain.
	root, err := os.MkdirTemp("", "gregale-native-loop-")
	if err != nil {
		t.Fatal(err)
	}
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes")}
	l := leaseForSlot("native-loop-recovery", 0)
	l.Plan = api.PlanHobby
	l.Networkless = true
	if err := j.prepare(ctx, l); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(root, "drive.ext4")
	file, err := os.OpenFile(drive, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(64<<20), file.Close()); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "mkfs.ext4", "-q", "-F", drive).CombinedOutput(); err != nil {
		t.Fatalf("make private fixture image: %v %s", err, out)
	}
	loops := &nativeLoopMountJournal{owner: j, backend: newNativeLoopMountBackend()}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		current, err := j.revoke(cleanupCtx, l.Instance)
		if err == nil {
			err = j.confirmExit(cleanupCtx, current)
		}
		if err == nil {
			current, err = j.read(l.Instance)
		}
		if err == nil {
			err = loops.retireAll(cleanupCtx, current)
		}
		if err != nil {
			t.Error("native loop fixture cleanup:", err)
			return
		}
		if err := loops.requireRemoved(current); err != nil {
			t.Error("native loop fixture removal proof:", err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error("remove confirmed native loop fixture:", err)
		}
	})
	if err := loops.session(ctx, owner, drive, func(point string) error {
		return os.WriteFile(filepath.Join(point, "written-before-exit"), []byte("original"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := loops.requireRemoved(owner); err != nil {
		t.Fatal(err)
	}
	// The child exits from inside the writer, skipping every Go defer. Its
	// surviving mount must be recovered through durable frames alone.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativeLoopMountRecovery$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "GREGALE_NATIVE_LOOP_CRASH_ROOT="+root, "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash fixture: %v %s", err, out)
	}
	records, err := loops.records(owner)
	if err != nil || len(records) != 2 || records[0].Removed == records[1].Removed {
		t.Fatalf("crash mount frames=%+v %v", records, err)
	}
	var orphan nativeLoopMountRecord
	for _, record := range records {
		if !record.Removed {
			orphan = record
		}
	}
	entry, err := readNativeLoopMount(loops.point(orphan))
	if err != nil || entry == nil || entry.id != orphan.MountID {
		t.Fatalf("orphan mount=%+v %v", entry, err)
	}
	changed := orphan
	changed.Device.Source.Inode++
	if err := loops.backend.Retire(ctx, changed, loops.point(orphan)); err == nil {
		t.Fatal("changed source identity authorized unmount")
	}
	if entry, err := readNativeLoopMount(loops.point(orphan)); err != nil || entry == nil {
		t.Fatalf("refused retirement altered the original mount: %+v %v", entry, err)
	}
	owner, err = j.revoke(ctx, l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.confirmExit(ctx, owner); err != nil {
		t.Fatal(err)
	}
	owner, err = j.read(l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.confirmResourcesRemoved(ctx, owner); err == nil {
		t.Fatal("surviving loop mount released the prepared VM lease")
	}
	restarted := &nativeLoopMountJournal{owner: &nativeLaunchJournal{root: j.root}, backend: newNativeLoopMountBackend()}
	if err := restarted.retireAll(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := restarted.requireRemoved(owner); err != nil {
		t.Fatal(err)
	}
	if err := j.confirmResourcesRemoved(ctx, owner); err != nil {
		t.Fatal(err)
	}
}

func nativeMetalLoopCrashChild(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes")}
	owner, err := j.read("native-loop-recovery")
	if err != nil {
		t.Fatal(err)
	}
	loops := &nativeLoopMountJournal{owner: j, backend: newNativeLoopMountBackend()}
	if err := loops.session(ctx, owner, filepath.Join(root, "drive.ext4"), func(point string) error {
		if _, err := os.Stat(filepath.Join(point, "written-before-exit")); err != nil {
			return err
		}
		fd, err := unix.Open(point, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		if err := errors.Join(unix.Syncfs(fd), unix.Close(fd)); err != nil {
			return err
		}
		os.Exit(0)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash fixture did not exit inside the drive writer")
}
