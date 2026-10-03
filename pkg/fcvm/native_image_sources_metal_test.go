//go:build linux && metal

// adr: 493 — surviving image grants recover through original inode and VM ownership.
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
)

func TestMetalNativeImageSourceRecovery(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires dedicated native KVM acceptance host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_IMAGE_ACCEPTANCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeImageSourceRecovery$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_IMAGE_ACCEPTANCE_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated image acceptance: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	if root := os.Getenv("GREGALE_NATIVE_IMAGE_CRASH_ROOT"); root != "" {
		nativeMetalImageCrashChild(t, ctx, root)
		return
	}
	// Preserve the tree on uncertain retirement; never recurse over a live mount.
	root, err := os.MkdirTemp("", "gregale-native-image-")
	if err != nil {
		t.Fatal(err)
	}
	backend := newNativeImageSourceBackend(root)
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), imageSources: backend}
	images := &nativeImageSourceJournal{owner: j, backend: backend}
	var owners []nativeLaunchRecord
	for i, name := range []string{"native-image-first", "native-image-second"} {
		lease := leaseForSlot(name, i)
		lease.Plan, lease.Networkless = api.PlanHobby, true
		if err := j.prepare(ctx, lease); err != nil {
			t.Fatal(err)
		}
		owner, err := j.read(name)
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, owner)
		if err := os.MkdirAll(filepath.Join(root, "firecracker", name, "root"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, expected := range owners {
			owner, err := j.revoke(cleanupCtx, expected.Lease.Instance)
			if err == nil {
				err = j.confirmExit(cleanupCtx, owner)
			}
			if err == nil {
				owner, err = j.read(expected.Lease.Instance)
			}
			if err == nil {
				err = images.retireAll(cleanupCtx, owner)
			}
			if err == nil {
				err = images.require(cleanupCtx, owner, true)
			}
			if err != nil {
				t.Error("native image fixture cleanup:", err)
				return
			}
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
	source := filepath.Join(root, "source.img")
	input, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := errors.Join(input.Truncate(4096), input.Sync()); err != nil {
		t.Fatal(err)
	}
	_, original, err := nativeImageFileMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(source, filepath.Join(root, "alias.img")); err != nil {
		t.Fatal(err)
	}
	firstRoot := filepath.Join(root, "firecracker", owners[0].Lease.Instance, "root")
	if _, err := images.stage(ctx, owners[0], firstRoot, source, "base", true, 0o044, true); err != nil {
		t.Fatal(err)
	}
	if err := images.require(ctx, owners[0], false); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativeImageSourceRecovery$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "GREGALE_NATIVE_IMAGE_CRASH_ROOT="+root, "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("image crash fixture: %v %s", err, out)
	}
	records, err := images.records()
	if err != nil || len(records) != 1 || len(records[0].References) != 2 {
		t.Fatalf("surviving shared frame=%+v %v", records, err)
	}
	ref := records[0].References[1]
	if ref.Ready || ref.Target.Inode == 0 {
		t.Fatal("child did not interrupt final binding publication")
	}
	point := filepath.Join(ref.Root, ref.Name)
	if id, err := nativeImageMountID(point); err != nil || id == 0 {
		t.Fatalf("crash binding did not survive: %d %v", id, err)
	}
	changed := records[0]
	changed.Identity.Inode++
	if err := backend.RetireReference(changed, ref); err == nil {
		t.Fatal("changed source identity authorized unmount")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "alias.img")); err != nil {
		t.Fatal(err)
	}
	for i, expected := range owners {
		owner, err := j.revoke(ctx, expected.Lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.confirmExit(ctx, owner); err != nil {
			t.Fatal(err)
		}
		owner, err = j.read(expected.Lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.confirmResourcesRemoved(ctx, owner); err == nil {
			t.Fatal("unretired source grant released VM ownership")
		}
		restartedOwner := &nativeLaunchJournal{root: j.root, imageSources: backend}
		restarted := &nativeImageSourceJournal{owner: restartedOwner, backend: backend}
		if err := restarted.retireAll(ctx, owner); err != nil {
			t.Fatal(err)
		}
		_, metadata, err := nativeImageFileMetadata(input)
		if err != nil {
			t.Fatal(err)
		}
		want := original
		if i == 0 {
			want.Mode |= 0o044
		}
		if metadata != want {
			t.Fatalf("retirement %d restored %+v, want %+v", i, metadata, want)
		}
		if err := j.confirmResourcesRemoved(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
}

func nativeMetalImageCrashChild(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes")}
	owner, err := j.read("native-image-second")
	if err != nil {
		t.Fatal(err)
	}
	images := &nativeImageSourceJournal{owner: j, backend: newNativeImageSourceBackend(root)}
	images.writeValue = func(path string, record nativeImageSourceRecord) error {
		last := record.References[len(record.References)-1]
		if last.Owner.Generation == owner.Generation && last.Ready {
			// Real bind and restrictive attributes have completed. No defer
			// or ready receipt runs, leaving the planned reference for recovery.
			os.Exit(0)
		}
		return writeNativeJournalValue(path, record)
	}
	secondRoot := filepath.Join(root, "firecracker", owner.Lease.Instance, "root")
	if _, err := images.stage(ctx, owner, secondRoot, filepath.Join(root, "alias.img"), "base", true, 0o044, false); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash fixture did not interrupt binding acknowledgement")
}
