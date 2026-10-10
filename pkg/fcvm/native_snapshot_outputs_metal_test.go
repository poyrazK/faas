//go:build linux && metal

// adr: 568 — original capture output binds survive producer death until retirement.
package fcvm

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This exercises real anonymous files, binds and crash recovery on the native
// host. Its child process is an ownership fixture, not a captured Firecracker VM.
func TestMetalNativeSnapshotOutputRecovery(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires dedicated native KVM acceptance host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_CAPTURE_OUTPUT_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeSnapshotOutputRecovery$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_CAPTURE_OUTPUT_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated capture output acceptance: %v\n%s", err, out)
		} else {
			t.Logf("%s", out)
		}
		return
	}
	if root := os.Getenv("GREGALE_NATIVE_CAPTURE_OUTPUT_ROOT"); root != "" {
		nativeMetalCaptureOutputCrashChild(t, ctx, root, os.Getenv("GREGALE_NATIVE_CAPTURE_OUTPUT_INSTANCE"), os.Getenv("GREGALE_NATIVE_CAPTURE_OUTPUT_NODE"), os.Getenv("GREGALE_NATIVE_CAPTURE_OUTPUT_PHASE"))
		return
	}
	root, err := os.MkdirTemp("", "gregale-native-capture-output-")
	if err != nil {
		t.Fatal(err)
	}
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, root)) {
		t.Fatal("capture output acceptance requires an ext4, XFS or Btrfs temporary disk directory")
	}
	nativeMetalRefuseTmpfsStagingBeforeEffects(t, ctx, root)
	backend := newNativeImageSourceBackend(root)
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), imageSources: backend}
	images := &nativeImageSourceJournal{owner: j, backend: backend}
	var owners []nativeLaunchRecord
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, expected := range owners {
			owner, err := j.revoke(cleanup, expected.Lease.Instance)
			if err == nil && owner.Generation != expected.Generation {
				err = errors.New("capture output fixture owner changed")
			}
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
				t.Error("native capture output cleanup:", err)
				return
			}
		}
		mounts, err := nativeJailMounts(root)
		if err != nil || len(mounts) != 0 {
			t.Errorf("native capture outputs retain mounts: %v %v", mounts, err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	for _, phase := range []string{"source", "anchor", "binding", "read_mem", "read_vmstate"} {
		_, frame, _ := nativeQualificationFixture(t)
		q := j.qualifications(frame.NodeID)
		incoming, err := q.claim(ctx, frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.prepare(nativeQualificationContext(ctx, incoming), qualificationLease(frame.InstanceID)); err != nil {
			t.Fatal(err)
		}
		owner, err := j.read(frame.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, owner)
		if err := os.MkdirAll(filepath.Join(root, "firecracker", frame.InstanceID, "root"), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativeSnapshotOutputRecovery$", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_CAPTURE_OUTPUT_ROOT="+root, "GREGALE_NATIVE_CAPTURE_OUTPUT_INSTANCE="+frame.InstanceID,
			"GREGALE_NATIVE_CAPTURE_OUTPUT_NODE="+frame.NodeID, "GREGALE_NATIVE_CAPTURE_OUTPUT_PHASE="+phase, "GORACE=atexit_sleep_ms=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("capture output %s crash producer: %v\n%s", phase, err, out)
		}
		current, err := j.read(frame.InstanceID)
		if err != nil || current.PID != cmd.Process.Pid || !cmd.ProcessState.Exited() {
			t.Fatalf("original output producer exit was not joined: %+v %v", current, err)
		}
	}
	records, err := images.records()
	if err != nil || len(records) != 5 || records[0].Identity == records[1].Identity {
		t.Fatalf("crash lost independent original output epochs: %+v %v", records, err)
	}
	restarted := &nativeImageSourceJournal{owner: &nativeLaunchJournal{root: j.root, imageSources: backend}, backend: backend}
	var liveOwners []nativeLaunchRecord
	for _, original := range owners {
		owner, err := j.read(original.Lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		liveOwners = append(liveOwners, owner)
	}
	if err := restarted.inventory(ctx, liveOwners); err != nil {
		t.Fatal("restart did not inventory interrupted output binds", err)
	}
	for _, original := range owners {
		owner := retireNativeImageFixtureOwner(t, restarted, original)
		if err := restarted.owner.confirmResourcesRemoved(ctx, owner); err == nil {
			t.Fatal("surviving output bind released physical ownership")
		}
		if err := restarted.retireAll(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := restarted.owner.confirmResourcesRemoved(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	if mounts, err := nativeJailMounts(root); err != nil || len(mounts) != 0 {
		t.Fatalf("capture output producer mounts leaked: %v %v", mounts, err)
	}
}

func nativeMetalRefuseTmpfsStagingBeforeEffects(t *testing.T, ctx context.Context, disk string) {
	t.Helper()
	base, err := os.MkdirTemp("", "gregale-native-tmpfs-profile-")
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", base, "tmpfs", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0700,size=4m"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(base, 0); err != nil {
			t.Error(err)
			return
		}
		if err := os.Remove(base); err != nil {
			t.Error(err)
		}
	})
	q, _, _ := nativeQualificationFixture(t)
	lease := qualificationLease("tmpfs-staging-profile")
	if err := q.owner.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	owner, err := q.owner.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	backend := linuxNativeImageSources{base: base}
	root := filepath.Join(base, "firecracker", lease.Instance, "root")
	name, err := nativeSnapshotOutputName(owner.Generation, "mem")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(disk)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"capture", "drive"} {
		var prepared nativeImagePreparation
		if kind == "capture" {
			prepared, err = backend.PrepareSnapshotOutput(ctx, owner, root, disk, name)
		} else {
			prepared, err = backend.PrepareWritable(ctx, owner, root, filepath.Join(disk, "absent-immutable.img"), layerImageName)
		}
		if prepared != nil {
			_ = prepared.Close()
		}
		if prepared != nil || err == nil || !strings.Contains(err.Error(), "same filesystem") {
			t.Fatal("tmpfs profile reached output production", kind, err)
		}
	}
	if entries, err := os.ReadDir(base); err != nil || len(entries) != 0 {
		t.Fatal("tmpfs rejection prepared a jail or ownership marker", entries, err)
	}
	if entries, err := os.ReadDir(disk); err != nil || len(entries) != len(before) {
		t.Fatal("tmpfs rejection left a disk output", err)
	}
}

func nativeMetalCaptureOutputCrashChild(t *testing.T, ctx context.Context, root, instance, node, phase string) {
	t.Helper()
	backend := newNativeImageSourceBackend(root)
	j := &nativeLaunchJournal{root: filepath.Join(root, ".native-processes"), imageSources: backend}
	owner, err := j.read(instance)
	if err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID = true, os.Getpid()
	owner.StartTime, err = nativeHostHelperStartTime(owner.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.write(owner); err != nil {
		t.Fatal(err)
	}
	q := j.qualifications(node)
	incoming, err := q.read(instance)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := q.lock(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: instance, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, FCVersion: "1.7.0", StartedAt: q.clock().UTC()}
	if err := q.writeCapture(incoming, capture); err != nil {
		t.Fatal(err)
	}
	images := &nativeImageSourceJournal{owner: j, backend: backend}
	images.writeValue = func(path string, record nativeImageSourceRecord) error {
		ref := record.References[len(record.References)-1]
		if phase == "source" && !record.Ready && record.Placeholder.Inode != 0 || phase == "anchor" && record.Ready && ref.Target.Inode == 0 || phase == "binding" && ref.Ready {
			// Keep the last durable intent and real kernel bind, but lose the
			// acknowledgement and all producer file defers through process exit.
			os.Exit(0)
		}
		return writeNativeJournalValue(path, record)
	}
	kind := "mem"
	if phase == "binding" || phase == "read_vmstate" {
		kind = "vmstate"
	}
	permit := nativeSnapshotCapturePermit{Incoming: incoming, Capture: capture, Physical: owner}
	if _, err := images.stageCaptureOutput(ctx, owner, permit, filepath.Join(root, "firecracker", instance, "root"), root, kind); err != nil {
		t.Fatal(err)
	}
	if phase == "read_mem" || phase == "read_vmstate" {
		nativeMetalReadCaptureOutput(t, ctx, j, owner, permit, root, kind)
		// Producer death keeps the original bind for the parent's recovery
		// check, after the synchronous reader has closed its descriptor.
		os.Exit(0)
	}
	t.Fatal("native capture output crash did not interrupt its acknowledgement")
}

func nativeMetalReadCaptureOutput(t *testing.T, ctx context.Context, j *nativeLaunchJournal, owner nativeLaunchRecord, permit nativeSnapshotCapturePermit, root, kind string) {
	t.Helper()
	r := &nativeProcessRecoveryRuntime{journal: j, imageSources: j.imageSources, owned: make(map[string]string)}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.daemonLock.Close()
	r.remember(owner)
	v := &JailerVMM{chrootBase: root, fcName: "firecracker", nativeRecovery: r}
	name, err := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
	if err != nil {
		t.Fatal(err)
	}
	// Model Firecracker writing the already-owned target, while testing real
	// anonymous inode/bind/read behavior. This supplies no VM restore proof.
	if err := os.WriteFile(filepath.Join(v.chrootRoot(owner.Lease.Instance), name), []byte(kind), 0o600); err != nil {
		t.Fatal(err)
	}
	var consumed *os.File
	if err := v.withNativeSnapshotOutput(nativeSnapshotCaptureContext(ctx, permit.Incoming, permit.Capture, owner), owner.Lease, kind, func(file *os.File) error {
		consumed = file
		flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			return errors.Join(err, errors.New("capture output descriptor can escape through exec"))
		}
		body, err := io.ReadAll(file)
		if err != nil || string(body) != kind {
			return errors.Join(err, errors.New("capture output reader changed original bytes"))
		}
		if _, err := file.WriteAt([]byte("wrong"), 0); !errors.Is(err, unix.EBADF) {
			return errors.Join(err, errors.New("capture output reader permits writing"))
		}
		// OCI publication reopens File.Name; verify it names this still-open
		// pinned read-only descriptor, rather than a replaceable jail path.
		reopened, err := os.OpenFile(file.Name(), os.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer reopened.Close()
		original, err := file.Stat()
		other, statErr := reopened.Stat()
		if err != nil || statErr != nil || !os.SameFile(original, other) {
			return errors.Join(err, statErr, errors.New("publication reopen changed original inode"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if consumed == nil {
		t.Fatal("native output reader did not run")
	}
	if _, err := consumed.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("native output reader escaped its consumer", err)
	}
}
