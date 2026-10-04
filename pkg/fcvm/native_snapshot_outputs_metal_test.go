//go:build linux && metal

// adr: 568 — original capture output binds survive producer death until retirement.
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
	for _, phase := range []string{"anchor", "binding"} {
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
	if err != nil || len(records) != 2 || records[0].Identity == records[1].Identity {
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
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: q.clock().UTC()}
	if err := q.writeCapture(incoming, capture); err != nil {
		t.Fatal(err)
	}
	images := &nativeImageSourceJournal{owner: j, backend: backend}
	images.writeValue = func(path string, record nativeImageSourceRecord) error {
		ref := record.References[len(record.References)-1]
		if phase == "anchor" && record.Ready && ref.Target.Inode == 0 || phase == "binding" && ref.Ready {
			// Keep the last durable intent and real kernel bind, but lose the
			// acknowledgement and all producer file defers through process exit.
			os.Exit(0)
		}
		return writeNativeJournalValue(path, record)
	}
	kind := "mem"
	if phase == "binding" {
		kind = "vmstate"
	}
	permit := nativeSnapshotCapturePermit{Incoming: incoming, Capture: capture, Physical: owner}
	if _, err := images.stageCaptureOutput(ctx, owner, permit, filepath.Join(root, "firecracker", instance, "root"), root, kind); err != nil {
		t.Fatal(err)
	}
	t.Fatal("native capture output crash did not interrupt its acknowledgement")
}
