//go:build linux && metal

// adr: 568 — actual VM capture acceptance stays separate from graph evidence.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

// This opt-in boots a real native-owned Firecracker VM, captures through the
// internal producer and restores receipt-verified bytes through an isolated
// ordinary lifecycle fixture. It does not grant native qualification restore, object cleanup
// or graph activation evidence, and never opens the production capture gate.
func TestMetalNativeCaptureVM(t *testing.T) {
	if os.Getenv("FAAS_NATIVE_CAPTURE_VM") != "1" {
		t.Skip("set FAAS_NATIVE_CAPTURE_VM=1 on the authorized isolated KVM node")
	}
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("native VM capture acceptance requires x86_64 Linux and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_CAPTURE_VM_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--net", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeCaptureVM$", "-test.timeout=4m", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_CAPTURE_VM_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated real native capture: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	// Leave one minute for joined retirement before the outer process budget.
	work, stop := context.WithTimeout(ctx, 3*time.Minute)
	defer stop()
	nativeMetalCaptureVM(t, work)
}

func nativeMetalCaptureVM(t *testing.T, ctx context.Context) {
	t.Helper()
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Fatal(err)
	}
	disk, err := os.MkdirTemp("/tmp", "ncvm-")
	if err != nil {
		t.Fatal(err)
	}
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, disk)) {
		t.Fatal("real capture requires an ext4/XFS/Btrfs scratch directory")
	}
	jail, restoreJail := filepath.Join(disk, "jail"), JailChrootBase
	images, intents, objects := filepath.Join(disk, "images"), filepath.Join(disk, "intents"), filepath.Join(disk, "objects")
	for _, directory := range []string{jail, images, intents, objects} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The ordinary jailer uses its production base. This mount is private to
	// the child namespace and cannot replace the host's active jail mount.
	if err := os.MkdirAll(restoreJail, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{jail, restoreJail} {
		if err := unix.Mount("tmpfs", directory, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=256m"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", "/run/netns", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "mode=0755,size=4m"); err != nil {
		t.Fatal(err)
	}
	runner := wire.ExecRunner{}
	for _, argv := range [][]string{
		{"ip", "link", "set", "lo", "up"},
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := runner.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	canonical, err := storage.NewLocalStorageBackend(objects)
	if err != nil {
		t.Fatal(err)
	}
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v := newMetalVMM(t, 45*time.Second)
	v.chrootBase = jail
	v.WithStorage(canonical).WithNativeImageStagingRoot(images).WithNativeSnapshotPublicationRoot(intents).WithNativeProcessRecovery()
	r := v.nativeRecovery
	_, frame, _ := nativeQualificationFixture(t)
	frame.RAMMB = 128
	m := NewManager(runner, v, Paths{Kernel: kernel}, version, nil, nil).WithNativeQualificationNodeID(frame.NodeID)
	m.WithStorage(canonical)
	m.alloc.free = []int{MaxSlots - 1}
	var restoredManager *Manager
	restoredID := uuid.NewString()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer stop()
		var retireErr error
		if restoredManager != nil {
			retireErr = restoredManager.Destroy(cleanup, restoredID)
		}
		q := r.journal.qualifications(frame.NodeID)
		if incoming, err := q.read(frame.InstanceID); err == nil && incoming.NativeGeneration != "" {
			_, err = m.RetireEnvironmentQualification(cleanup, frame)
			retireErr = errors.Join(retireErr, err)
		}
		if retireErr != nil {
			t.Error("real capture retains uncertain original VM ownership:", retireErr)
			return
		}
		for _, lock := range []*os.File{r.daemonLock, r.diskLock} {
			if lock != nil {
				retireErr = errors.Join(retireErr, lock.Close())
			}
		}
		if publication := r.publications.(*linuxNativeSnapshotPublicationJournal); publication.owner != nil {
			retireErr = errors.Join(retireErr, publication.owner.Close())
		}
		if retireErr != nil {
			t.Error(retireErr)
			return
		}
		for _, directory := range []string{jail, restoreJail, "/run/netns"} {
			if err := unix.Unmount(directory, 0); err != nil {
				t.Error(err)
				return
			}
		}
		if err := os.RemoveAll(disk); err != nil {
			t.Error(err)
		}
	})
	base, layer := filepath.Join(disk, "base.ext4"), filepath.Join(disk, "layer.ext4")
	if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
		t.Fatal(err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatal(err)
	}
	// This locally assembled guest is a lifecycle fixture. Model only its
	// admission sidecar; this run does not establish Grype scan evidence.
	scan, err := json.Marshal(baseScanSidecar{Image: "native-capture-fixture",
		Findings: map[string]int{"CRITICAL": 0}, FixAvailableFindings: map[string]int{"CRITICAL": 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.Put(ctx, wire.ScanKeyForBaseKey(base), bytes.NewReader(scan)); err != nil {
		t.Fatal(err)
	}
	frame.Artifact.RootfsKey, frame.Artifact.RootfsBytes = layer, 64<<20
	ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: frame.WakeID})
	req := WakeRequest{Instance: frame.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID,
		AccountID: uuid.NewString(), Plan: api.PlanHobby, MemSizeMiB: frame.RAMMB, VcpuCount: 1, CPUMillicores: 1000,
		BaseKey: base, LayerKey: layer, DisableStartupCPUBoost: true}
	instance, err := m.WakeEnvironmentQualification(ctx, frame, req)
	if err != nil {
		t.Fatal("native qualification cold boot:", err)
	}
	originalUUID := fetchV6UUID(t, instance.Lease.HostIP.String())
	if originalUUID == "" {
		t.Fatal("real cold boot has no guest readiness witness")
	}
	scope := nativeCgroupScope(instance.Lease)
	beforeOOM := nativeCaptureVMMemoryEvent(t, scope, "oom_kill")
	beforeLimit := nativeCaptureVMMemoryLimit(t, scope)
	if beforeLimit != uint64(api.BillableRAMMB(frame.RAMMB))<<20 {
		t.Fatal("original native VM fence differs from policy", beforeLimit)
	}
	info, incoming, err := nativeMetalCaptureVMProducer(ctx, m, v, frame.InstanceID, frame.NodeID)
	if err != nil || info.MemBytes != int64(frame.RAMMB)<<20 || info.VMStateBytes <= 0 || info.StoredBytes <= 0 {
		t.Fatal("real native capture failed:", info, err)
	}
	if stored := nativeMetalPublicationReceiptBytes(t, ctx, v, incoming); stored != info.StoredBytes {
		t.Fatal("capture accounting differs from original allocation receipts", stored, info.StoredBytes)
	}
	if after := nativeCaptureVMMemoryLimit(t, scope); after != beforeLimit {
		t.Fatal("capture did not restore the original VM fence", beforeLimit, after)
	}
	if after := nativeCaptureVMMemoryEvent(t, scope, "oom_kill"); after != beforeOOM {
		t.Fatal("native capture triggered an OOM kill", beforeOOM, after)
	}
	helpers := nativeHostHelperJournal{owner: v.nativeRecovery.journal}
	physical, err := v.nativeRecovery.journal.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	memoryRecord, err := helpers.snapshotMemoryRecord(physical)
	if err != nil || memoryRecord == nil || memoryRecord.SnapshotOutput.Memory == nil || memoryRecord.SnapshotOutput.Memory.Phase != nativeSnapshotMemoryRestored {
		t.Fatal("original memory-fence receipt is incomplete", err)
	}
	if got := fetchV6UUID(t, instance.Lease.HostIP.String()); got != originalUUID {
		t.Fatal("native capture failed to resume the original guest", got)
	}
	if _, err := m.RetireEnvironmentQualification(ctx, frame); err != nil {
		t.Fatal("captured original VM did not retire:", err)
	}
	physical, err = v.nativeRecovery.journal.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := helpers.requireSnapshotMemoryDisposed(physical); err != nil {
		t.Fatal("original headroom scope was not disposed", err)
	}
	// Use a separate ordinary lifecycle root. Switching the native root back
	// to the legacy allocator would bypass ownership and is deliberately denied.
	restoreVMM := newMetalVMM(t, 45*time.Second)
	restoreVMM.chrootBase = restoreJail
	restoreVMM.WithStorage(canonical)
	restoredManager = NewManager(runner, restoreVMM, Paths{Kernel: kernel}, version, nil, nil)
	restoredManager.WithStorage(canonical)
	restoredManager.alloc.free = []int{MaxSlots - 1}
	completed, err := r.journal.qualifications(frame.NodeID).readCapture(incoming)
	if err != nil {
		t.Fatal("original capture completion missing:", err)
	}
	nativeMetalCaptureVMRestoreBackings(t, ctx, v, incoming, completed, [2]string{kernel, base}, disk)
	err = withNativeSnapshotRestoreInputs(ctx, r.publications.(nativeSnapshotRestoreReceiptJournal), canonical, completed, images, func(inputs nativeSnapshotRestoreInputs) (result error) {
		// Only this disposable acceptance fixture names the verified copies.
		// Production inputs stay anonymous and need native staging ownership.
		verifiedRoot := filepath.Join(disk, "verified-restore")
		if err := os.Mkdir(verifiedRoot, 0o700); err != nil {
			return err
		}
		verified, err := storage.NewLocalStorageBackend(verifiedRoot)
		if err != nil {
			return err
		}
		for i, receipt := range inputs.Cohort.Objects {
			if err := verified.Put(ctx, receipt.Object.Key, io.NewSectionReader(inputs.Files[i], 0, receipt.Object.LogicalBytes)); err != nil {
				return err
			}
		}
		// Preserve the fixture's modeled admission evidence in its separate
		// store. No scan guard is bypassed and this is still not Grype proof.
		if err := verified.Put(ctx, wire.ScanKeyForBaseKey(base), bytes.NewReader(scan)); err != nil {
			return err
		}
		restoreVMM.WithStorage(verified)
		restoredManager.WithStorage(verified)
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			defer stop()
			result = errors.Join(result, restoredManager.Destroy(cleanup, restoredID))
		}()
		keys := qualificationSnapshotProof(incoming, info)
		restored, err := restoredManager.Wake(ctx, WakeRequest{Instance: restoredID, Plan: api.PlanHobby,
			BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: frame.RAMMB, DisableStartupCPUBoost: true,
			Snapshot: &Snapshot{FCVersion: version, StorageKey: keys.StorageKey, VMStateStorageKey: keys.VMStateStorageKey}})
		if err != nil {
			return err
		}
		if restored.Method != WakeRestore {
			return fmt.Errorf("receipt-verified artifact acceptance used cold fallback: %s", restored.RestoreError)
		}
		if got := fetchV6UUID(t, restored.Lease.HostIP.String()); got == "" || got == originalUUID {
			return errors.New("receipt-verified restore lacks fresh readiness/entropy witness")
		}
		return nil
	})
	if err != nil {
		t.Fatal("restore receipt-verified native-produced artifacts:", err)
	}
	if v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("VM acceptance opened production native qualification capture")
	}
	t.Logf("actual native capture: memory=%d device-state=%d allocated-total=%d; original resumed, retired and receipt-verified cohort restored", info.MemBytes, info.VMStateBytes, info.StoredBytes)
}

// The actual producer has retired. This separate native target verifies and
// stages the original kernel/base names under its own image epochs, then
// proves its own cleanup. It does not load or grant guest/graph readiness.
func nativeMetalCaptureVMRestoreBackings(t *testing.T, ctx context.Context, v *JailerVMM, incoming nativeQualificationRecord, completed nativeQualificationCaptureRecord, paths [2]string, disk string) {
	t.Helper()
	r := v.nativeRecovery
	q := r.journal.qualifications(incoming.Execution.NodeID)
	backings, err := q.readBackings(completed)
	if err != nil {
		t.Fatal("real capture backing evidence:", err)
	}
	frame := incoming.Execution
	frame.InstanceID, frame.WakeID, frame.CleanupToken, frame.CaptureInstanceID = uuid.NewString(), uuid.NewString(), uuid.NewString(), frame.InstanceID
	target, err := q.restores().claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx = nativeQualificationRestoreContext(ctx, target)
	lease := leaseForSlot(frame.InstanceID, MaxSlots-2)
	lease.Plan, lease.MemoryMaxMiB, lease.CPUMillicores = api.PlanHobby, api.BillableRAMMB(frame.RAMMB), 1000
	if err := q.owner.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	owner, err := q.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	r.remember(owner)
	retire := func(cleanup context.Context) error {
		_, err := q.restores().revoke(cleanup, frame)
		if err == nil {
			err = v.Kill(cleanup, lease)
		}
		if err == nil {
			var physical nativeLaunchRecord
			physical, err = q.owner.read(frame.InstanceID)
			if err == nil {
				err = q.owner.confirmResourcesRemoved(cleanup, physical)
			}
		}
		return err
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer stop()
		if err := retire(cleanup); err != nil {
			t.Error("native restore backing target retains original ownership:", err)
		}
	})
	root, err := v.mkChrootForOwner(ctx, owner, frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	// A content candidate's current basename must not choose the captured drive path.
	alias := filepath.Join(disk, "candidate-base-any-name.ext4")
	if err := os.Link(paths[1], alias); err != nil {
		t.Fatal(err)
	}
	paths[1] = alias
	if err := v.stageNativeQualificationRestoreBackings(ctx, owner, paths); err != nil {
		t.Fatal("real backing staging:", err)
	}
	images := nativeImageSourceJournal{owner: q.owner, backend: r.imageSources}
	staged, err := images.records()
	if err != nil {
		t.Fatal(err)
	}
	for _, image := range backings.Images {
		file, err := os.Open(filepath.Join(root, image.Name))
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		err = errors.Join(unix.Fstat(int(file.Fd()), &stat), verifyNativeRestoreDigest(ctx, file, image.LogicalBytes, image.SHA256), file.Close())
		if err != nil || stat.Nlink != 0 || (nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) == image.Identity {
			t.Fatal("real restored backing aliases or differs from source:", err)
		}
		found := false
		for _, source := range staged {
			for _, ref := range source.References {
				if sameNativeImageOwner(ref, owner) && ref.Name == image.Name && source.Epoch != image.Epoch && ref.ReadOnly && ref.Ready {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("real restored backing lacks target's own read-only epoch", image.Name)
		}
	}
	if err := retire(ctx); err != nil {
		t.Fatal(err)
	}
	proof, err := q.restores().retirement(ctx, frame)
	if err != nil || proof.NativeGeneration != owner.Generation || proof.NativeGeneration == completed.NativeGeneration {
		t.Fatal("real backing target borrowed source retirement:", err)
	}
	t.Log("actual captured kernel/base: anonymous verified native target epochs retained original jail names and retired independently; target load remains gated")
}

// Invoke the internal original producer without overriding the public support
// gate or writing Manager capture completion before artifact receipts exist.
func nativeMetalCaptureVMProducer(ctx context.Context, m *Manager, v *JailerVMM, instance, node string) (info SnapshotInfo, incoming nativeQualificationRecord, err error) {
	q := v.nativeRecovery.journal.qualifications(node)
	lock, err := q.lock(ctx, instance)
	if err != nil {
		return info, incoming, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	incoming, err = q.read(instance)
	if err != nil {
		return info, incoming, err
	}
	physical, err := q.snapshotPhysical(ctx, incoming)
	if err != nil {
		return info, incoming, err
	}
	backing, err := m.qualificationSnapshotBacking(instance)
	if err != nil {
		return info, incoming, err
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: instance, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: q.clock().UTC()}
	if err := q.writeCapture(incoming, capture); err != nil {
		return info, incoming, err
	}
	info, err = v.captureEnvironmentQualificationSnapshot(nativeSnapshotCaptureContext(ctx, incoming, capture, physical), incoming.NativeLease, backing)
	if err != nil {
		return info, incoming, err
	}
	if err := q.requireSnapshotPhysical(ctx, incoming); err != nil {
		return SnapshotInfo{}, incoming, err
	}
	capture.Info, capture.Backing, capture.CompletedAt = info, backing, q.clock().UTC()
	return info, incoming, errors.Join(q.writeCapture(incoming, capture), ctx.Err())
}

func nativeCaptureVMMemoryLimit(t *testing.T, scope string) uint64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scope, "memory.max"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func nativeCaptureVMMemoryEvent(t *testing.T, scope, name string) uint64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scope, "memory.events"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != name {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	t.Fatal("original cgroup has no memory event counter", name)
	return 0
}
