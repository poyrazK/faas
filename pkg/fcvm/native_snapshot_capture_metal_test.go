//go:build linux && metal

// adr: 568 — real kernel protocol acceptance, not Firecracker restore evidence.
package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

func TestMetalNativeCaptureSequence(t *testing.T) {
	runNativeMetalCaptureSequences(t, "TestMetalNativeCaptureSequence", "GREGALE_NATIVE_CAPTURE_SEQUENCE_CHILD", false)
}

// adr: 20261022184951 — an ordinary park/warm capture runs the same producer.
func TestMetalNativeRuntimeCaptureSequence(t *testing.T) {
	runNativeMetalCaptureSequences(t, "TestMetalNativeRuntimeCaptureSequence", "GREGALE_NATIVE_RUNTIME_CAPTURE_SEQUENCE_CHILD", true)
}

func runNativeMetalCaptureSequences(t *testing.T, name, childEnv string, runtimeCapture bool) {
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
	if os.Getenv(childEnv) != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^"+name+"$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"=1")
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
		t.Run(outcome, func(t *testing.T) { nativeMetalCaptureSequence(t, ctx, peer, outcome, runtimeCapture) })
		if ctx.Err() != nil {
			t.Fatal("native capture fixture effect budget exhausted:", ctx.Err())
		}
	}
}

func nativeMetalCaptureSequence(t *testing.T, ctx context.Context, peer, outcome string, runtimeCapture bool) {
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
	// Runtime captures publish into a tree only vmmd writes (amendment 1),
	// opened through the same private-root check vmmd runs at startup.
	nativeRoot := filepath.Join(disk, "native-snap")
	if runtimeCapture {
		if err := os.Mkdir(nativeRoot, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := v.UseNativeRuntimeSnapshotRoot(nativeRoot); err != nil {
			t.Fatal(err)
		}
	}
	r := v.nativeRecovery
	r.snapshotMemory = nativeModeledSnapshotMemoryBackend{} // This protocol peer has no real VM cgroup.
	v.mountHelperPath = os.Getenv("FAAS_TEST_VMMD_BINARY")
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	_, frame, _ := nativeQualificationFixture(t)
	q := r.journal.qualifications(frame.NodeID)
	lease := qualificationLease(frame.InstanceID)
	var incoming nativeQualificationRecord
	if runtimeCapture {
		// An ordinary launch has no qualification incoming record.
		if err := r.journal.prepare(ctx, lease); err != nil {
			t.Fatal(err)
		}
	} else {
		incoming, err = q.claim(ctx, frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.owner.prepare(nativeQualificationContext(ctx, incoming), lease); err != nil {
			t.Fatal(err)
		}
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
	backing := BackingIdentity{Version: backingIdentityVersion, Timer: strings.TrimSpace(guestTimerArgs)}
	for i, name := range []string{"protocol-kernel", "protocol-base.ext4"} {
		body := []byte("original-" + name)
		path := filepath.Join(disk, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := images.stage(ctx, owner, root, path, name, true, 0o044, true); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(body)
		digest := "sha256:" + hex.EncodeToString(hash[:])
		if i == 0 {
			backing.Kernel = digest
		} else {
			backing.Base = digest
		}
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
	captureID := incoming.Generation
	var spec SnapshotSpec
	if runtimeCapture {
		deployment := uuid.NewString()
		captureID = uuid.NewString()
		memory := state.SnapshotCaptureMemKey(deployment, state.SnapshotTierWarm, captureID)
		spec = SnapshotSpec{StorageKey: memory, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: memory}),
			nativeRuntime: &nativeRuntimeCaptureRequest{DeploymentID: deployment, FCVersion: "1.7.0", Backing: backing}}
	}
	cmd = exec.CommandContext(ctx, "/capture-peer", "-capture", captureID, "-drive", layerImageName, "-lost", lost)
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
	if runtimeCapture {
		v.nativeRuntimeCapture = true
		spec.nativeRuntime.NativeGeneration = owner.Generation
		info, captureErr := v.captureNativeRuntimeSnapshot(ctx, lease, spec)
		join()
		checkNativeMetalRuntimeCapture(t, ctx, v, nativeRoot, objectRoot, intentRoot, lease, spec, outcome, info, captureErr, log.String())
		return
	}
	incoming, err = q.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: lease.Instance, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, FCVersion: "1.7.0", StartedAt: q.clock().UTC()}
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
	info, captureErr := v.captureEnvironmentQualificationSnapshot(ctx, lease, backing)
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
		if captureErr == nil || !info.IsZero() || log.String() != want {
			t.Fatal("uncertain native control replayed or supplied completion:", info, captureErr, log.String())
		}
		if entries, err := os.ReadDir(objectRoot); err != nil || len(entries) != 0 {
			t.Fatal("uncertain control reached object publication", err, entries)
		}
	}
	if current, err := q.readCapture(incoming); err != nil || !reflect.DeepEqual(current, capture) || v.checkEnvironmentQualificationSnapshotSupport() == nil {
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

// A completed runtime capture publishes the whole cohort under the scheduler's
// keys into the native tree, never the shared backend, and releases its
// producer bookkeeping. An uncertain one keeps its durable start and intent,
// publishes nothing and refuses another capture.
func checkNativeMetalRuntimeCapture(t *testing.T, ctx context.Context, v *JailerVMM, nativeRoot, objectRoot, intentRoot string, lease Lease, spec SnapshotSpec, outcome string, info SnapshotInfo, captureErr error, peerLog string) {
	t.Helper()
	keys := nativeRuntimeCaptureKeys(spec.StorageKey)
	native := v.nativeRuntimeSnapshots
	if entries, err := os.ReadDir(objectRoot); err != nil || len(entries) != 0 {
		t.Fatal("runtime capture wrote into the shared backend", err, entries)
	}
	record, recordErr := v.nativeRecovery.journal.runtimeCaptures().read(lease.Instance)
	intents, err := os.ReadDir(intentRoot)
	if err != nil {
		t.Fatal(err)
	}
	if outcome == "complete" {
		if captureErr != nil || info.MemBytes != 12 || info.VMStateBytes != 16 || info.StoredBytes <= 0 || peerLog != "pause\ncreate\nresume\n" {
			t.Fatal("runtime capture sequence failed:", info, captureErr, peerLog)
		}
		for key, expected := range map[string]string{keys.Memory: "original-mem", keys.VMState: "original-vmstate", keys.Drive: "original-private-drive"} {
			reader, err := native.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(reader)
			if err := errors.Join(readErr, reader.Close()); err != nil || string(body) != expected {
				t.Fatal("runtime cohort changed frozen/original bytes", key, string(body), err)
			}
		}
		reader, err := native.Get(ctx, keys.Backing)
		if err != nil {
			t.Fatal(err)
		}
		var backing BackingIdentity
		decodeErr := json.NewDecoder(reader).Decode(&backing)
		if err := errors.Join(decodeErr, reader.Close()); err != nil || backing != spec.nativeRuntime.Backing {
			t.Fatal("runtime capture published another backing identity", backing, err)
		}
		if len(intents) != 0 {
			t.Fatal("completed runtime capture kept its publication intent or receipts", intents)
		}
		if !errors.Is(recordErr, os.ErrNotExist) {
			t.Fatal("completed runtime capture kept its durable record", record, recordErr)
		}
		return
	}
	want := "pause\ncreate\n"
	if outcome == "lost_resume" {
		want += "resume\n"
	}
	if captureErr == nil || !info.IsZero() || peerLog != want {
		t.Fatal("uncertain runtime control replayed or supplied completion:", info, captureErr, peerLog)
	}
	if entries, err := os.ReadDir(nativeRoot); err != nil || len(entries) != 0 {
		t.Fatal("uncertain runtime control reached object publication", err, entries)
	}
	if recordErr != nil || !record.Capture.CompletedAt.IsZero() || len(intents) != 1 {
		t.Fatal("uncertain runtime capture lost its durable start or intent", record, recordErr, intents)
	}
	if _, err := v.captureNativeRuntimeSnapshot(ctx, lease, spec); err == nil {
		t.Fatal("a second capture replaced an uncertain runtime capture")
	}
}
