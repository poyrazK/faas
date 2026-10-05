//go:build linux && metal

// adr: 568 — a tmpfs jail cannot own a persistent disk name through reboot.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// Real mounts and producer process death exercise the production filesystem
// layout. These are ownership fixtures, not Firecracker capture/restore proof.
func TestMetalNativeDiskImageStagingRecovery(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires x86_64 Linux acceptance host and root")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_DISK_STAGING_ISOLATED") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeDiskImageStagingRecovery$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_DISK_STAGING_ISOLATED=1")
		if body, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated disk staging acceptance: %v\n%s", err, body)
		} else {
			t.Logf("%s", body)
		}
		return
	}
	if base := os.Getenv("GREGALE_NATIVE_DISK_STAGING_BASE"); base != "" {
		nativeMetalDiskStagingCrashChild(t, ctx, base)
		return
	}
	disk, err := os.MkdirTemp("", "gregale-native-disk-staging-")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(disk, "jail")
	claims := filepath.Join(disk, "claims")
	for _, root := range []string{base, claims} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mount("tmpfs", base, "tmpfs", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0700,size=8m"); err != nil {
		t.Fatal(err)
	}
	backend := linuxNativeImageSources{base: base, diskStagingRoot: claims}
	j := &nativeLaunchJournal{root: filepath.Join(base, ".native-processes"), imageSources: backend}
	images := nativeImageSourceJournal{owner: j, backend: backend}
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
				t.Error("persistent disk staging cleanup:", err)
				return
			}
		}
		mounts, err := nativeJailMounts(base)
		if err != nil {
			t.Error(err)
			return
		}
		for _, point := range mounts {
			if point != base { // The fixture's own tmpfs is retired below.
				t.Error("persistent disk fixture retains a child mount", point)
				return
			}
		}
		if err := unix.Unmount(base, 0); err != nil {
			t.Error(err)
			return
		}
		if err := os.RemoveAll(disk); err != nil {
			t.Error(err)
		}
	})
	lock, err := backend.LockDiskStaging(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	source := filepath.Join(disk, "immutable.img")
	if err := os.WriteFile(source, []byte("original-native-disk-layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"drive", "output"} {
		for _, phase := range []string{"source", "anchor", "binding"} {
			instance := fmt.Sprintf("disk-%s-%s", kind, phase)
			lease := leaseForSlot(instance, len(owners))
			lease.Plan, lease.Networkless = api.PlanHobby, true
			if err := j.prepare(ctx, lease); err != nil {
				t.Fatal(err)
			}
			owner, err := j.read(instance)
			if err != nil {
				t.Fatal(err)
			}
			owners = append(owners, owner)
			if err := os.MkdirAll(filepath.Join(base, "firecracker", instance, "root"), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativeDiskImageStagingRecovery$", "-test.timeout=60s")
			cmd.Env = append(os.Environ(), "GREGALE_NATIVE_DISK_STAGING_BASE="+base, "GREGALE_NATIVE_DISK_STAGING_CLAIMS="+claims,
				"GREGALE_NATIVE_DISK_STAGING_SOURCE="+source, "GREGALE_NATIVE_DISK_STAGING_INSTANCE="+instance,
				"GREGALE_NATIVE_DISK_STAGING_KIND="+kind, "GREGALE_NATIVE_DISK_STAGING_PHASE="+phase, "GORACE=atexit_sleep_ms=0")
			if body, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("disk %s %s producer crash: %v\n%s", kind, phase, err, body)
			}
			if !cmd.ProcessState.Exited() {
				t.Fatal("original disk staging producer exit was not joined")
			}
		}
	}
	records, err := images.records()
	if err != nil || len(records) != 6 {
		t.Fatal("disk producer death lost original image epochs", len(records), err)
	}
	if err := images.inventory(ctx, owners); err != nil {
		t.Fatal("restart failed to inventory tmpfs/disk owned sources", err)
	}
	var jail unix.Stat_t
	if err := unix.Stat(base, &jail); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		claim, err := readNativeDiskImageClaim(claims, record.Epoch)
		if err != nil || !sameNativeDiskImageSource(claim, record) || record.Identity.Device == uint64(jail.Dev) {
			t.Fatal("tmpfs producer lacked its persistent original disk claim", err)
		}
		if err := inspectNativeDiskImageSource(claims, claim); err != nil {
			t.Fatal(err)
		}
		if record.References[0].Owner.Lease.Instance == "disk-drive-source" {
			nativeMetalDiskStagingRejectEarlyPermissions(t, ctx, &images, owners, claims, record)
		}
	}
	for _, expected := range owners {
		owner := retireNativeImageFixtureOwner(t, &images, expected)
		if err := j.confirmResourcesRemoved(ctx, owner); err == nil {
			t.Fatal("persistent source released original physical ownership before retirement")
		}
		if err := images.retireAll(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := j.confirmResourcesRemoved(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	if entries, err := os.ReadDir(claims); err != nil || len(entries) != 1 || entries[0].Name() != ".daemon-owner.lock" {
		t.Fatal("original retirement retained a disk name or claim", entries, err)
	}
	body, err := os.ReadFile(source)
	if err != nil || string(body) != "original-native-disk-layer" {
		t.Fatal("private disk staging mutated the shared source", err)
	}
}

func nativeMetalDiskStagingRejectEarlyPermissions(t *testing.T, ctx context.Context, images *nativeImageSourceJournal, owners []nativeLaunchRecord, claims string, record nativeImageSourceRecord) {
	t.Helper()
	path := nativeDiskImageSourcePath(claims, record.Epoch)
	if record.Ready || record.Original.UID == record.Desired.UID {
		t.Fatal("source crash fixture cannot test a premature permission grant")
	}
	if err := os.Chown(path, int(record.Desired.UID), int(record.Desired.GID)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chown(path, int(record.Original.UID), int(record.Original.GID)); err != nil {
			t.Error("restore early-permission fixture:", err)
		}
	}()
	if err := images.inventory(ctx, owners); err == nil {
		t.Fatal("unacknowledged anchor borrowed a later permission transition")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("uncertain metadata authorized disk source cleanup", err)
	}
}

func nativeMetalDiskStagingCrashChild(t *testing.T, ctx context.Context, base string) {
	t.Helper()
	backend := linuxNativeImageSources{base: base, diskStagingRoot: os.Getenv("GREGALE_NATIVE_DISK_STAGING_CLAIMS")}
	j := &nativeLaunchJournal{root: filepath.Join(base, ".native-processes"), imageSources: backend}
	owner, err := j.read(os.Getenv("GREGALE_NATIVE_DISK_STAGING_INSTANCE"))
	if err != nil {
		t.Fatal(err)
	}
	phase := os.Getenv("GREGALE_NATIVE_DISK_STAGING_PHASE")
	images := nativeImageSourceJournal{owner: j, backend: backend}
	images.writeValue = func(path string, record nativeImageSourceRecord) error {
		ref := record.References[0]
		if phase == "source" && record.Placeholder.Inode != 0 && !record.Ready || phase == "anchor" && record.Ready && ref.Target.Inode == 0 || phase == "binding" && ref.Ready {
			os.Exit(0) // Lose producer defers and acknowledgement after the real effect.
		}
		return writeNativeJournalValue(path, record)
	}
	root := filepath.Join(base, "firecracker", owner.Lease.Instance, "root")
	if os.Getenv("GREGALE_NATIVE_DISK_STAGING_KIND") == "drive" {
		_, err = images.stageWritable(ctx, owner, root, os.Getenv("GREGALE_NATIVE_DISK_STAGING_SOURCE"), layerImageName)
	} else {
		name, nameErr := nativeSnapshotOutputName(owner.Generation, "mem")
		if nameErr != nil {
			t.Fatal(nameErr)
		}
		// Exercise the output disk producer using a prepared original owner;
		// qualification/capture authority has separate acceptance coverage.
		_, err = images.stagePrepared(ctx, owner, root, name, false, 0, func(original nativeLaunchRecord) (nativeImagePreparation, error) {
			return backend.PrepareSnapshotOutput(ctx, original, root, backend.diskStagingRoot, name)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal(errors.New("disk staging did not interrupt its original acknowledgement"))
}
