// adr: 590
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type snapshotRestoreSourceFixture struct {
	vmm     *JailerVMM
	backend *runtimeSourceTestBackend
	lease   Lease
	request SnapshotRestoreInputs
}

func newSnapshotRestoreSourceFixture(t *testing.T) snapshotRestoreSourceFixture {
	t.Helper()
	return newSnapshotRestoreSourceFixtureWithSidecar(t, false)
}

func newSnapshotRestoreSourceFixtureWithSidecar(t *testing.T, sidecar bool) snapshotRestoreSourceFixture {
	t.Helper()
	m, simulated, request := consumedRuntimeFixture(t)
	request.Request.MemSizeMiB = 1
	if sidecar {
		request.Request.Sidecars = []WorkloadSpec{{Name: "metrics", StorageKey: "sidecars/metrics.ext4"}}
		request.Request.ArtifactSources = append(request.Request.ArtifactSources, runtimeSourceFixture("sidecar-layer", "metrics", "sidecars/metrics.ext4", []byte("metrics")))
		request.Binding.ArtifactSourcesHash, _ = runtimeadmission.HashArtifactSources(request.Request.ArtifactSources)
	}
	request.NativeInputHash, _ = NativeWakeInputHash(request.Request)
	_, parent, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := state.SnapshotCaptureMemKey(parent.Binding.DeploymentID, state.SnapshotTierInit, uuid.NewString())
	bodies := map[string][]byte{
		request.Request.BaseKey: []byte("base"), request.Request.LayerKey: []byte("main"),
		key: bytes.Repeat([]byte{0x42}, 1<<20),
		state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}): []byte("captured VM state"),
		state.SnapshotDriveKey(state.Snapshot{StorageKey: key}):   []byte("live"),
	}
	if sidecar {
		bodies["sidecars/metrics.ext4"] = []byte("metrics")
	}
	artifact := func(key string) runtimeadmission.CapturedArtifact {
		source := runtimeSourceFixture("full-rootfs", "", key, bodies[key])
		return runtimeadmission.CapturedArtifact{StorageKey: key, Digest: source.Digest, Bytes: source.Bytes}
	}
	capture := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: parent, Memory: artifact(key), VMState: artifact(state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})), PrivateDrive: artifact(state.SnapshotDriveKey(state.Snapshot{StorageKey: key})), CapturedAtUnixNano: time.Now().UnixNano()}
	binding := parent.Binding
	binding.Token, binding.InstanceID = uuid.NewString(), uuid.NewString()
	binding.NodeID, binding.Incarnation = uuid.NewString(), uuid.NewString()
	binding.CapturedInputHash = strings.Repeat("9", 64)
	binding.IssuedAtUnixNano, binding.ExpiresAtUnixNano = time.Now().UnixNano(), time.Now().Add(time.Minute).UnixNano()
	backend := &runtimeSourceTestBackend{data: bodies}
	parentDir := t.TempDir()
	if err := os.Chmod(parentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	vmm := (&JailerVMM{storage: backend}).WithRuntimeSourceRoot(parentDir)
	lease := Lease{Instance: binding.InstanceID, UID: os.Getuid(), GID: os.Getgid(), Slot: 2}
	req := SnapshotRestoreInputs{Binding: binding, Capture: capture, Sources: request.Request.ArtifactSources, Runtime: simulated.spec, Snapshot: Snapshot{DeploymentID: binding.DeploymentID, StorageKey: key, VMStateStorageKey: capture.VMState.StorageKey, MemBytes: capture.Memory.Bytes, FCVersion: m.fcVersion}}
	t.Cleanup(func() { _ = vmm.releaseRuntimeSources(lease.Instance) })
	return snapshotRestoreSourceFixture{vmm: vmm, backend: backend, lease: lease, request: req}
}

func stageSnapshotRestoreDrives(t *testing.T, f snapshotRestoreSourceFixture, prepared VerifiedSnapshotInputs) (string, VMConfig) {
	t.Helper()
	root := t.TempDir()
	config := BuildColdBootConfig(f.request.Runtime, f.lease.Slot)
	for i := range config.Drives {
		drive := &config.Drives[i]
		body, err := os.ReadFile(prepared.drives[drive.DriveID])
		if err != nil {
			t.Fatal(err)
		}
		drive.PathOnHost = drive.DriveID + ".ext4"
		if err := os.WriteFile(filepath.Join(root, drive.PathOnHost), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, config
}

func TestSnapshotRestoreSourcesSealHistoricalFilesAndPreserveProducerLineage(t *testing.T) {
	f := newSnapshotRestoreSourceFixture(t)
	prepared, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, key string }{{prepared.memory, f.request.Capture.Memory.StorageKey}, {prepared.vmstate, f.request.Capture.VMState.StorageKey}, {prepared.privateDrive, f.request.Capture.PrivateDrive.StorageKey}} {
		body, err := os.ReadFile(tc.path)
		if err != nil || !bytes.Equal(body, f.backend.data[tc.key]) {
			t.Fatal("captured bytes were replaced by a mutable locator", err)
		}
		info, err := os.Stat(tc.path)
		if err != nil || info.Mode().Perm() != 0o444 {
			t.Fatal("captured input not protected", err)
		}
		f.backend.data[tc.key] = []byte("storage now has different bytes")
		got, err := os.ReadFile(tc.path)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatal("backend mutation reached protected capture", err)
		}
	}
	root, config := stageSnapshotRestoreDrives(t, f, prepared)
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, root, config); err != nil {
		t.Fatal(err)
	}
	handoff, err := f.vmm.runtimeDriveHandoff(f.lease)
	if err != nil || handoff == nil {
		t.Fatal("missing prepared handoff", err)
	}
	main := handoff.drives[1].observation
	if main.Producer.Digest != f.request.Sources[1].Digest || main.Producer.Digest == f.request.Capture.PrivateDrive.Digest || main.Source.StorageKey != f.request.Sources[1].StorageKey {
		t.Fatal("captured private bytes replaced approved producer lineage")
	}
	if err := os.WriteFile(filepath.Join(root, config.Drives[1].PathOnHost), []byte("env!"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.vmm.measureFinalRuntimeDrives(t.Context(), f.lease, root, body); err != nil {
		t.Fatal(err)
	}
	main = handoff.drives[1].observation
	if main.Producer.Digest != f.request.Sources[1].Digest || main.Injected.Digest == main.Producer.Digest || main.Injected.Digest == f.request.Capture.PrivateDrive.Digest {
		t.Fatal("post-restore staging lost producer/capture/injected distinctions")
	}
	if _, err := f.vmm.ObservedRuntimeDrives(t.Context(), f.lease); err == nil {
		t.Fatal("preparation fabricated a native process receipt")
	}
	f.request.Capture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-mutated"
	if handoff.restoreCapture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey == "caller-mutated" {
		t.Fatal("caller aliased retained lineage")
	}
	if err := f.vmm.releaseRuntimeSources(f.lease.Instance); err != nil {
		t.Fatal(err)
	}
	if len(f.vmm.runtimeDriveHandoffs) != 0 || f.vmm.runtimeSources().root != "" {
		t.Fatal("prepared restore sources leaked")
	}
	if _, err := os.Stat(prepared.memory); !os.IsNotExist(err) {
		t.Fatal("retirement kept protected memory", err)
	}
}

func TestSnapshotRestoreSourcesRefuseCorruptCompleteStreamsAndSweepEarlierInputs(t *testing.T) {
	for _, role := range []string{"base", "producer main", "memory", "vmstate", "private drive"} {
		for _, fault := range []string{"digest", "truncated", "overflow", "read", "close"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				f := newSnapshotRestoreSourceFixture(t)
				keys := map[string]string{"base": f.request.Sources[0].StorageKey, "producer main": f.request.Sources[1].StorageKey, "memory": f.request.Capture.Memory.StorageKey, "vmstate": f.request.Capture.VMState.StorageKey, "private drive": f.request.Capture.PrivateDrive.StorageKey}
				key, body := keys[role], f.backend.data[keys[role]]
				switch fault {
				case "digest":
					f.backend.data[key] = bytes.Repeat([]byte{0xff}, len(body))
				case "truncated":
					f.backend.data[key] = body[:len(body)-1]
				case "overflow":
					f.backend.data[key] = append(bytes.Clone(body), 0)
				case "read", "close":
					f.backend.open = func(_ context.Context, requested string) (io.ReadCloser, error) {
						if requested != key {
							return io.NopCloser(bytes.NewReader(f.backend.data[requested])), nil
						}
						reader := &runtimeSourceFaultReader{Reader: bytes.NewReader(body)}
						if fault == "read" {
							reader.readErr = io.ErrUnexpectedEOF
						} else {
							reader.closeErr = io.ErrUnexpectedEOF
						}
						return reader, nil
					}
				}
				prepared, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
				if err == nil || prepared.memory != "" || prepared.drives != nil {
					t.Fatal("corrupt restore input accepted", err)
				}
				expectedReads := map[string]int32{"base": 1, "producer main": 2, "memory": 3, "vmstate": 4, "private drive": 5}[role]
				if f.backend.gets.Load() != expectedReads {
					t.Fatalf("fault did not reach the selected stream: reads=%d want=%d err=%v", f.backend.gets.Load(), expectedReads, err)
				}
				cache := f.vmm.runtimeSources()
				if len(f.vmm.runtimeDriveHandoffs) != 0 || len(cache.entries) != 0 || len(cache.owners) != 0 || cache.root != "" {
					t.Fatal("failed preparation retained inputs")
				}
			})
		}
	}
}

func TestSnapshotRestoreSourcesCheckLayoutAndFreshGrantBeforeStorage(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*snapshotRestoreSourceFixture)
	}{
		{"memory size", func(f *snapshotRestoreSourceFixture) { f.request.Runtime.MemSizeMiB++ }},
		{"catalog memory size", func(f *snapshotRestoreSourceFixture) { f.request.Snapshot.MemBytes++ }},
		{"old instance", func(f *snapshotRestoreSourceFixture) {
			f.request.Binding.InstanceID = f.request.Capture.Parent.Binding.InstanceID
			f.lease.Instance = f.request.Binding.InstanceID
		}},
		{"node lease", func(f *snapshotRestoreSourceFixture) { f.lease.Instance = uuid.NewString() }},
		{"stale cache", func(f *snapshotRestoreSourceFixture) { f.request.Snapshot.Stale = true }},
		{"networkless", func(f *snapshotRestoreSourceFixture) { f.request.Snapshot.Networkless = true }},
		{"wrong deployment", func(f *snapshotRestoreSourceFixture) { f.request.Snapshot.DeploymentID = uuid.NewString() }},
		{"wrong source", func(f *snapshotRestoreSourceFixture) { f.request.Runtime.LayerKey = "other/main.ext4" }},
		{"changed drive ID", func(f *snapshotRestoreSourceFixture) {
			f.request.Runtime.LayerKey = ""
			f.request.Runtime.Workloads = []WorkloadSpec{{Name: "main", StorageKey: f.request.Sources[1].StorageKey, DriveID: "different-main"}}
		}},
		{"expired", func(f *snapshotRestoreSourceFixture) {
			f.request.Binding.IssuedAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			f.request.Binding.ExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSnapshotRestoreSourceFixture(t)
			tc.edit(&f)
			if _, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request); err == nil {
				t.Fatal("invalid request prepared files")
			}
			if f.backend.gets.Load() != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 {
				t.Fatal("invalid request touched storage or registered owner")
			}
		})
	}
}

func TestSnapshotRestoreSourcesTeardownCancelsAndJoinsBlockedPreparation(t *testing.T) {
	f := newSnapshotRestoreSourceFixture(t)
	reader, writer := io.Pipe()
	defer writer.Close()
	opened := make(chan struct{})
	f.backend.open = func(_ context.Context, key string) (io.ReadCloser, error) {
		if key == f.request.Capture.Memory.StorageKey {
			close(opened)
			return reader, nil
		}
		return io.NopCloser(bytes.NewReader(f.backend.data[key])), nil
	}
	result := make(chan error, 1)
	go func() { _, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request); result <- err }()
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not reach memory stream")
	}
	retired := make(chan error, 1)
	go func() { retired <- f.vmm.releaseRuntimeSources(f.lease.Instance) }()
	select {
	case err := <-retired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("teardown did not join source preparation")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("wrong cancelled preparation result", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not finish")
	}
	cache := f.vmm.runtimeSources()
	if cache.root != "" || len(cache.entries) != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 {
		t.Fatal("cancelled preparation leaked owned files")
	}
}

func TestSnapshotRestoreSourcesRefuseReusedOwnerWithoutRemovingItsFiles(t *testing.T) {
	f := newSnapshotRestoreSourceFixture(t)
	prepared, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
	if err != nil {
		t.Fatal(err)
	}
	f.request.Binding.Token = uuid.NewString()
	if _, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("existing native owner replaced", err)
	}
	if _, err := os.Stat(prepared.memory); err != nil {
		t.Fatal("refused replay deleted existing owner's files", err)
	}
}

func TestSnapshotRestoreSourcesPreserveSidecarMembershipAndIndependentWritableCopies(t *testing.T) {
	f := newSnapshotRestoreSourceFixtureWithSidecar(t, true)
	one, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
	if err != nil {
		t.Fatal(err)
	}
	firstRoot, firstConfig := stageSnapshotRestoreDrives(t, f, one)
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, firstRoot, firstConfig); err != nil {
		t.Fatal(err)
	}
	oldLease := f.lease
	f.request.Binding.Token, f.request.Binding.InstanceID = uuid.NewString(), uuid.NewString()
	f.lease.Instance = f.request.Binding.InstanceID
	t.Cleanup(func() { _ = f.vmm.releaseRuntimeSources(f.lease.Instance) })
	two, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, secondConfig := stageSnapshotRestoreDrives(t, f, two)
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, secondRoot, secondConfig); err != nil {
		t.Fatal(err)
	}
	if one.memory != two.memory || one.drives[DriveBase] != two.drives[DriveBase] || f.backend.gets.Load() != 6 {
		t.Fatal("independent consumers duplicated immutable snapshot/base inputs")
	}
	firstMain, secondMain := filepath.Join(firstRoot, firstConfig.Drives[1].PathOnHost), filepath.Join(secondRoot, secondConfig.Drives[1].PathOnHost)
	a, err := os.Stat(firstMain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(secondMain)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(a, b) {
		t.Fatal("restored main drives share a writable inode")
	}
	if err := os.WriteFile(firstMain, []byte("one!"), 0o600); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(secondMain)
	if err != nil || string(unchanged) != "live" {
		t.Fatal("one consumer changed another's main drive", err)
	}
	if err := f.vmm.releaseRuntimeSources(oldLease.Instance); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(two.memory); err != nil {
		t.Fatal("first retirement removed the second consumer's inputs", err)
	}
	if err := f.vmm.releaseRuntimeSources(f.lease.Instance); err != nil {
		t.Fatal(err)
	}
	if f.vmm.runtimeSources().root != "" {
		t.Fatal("last retirement leaked snapshot inputs")
	}
}

func TestSnapshotRestoreSourcesRejectRenamedSidecarAndBorrowedPrivateBytes(t *testing.T) {
	f := newSnapshotRestoreSourceFixtureWithSidecar(t, true)
	f.request.Runtime.Workloads[1].Name = "renamed"
	if _, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request); err == nil || f.backend.gets.Load() != 0 {
		t.Fatal("renamed sidecar bypassed exact source membership", err)
	}
	f = newSnapshotRestoreSourceFixture(t)
	prepared, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
	if err != nil {
		t.Fatal(err)
	}
	root, config := stageSnapshotRestoreDrives(t, f, prepared)
	if err := os.WriteFile(filepath.Join(root, config.Drives[1].PathOnHost), []byte("main"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, root, config); !errors.Is(err, runtimeadmission.ErrInvalid) {
		t.Fatal("approved producer substituted for the captured writable drive", err)
	}
}

func TestSnapshotRestoreSourcesExpireWhileStreamingAndReleaseProtectedFiles(t *testing.T) {
	f := newSnapshotRestoreSourceFixture(t)
	f.request.Binding.ExpiresAtUnixNano = time.Now().Add(time.Second).UnixNano()
	f.backend.open = func(ctx context.Context, key string) (io.ReadCloser, error) {
		if key == f.request.Capture.Memory.StorageKey {
			timer := time.NewTimer(time.Until(time.Unix(0, f.request.Binding.ExpiresAtUnixNano)) + 10*time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return io.NopCloser(bytes.NewReader(f.backend.data[key])), nil
	}
	prepared, err := f.vmm.PrepareSnapshotRestoreInputs(t.Context(), f.lease, f.request)
	if !errors.Is(err, runtimeadmission.ErrExpired) || prepared.memory != "" || prepared.drives != nil {
		t.Fatal("expired preparation returned protected restore inputs", err)
	}
	if f.vmm.runtimeSources().root != "" || len(f.vmm.runtimeDriveHandoffs) != 0 {
		t.Fatal("expiry leaked protected inputs")
	}
}
