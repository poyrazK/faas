//go:build linux && metal

// adr: 568 — real kernel protocol acceptance, not Firecracker restore evidence.
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

func TestMetalNativeCaptureSequence(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires KVM acceptance host")
	}
	peer := os.Getenv("FAAS_NATIVE_CAPTURE_PEER_BINARY")
	if peer == "" {
		t.Skip("requires static pkg/fcvm/testdata/native-capture-peer binary")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_CAPTURE_SEQUENCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeCaptureSequence$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_CAPTURE_SEQUENCE_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated native capture sequence: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	ctx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for _, outcome := range []string{"complete", "lost_create", "lost_resume"} {
		t.Run(outcome, func(t *testing.T) { nativeMetalCaptureSequence(t, ctx, peer, outcome) })
		if ctx.Err() != nil {
			t.Fatal("native capture fixture effect budget exhausted:", ctx.Err())
		}
	}
}

func nativeMetalCaptureSequence(t *testing.T, ctx context.Context, peer, outcome string) {
	t.Helper()
	// A short jail path keeps the UUID-qualified API socket under sun_path's
	// limit. Only the explicit jail subdirectory is mounted as tmpfs.
	disk, err := os.MkdirTemp("/tmp", "ncap-")
	if err != nil {
		t.Fatal(err)
	}
	jail := filepath.Join(disk, "jail")
	imagesRoot, intentRoot, objectRoot := filepath.Join(disk, "images"), filepath.Join(disk, "intents"), filepath.Join(disk, "objects")
	for _, directory := range []string{jail, imagesRoot, intentRoot, objectRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, disk)) {
		t.Fatal("capture sequence requires private ext4/XFS/Btrfs scratch")
	}
	if err := unix.Mount("tmpfs", jail, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=256m"); err != nil {
		t.Fatal(err)
	}
	canonical, err := storage.NewLocalStorageBackend(objectRoot)
	if err != nil {
		t.Fatal(err)
	}
	v := (&JailerVMM{chrootBase: jail, fcName: "firecracker", storage: canonical}).
		WithNativeImageStagingRoot(imagesRoot).WithNativeSnapshotPublicationRoot(intentRoot).WithNativeProcessRecovery()
	r := v.nativeRecovery
	r.snapshotMemory = nativeModeledSnapshotMemoryBackend{} // This protocol peer has no real VM cgroup.
	v.mountHelperPath = os.Getenv("FAAS_TEST_VMMD_BINARY")
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	_, frame, _ := nativeQualificationFixture(t)
	q := r.journal.qualifications(frame.NodeID)
	incoming, err := q.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	lease := qualificationLease(frame.InstanceID)
	if err := q.owner.prepare(nativeQualificationContext(ctx, incoming), lease); err != nil {
		t.Fatal(err)
	}
	owner, err := q.owner.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	root := v.chrootRoot(lease.Instance)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(disk, "original.ext4")
	if err := os.WriteFile(source, []byte("original-private-drive"), 0o600); err != nil {
		t.Fatal(err)
	}
	images := &nativeImageSourceJournal{owner: q.owner, backend: r.imageSources}
	var cmd *exec.Cmd
	var readyRead, readyWrite *os.File
	joined := false
	rootMounted := false
	join := func() {
		if cmd != nil && cmd.Process != nil && !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			joined = true
		}
	}
	// Cleanup is registered before readiness; even a failed peer is joined.
	t.Cleanup(func() {
		join()
		if readyRead != nil {
			_ = readyRead.Close()
		}
		if readyWrite != nil {
			_ = readyWrite.Close()
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		retired, err := q.owner.revoke(cleanup, lease.Instance)
		if err == nil {
			err = q.owner.confirmExit(cleanup, retired)
		}
		if err == nil {
			retired, err = q.owner.read(lease.Instance)
		}
		helpers := nativeHostHelperJournal{owner: q.owner, groups: r.helperGroups}
		if err == nil {
			err = helpers.retireAll(cleanup, retired)
		}
		if err == nil {
			err = helpers.requireDeviceNamespacesRemoved(cleanup, retired)
		}
		if err == nil {
			err = images.retireAll(cleanup, retired)
		}
		if err == nil {
			err = images.require(cleanup, retired, true)
		}
		if err != nil {
			t.Error("native capture retains uncertain fixture ownership:", err)
			return
		}
		// The daemon lock is itself an open file on the tmpfs jail. Close
		// every lifetime owner after source retirement and before unmount.
		if err := errors.Join(r.daemonLock.Close(), r.diskLock.Close(), r.publications.(*linuxNativeSnapshotPublicationJournal).owner.Close()); err != nil {
			t.Error(err)
			return
		}
		if rootMounted {
			if err := unix.Unmount(root, 0); err != nil {
				t.Error(err)
				return
			}
		}
		if err := unix.Unmount(jail, 0); err != nil {
			t.Error(err)
			return
		}
		if err := os.RemoveAll(disk); err != nil {
			t.Error(err)
		}
	})
	if err := unix.Mount(root, root, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	rootMounted = true
	if _, err := images.stageWritable(ctx, owner, root, source, layerImageName); err != nil {
		t.Fatal(err)
	}
	// Copy a static test peer into this disposable jail before handing the
	// directory to the original guest UID. It has no guest workload or KVM.
	input, err := os.Open(peer)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(filepath.Join(root, "capture-peer"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Close(), input.Close()); err != nil {
		t.Fatal(err)
	}
	if err := v.ownChrootRoot(root, lease); err != nil {
		t.Fatal(err)
	}
	readyRead, readyWrite, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	lost := strings.TrimPrefix(outcome, "lost_")
	cmd = exec.CommandContext(ctx, "/capture-peer", "-capture", incoming.Generation, "-drive", layerImageName, "-lost", lost)
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS, Chroot: root, Credential: &syscall.Credential{Uid: uint32(lease.UID), Gid: uint32(lease.GID)}}
	cmd.ExtraFiles = []*os.File{readyWrite}
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		_ = readyRead.Close()
		_ = readyWrite.Close()
		t.Fatal(err)
	}
	if err := readyWrite.Close(); err != nil {
		t.Fatal(err)
	}
	if err := readyRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(readyRead, ready[:]); err != nil || ready[0] != 1 {
		join()
		t.Fatal("original chroot peer did not start:", err, log.String())
	}
	owner.Authorized, owner.PID = true, cmd.Process.Pid
	owner.StartTime, err = nativeHostHelperStartTime(owner.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	r.remember(owner)
	incoming, err = q.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: lease.Instance, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: q.clock().UTC()}
	if err := q.writeCapture(incoming, capture); err != nil {
		t.Fatal(err)
	}
	lock, err := q.lock(ctx, lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	ctx = nativeSnapshotCaptureContext(ctx, incoming, capture, owner)
	backend := r.imageSources.(linuxNativeImageSources)
	if err := backend.CheckSnapshotCaptureRoot(ctx, owner, root); err != nil {
		_ = lock.Close()
		t.Fatal("original guest-owned live jail was refused:", err)
	}
	for _, change := range []string{"missing_capture", "wrong_start_time", "foreign_process"} {
		bad := ctx
		physical := owner
		switch change {
		case "missing_capture":
			bad = context.WithValue(ctx, nativeSnapshotCaptureContextKey{}, nil)
		case "wrong_start_time":
			physical.StartTime++
			bad = nativeSnapshotCaptureContext(ctx, incoming, capture, physical)
		case "foreign_process":
			physical.PID = os.Getpid()
			physical.StartTime, err = nativeHostHelperStartTime(physical.PID)
			if err != nil {
				_ = lock.Close()
				t.Fatal(err)
			}
			bad = nativeSnapshotCaptureContext(ctx, incoming, capture, physical)
		}
		if err := backend.CheckSnapshotCaptureRoot(bad, physical, root); err == nil {
			_ = lock.Close()
			t.Fatal("foreign process gained original live output preparation:", change)
		}
	}
	info, captureErr := v.captureEnvironmentQualificationSnapshot(ctx, lease, BackingIdentity{Version: 1, Kernel: "fixture-kernel", Base: "fixture-base"})
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	join()
	if outcome == "complete" {
		if captureErr != nil || info.MemBytes != 12 || info.VMStateBytes != 16 || info.StoredBytes != nativeMetalPublicationReceiptBytes(t, ctx, v, incoming) || log.String() != "pause\ncreate\nresume\n" {
			t.Fatal("kernel capture sequence failed:", info, captureErr, log.String())
		}
		keys := qualificationSnapshotProof(incoming, SnapshotInfo{})
		for key, expected := range map[string]string{keys.StorageKey: "original-mem", keys.VMStateStorageKey: "original-vmstate", keys.DriveStorageKey: "original-private-drive"} {
			reader, err := canonical.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(reader)
			if err := errors.Join(readErr, reader.Close()); err != nil || string(body) != expected {
				t.Fatal("native cohort changed frozen/original bytes", key, string(body), err)
			}
		}
	} else {
		want := "pause\ncreate\n"
		if outcome == "lost_resume" {
			want += "resume\n"
		}
		if captureErr == nil || info != (SnapshotInfo{}) || log.String() != want {
			t.Fatal("uncertain native control replayed or supplied completion:", info, captureErr, log.String())
		}
		if entries, err := os.ReadDir(objectRoot); err != nil || len(entries) != 0 {
			t.Fatal("uncertain control reached object publication", err, entries)
		}
	}
	if current, err := q.readCapture(incoming); err != nil || current != capture || v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("kernel protocol fixture promoted or enabled capture", err)
	}
	entries, err := os.ReadDir(intentRoot)
	expectedEntries := 1
	if outcome == "complete" {
		expectedEntries = 5
	}
	if err != nil || len(entries) != expectedEntries {
		t.Fatal("kernel capture lost original persistent publication intent/receipts", err, entries)
	}
	if _, err := os.Lstat(filepath.Join(intentRoot, incoming.Generation+".json")); err != nil {
		t.Fatal(err)
	}
}
