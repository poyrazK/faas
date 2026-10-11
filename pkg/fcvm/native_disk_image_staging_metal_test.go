//go:build linux && metal

// adr: 568 — a tmpfs jail cannot own a persistent disk name through reboot.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
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
	r := &nativeProcessRecoveryRuntime{journal: j, imageSources: backend, owned: make(map[string]string)}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.daemonLock.Close()
	defer r.diskLock.Close()
	source := filepath.Join(disk, "immutable.img")
	if err := os.WriteFile(source, []byte("original-native-disk-layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"drive", "output", "restore-mem", "restore-state", "restore-drive", "restore-kernel", "restore-base"} {
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
	if err != nil || len(records) != 21 {
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
	// Join the complete receipt barrier with the original daemon's prepared
	// target using real disk copies and binds. These are modeled snapshot bytes;
	// no Firecracker load, qualification execution or graph evidence is minted.
	lease := leaseForSlot("disk-restore-complete", len(owners))
	lease.Plan, lease.Networkless = api.PlanHobby, true
	if err := j.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	owners = append(owners, owner)
	r.remember(owner)
	root := filepath.Join(base, "firecracker", lease.Instance, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	v := &JailerVMM{chrootBase: base, fcName: "firecracker", nativeRecovery: r}
	f := nativeRestoreInputsFixture(t, nil)
	var received nativeSnapshotRestoreInputs
	if err := withNativeSnapshotRestoreInputs(ctx, f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
		received = inputs
		if err := v.stageNativeSnapshotRestoreInputs(ctx, owner, root, inputs, f.completed, f.journal); err != nil {
			return err
		}
		for i, name := range [...]string{memSnapshotName, vmstateSnapshotName, layerImageName} {
			path := filepath.Join(root, name)
			body, err := os.ReadFile(path)
			if err != nil || string(body) != string(f.bodies[i]) {
				t.Fatal("native staging redirected verified bytes", name, err)
			}
			var stat unix.Stat_t
			if err := unix.Stat(path, &stat); err != nil || uint64(stat.Dev) == uint64(jail.Dev) || stat.Nlink != 0 {
				t.Fatal("staged bytes were copied to tmpfs or kept a disk name", name, err)
			}
			if name != layerImageName {
				file, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err == nil {
					_ = file.Close()
					t.Fatal("memory/device-state binding permits writes", name)
				}
			} else {
				file, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteAt([]byte("guest"), 0)
				if err := errors.Join(writeErr, file.Close()); err != nil {
					t.Fatal(err)
				}
				original := make([]byte, len(f.bodies[i]))
				if _, err := inputs.Files[i].ReadAt(original, 0); err != nil || string(original) != string(f.bodies[i]) {
					t.Fatal("guest write reached the sealed receipt input", err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, file := range received.Files {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("verified source descriptor escaped its consumer", err)
		}
	}
	if err := images.require(ctx, owner, false); err != nil {
		t.Fatal("original epoch lost staging when verified inputs closed", err)
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
	kind := os.Getenv("GREGALE_NATIVE_DISK_STAGING_KIND")
	if kind == "drive" {
		_, err = images.stageWritable(ctx, owner, root, os.Getenv("GREGALE_NATIVE_DISK_STAGING_SOURCE"), layerImageName)
	} else if kind == "output" {
		name, nameErr := nativeSnapshotOutputName(owner.Generation, "mem")
		if nameErr != nil {
			t.Fatal(nameErr)
		}
		// Exercise the output disk producer using a prepared original owner;
		// qualification/capture authority has separate acceptance coverage.
		_, err = images.stagePrepared(ctx, owner, root, name, false, 0, func(original nativeLaunchRecord) (nativeImagePreparation, error) {
			return backend.PrepareSnapshotOutput(ctx, original, root, backend.diskStagingRoot, name)
		})
	} else if kind == "restore-kernel" || kind == "restore-base" {
		input, receipt := nativeMetalSealedRestoreStagingInput(t, backend.diskStagingRoot)
		defer input.Close()
		image := nativeSnapshotBackingImage{Epoch: uuid.NewString(), ReferenceID: uuid.NewString(), Identity: nativeLoopIdentity{Device: 11, Inode: 12},
			Name: "captured-" + kind, LogicalBytes: receipt.LogicalBytes, SHA256: receipt.SHA256}
		_, err = images.stagePrepared(ctx, owner, root, image.Name, true, 0o044, func(original nativeLaunchRecord) (nativeImagePreparation, error) {
			return backend.PrepareRestoreBacking(ctx, original, root, input, image)
		})
	} else {
		name := map[string]string{"restore-mem": memSnapshotName, "restore-state": vmstateSnapshotName, "restore-drive": layerImageName}[kind]
		if name == "" {
			t.Fatal("unknown descriptor staging fixture kind")
		}
		input, receipt := nativeMetalSealedRestoreStagingInput(t, backend.diskStagingRoot)
		defer input.Close()
		_, err = images.stageRestoreInput(ctx, owner, root, input, name, receipt)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal(errors.New("disk staging did not interrupt its original acknowledgement"))
}

// A small modeled receipt isolates native clone/mount crash behavior from
// publication. The complete-input case above uses real local object receipts.
func nativeMetalSealedRestoreStagingInput(t *testing.T, directory string) (*os.File, storage.ExclusiveArtifactReceipt) {
	t.Helper()
	body := []byte("modeled-original-snapshot-input")
	fd, err := unix.Open(directory, unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	output := os.NewFile(uintptr(fd), "modeled-unsealed-snapshot-input")
	if _, err := output.Write(body); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := errors.Join(output.Chmod(0o400), output.Sync()); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	readFD, err := unix.Open(nativeImageFDPath(output), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err := errors.Join(err, output.Close()); err != nil {
		if readFD >= 0 {
			_ = unix.Close(readFD)
		}
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	receipt := storage.ExclusiveArtifactReceipt{Version: 1, Key: "modeled/restore.snap", ObjectKey: "modeled/restore.snap", Backend: "gcs",
		Location: "modeled-native-staging", LogicalBytes: int64(len(body)), StoredBytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), Generation: 1}
	return os.NewFile(uintptr(readFD), "modeled-sealed-snapshot-input"), receipt
}
