// adr: 593
package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// The historical parent and HTTP acknowledgment are simulated. Staging,
// hashing, inode isolation, descriptor ownership and cancellation are real I/O.
// These fixtures deliberately have no Firecracker process or consumed RAM.
type protectedRestoreFixture struct {
	snapshotRestoreSourceFixture
	ctx    context.Context
	spec   RestoreSpec
	flight *snapshotSourceFlight
	root   string
	config VMConfig
}

func bindSnapshotRestoreFixture(t *testing.T, f *snapshotRestoreSourceFixture) {
	t.Helper()
	parts := strings.Split(f.request.Capture.Memory.StorageKey, "/")
	f.request.Binding.SnapshotCaptureToken = parts[len(parts)-3]
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion,
		CaptureToken: f.request.Binding.SnapshotCaptureToken, FCVersion: f.request.Snapshot.FCVersion, Capture: f.request.Capture}
	var err error
	f.request.Binding.SnapshotEvidenceHash, err = e.Hash()
	if err != nil {
		t.Fatal(err)
	}
}

func newProtectedRestoreFixture(t *testing.T, sidecar, paused bool) protectedRestoreFixture {
	t.Helper()
	f := newSnapshotRestoreSourceFixtureWithSidecar(t, sidecar)
	f.vmm = NewJailerVMM(t.TempDir(), time.Second).WithStorage(f.backend).WithRuntimeSourceRoot(f.vmm.runtimeSourceRoot)
	return prepareProtectedRestoreFixture(t, f, paused)
}

func prepareProtectedRestoreFixture(t *testing.T, f snapshotRestoreSourceFixture, paused bool) protectedRestoreFixture {
	t.Helper()
	t.Cleanup(func() {
		_ = f.vmm.releaseRuntimeSources(f.lease.Instance)
		_ = f.vmm.sweepMaterialised(f.lease.Instance)
	})
	bindSnapshotRestoreFixture(t, &f)
	ctx, spec, flight, err := f.vmm.prepareVerifiedSnapshotLoad(t.Context(), f.lease, f.request, paused)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(flight.finish)
	if err := f.vmm.beginProtectedNativeRestore(ctx, f.lease, spec); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	config := BuildColdBootConfig(spec.verifiedSnapshot.request.Runtime, f.lease.Slot)
	for i := range config.Drives {
		config.Drives[i].PathOnHost = spec.verifiedSnapshot.inputs.drives[config.Drives[i].DriveID]
	}
	kernel := filepath.Join(t.TempDir(), "vmlinux")
	if err := os.WriteFile(kernel, []byte("platform kernel fixture"), 0o444); err != nil {
		t.Fatal(err)
	}
	config.BootSource.KernelImagePath = kernel
	config, err = f.vmm.provisionForOwner(ctx, nativeLaunchRecord{}, root, config, f.lease.UID, f.lease.GID, f.lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.vmm.pinVerifiedSnapshotDrives(ctx, f.lease, root, spec); err != nil {
		t.Fatal(err)
	}
	for _, blob := range []struct{ path, name string }{{spec.verifiedSnapshot.inputs.memory, memSnapshotName}, {spec.verifiedSnapshot.inputs.vmstate, vmstateSnapshotName}} {
		if _, err := f.vmm.stageReadOnlyAsForOwner(ctx, nativeLaunchRecord{}, root, blob.path, blob.name, f.lease.Instance); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.vmm.pinVerifiedSnapshotBlobs(ctx, f.lease, root, spec, memSnapshotName, vmstateSnapshotName); err != nil {
		t.Fatal(err)
	}
	return protectedRestoreFixture{snapshotRestoreSourceFixture: f, ctx: ctx, spec: spec, flight: flight, root: root, config: config}
}

func TestProtectedSnapshotLoadResolutionFetchesOnlyPlatformKernel(t *testing.T) {
	source := newSnapshotRestoreSourceFixtureWithSidecar(t, true)
	source.vmm = NewJailerVMM(t.TempDir(), time.Second).WithStorage(source.backend).WithRuntimeSourceRoot(source.vmm.runtimeSourceRoot)
	source.request.Runtime.KernelKey = "kernel/platform-fixture"
	f := prepareProtectedRestoreFixture(t, source, false)
	f.backend.data[f.spec.KernelKey] = []byte("platform kernel")
	reads := f.backend.gets.Load()
	artifacts := []restoreArtifactSpec{{artifact: "kernel", key: f.spec.KernelKey}}
	plan := f.spec.verifiedSnapshot
	for i, drive := range BuildColdBootConfig(plan.request.Runtime, f.lease.Slot).Drives {
		role, key := "base", drive.PathOnHost
		if i == 1 {
			role, key = "main", plan.request.Capture.PrivateDrive.StorageKey
		} else if i > 1 {
			role = "sidecar:" + plan.request.Runtime.Workloads[i-1].Name
		}
		artifacts = append(artifacts, restoreArtifactSpec{artifact: role, key: key})
	}
	resolved, err := f.vmm.resolveRestoreArtifactsForInputs(f.ctx, f.lease, f.spec, artifacts)
	if err != nil || len(resolved) != len(artifacts) || f.backend.gets.Load() != reads+1 {
		t.Fatalf("protected resolution err=%v artifacts=%d want=%d storage_reads=%d want=%d", err, len(resolved), len(artifacts), f.backend.gets.Load(), reads+1)
	}
	for i, drive := range BuildColdBootConfig(plan.request.Runtime, f.lease.Slot).Drives {
		if resolved[i+1].path != plan.inputs.drives[drive.DriveID] || resolved[i+1].Source != "verified" {
			t.Fatal("mutable artifact path reached protected restore")
		}
	}
	for _, tc := range []struct{ artifact, key, path string }{{"mem", f.spec.StorageKey, plan.inputs.memory}, {"vmstate", f.spec.VMStateStorageKey, plan.inputs.vmstate}} {
		path, timing, err := f.vmm.resolveRestoreBlobForInputs(f.ctx, f.lease, f.spec, tc.artifact, tc.key, "")
		if err != nil || path != tc.path || timing.Source != "verified" || f.backend.gets.Load() != reads+1 {
			t.Fatal("snapshot blob bypassed protected inputs", err)
		}
		if _, _, err := f.vmm.resolveRestoreBlobForInputs(f.ctx, f.lease, f.spec, tc.artifact, tc.key, "/caller/fallback"); err == nil {
			t.Fatal("caller fallback accepted")
		}
	}
}

func TestProtectedSnapshotLoadActualStagingSharesImmutableFilesAndIsolatesWritableCopies(t *testing.T) {
	one := newProtectedRestoreFixture(t, true, false)
	source := one.snapshotRestoreSourceFixture
	source.request.Binding.InstanceID, source.request.Binding.Token = uuid.NewString(), uuid.NewString()
	source.lease.Instance, source.lease.Slot = source.request.Binding.InstanceID, source.lease.Slot+1
	reads := source.backend.gets.Load()
	two := prepareProtectedRestoreFixture(t, source, true)
	first, second := one.spec.verifiedSnapshot.inputs.owner, two.spec.verifiedSnapshot.inputs.owner
	if !os.SameFile(first.drives[0].info, second.drives[0].info) || !os.SameFile(first.drives[2].info, second.drives[2].info) || os.SameFile(first.drives[1].info, second.drives[1].info) || !os.SameFile(first.restoreBlobs[0].info, second.restoreBlobs[0].info) || source.backend.gets.Load() != reads {
		t.Fatal("actual staging lost immutable sharing or private writable isolation")
	}
	one.flight.finish()
	if err := one.vmm.releaseRuntimeSources(one.lease.Instance); err != nil {
		t.Fatal(err)
	}
	if _, err := second.restoreBlobs[0].file.Stat(); err != nil {
		t.Fatal("first teardown closed second lease's pin", err)
	}
	if _, err := os.Stat(two.spec.verifiedSnapshot.inputs.memory); err != nil {
		t.Fatal("first teardown removed shared snapshot", err)
	}
	if err := two.vmm.checkPinnedSnapshotBlobs(two.ctx, two.root, two.spec.verifiedSnapshot); err != nil {
		t.Fatal("remaining protected input is stale", err)
	}
}

type protectedRestoreTransport struct {
	calls atomic.Int32
	run   func(*http.Request) (*http.Response, error)
}

func (p *protectedRestoreTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p.calls.Add(1)
	return p.run(req)
}

func installProtectedRestoreTransport(f protectedRestoreFixture, run func(*http.Request) (*http.Response, error)) *protectedRestoreTransport {
	transport := &protectedRestoreTransport{run: run}
	f.vmm.clients = map[string]*http.Client{f.lease.Instance: {Transport: transport}}
	return transport
}

func protectedRestoreResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
}

func protectedRestoreBody(paused bool) map[string]any {
	return map[string]any{"snapshot_path": vmstateSnapshotName,
		"mem_backend": map[string]any{"backend_type": "File", "backend_path": memSnapshotName}, "resume_vm": !paused}
}

func TestProtectedSnapshotLoadUsesPinnedBytesAndActualCommandHash(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "serving", true: "paused"}[paused], func(t *testing.T) {
			f := newProtectedRestoreFixture(t, true, paused)
			for key := range f.backend.data {
				f.backend.data[key] = []byte("locator changed after preparation")
			}
			if err := os.WriteFile(filepath.Join(f.root, layerImageName), []byte("env!"), 0o600); err != nil {
				t.Fatal(err)
			}
			body := protectedRestoreBody(paused)
			expected, _ := json.Marshal(body)
			transport := installProtectedRestoreTransport(f, func(req *http.Request) (*http.Response, error) {
				got, err := io.ReadAll(req.Body)
				if err != nil || req.Method != http.MethodPut || req.URL.Path != "/snapshot/load" || !bytes.Equal(got, expected) {
					t.Error("load command differed from the measured command", err)
				}
				return protectedRestoreResponse(http.StatusNoContent), nil
			})
			if err := f.vmm.loadRestoredSnapshot(f.ctx, f.lease, f.root, f.spec, body); err != nil {
				t.Fatal(err)
			}
			plan, handoff := f.spec.verifiedSnapshot, f.spec.verifiedSnapshot.inputs.owner
			hash := sha256.Sum256(expected)
			if !plan.accepted || handoff.observation.ConfigHash != hex.EncodeToString(hash[:]) || handoff.observation.ProcessPID != 0 || transport.calls.Load() != 1 {
				t.Fatal("acknowledgment lost exact command identity or invented process consumption")
			}
			main := handoff.drives[1].observation
			if main.Producer.Digest != f.request.Sources[1].Digest || main.Injected.Digest == main.Producer.Digest || main.Injected.Digest == f.request.Capture.PrivateDrive.Digest {
				t.Fatal("producer, capture and freshly injected private bytes were collapsed")
			}
			if f.config.Drives[2].PathOnHost != snapshotRestoreSidecarName(f.spec, 1, "cache/random-name") {
				t.Fatal("restore drive names differ from real cold provisioning")
			}
			if _, err := f.vmm.ObservedRuntimeDrives(f.ctx, f.lease); err == nil {
				t.Fatal("an HTTP acknowledgment fabricated native consumption")
			}
			if err := f.vmm.loadRestoredSnapshot(f.ctx, f.lease, f.root, f.spec, body); !errors.Is(err, runtimeadmission.ErrReplay) || transport.calls.Load() != 1 {
				t.Fatal("accepted load replay reached the API", err)
			}
			pinned := handoff.restoreBlobs[0].file
			f.flight.finish()
			if err := f.vmm.releaseRuntimeSources(f.lease.Instance); err != nil {
				t.Fatal(err)
			}
			if _, err := pinned.Stat(); !errors.Is(err, os.ErrClosed) || handoff.restoreLoad != nil || handoff.restoreBlobs != nil {
				t.Fatal("snapshot load teardown retained protected descriptors", err)
			}
		})
	}
}

func TestProtectedSnapshotLoadRefusesStagedReplacementAndMutationBeforeAPI(t *testing.T) {
	for _, name := range []string{memSnapshotName, vmstateSnapshotName, baseImageName, layerImageName, sidecarDriveImageName(1)} {
		for _, fault := range []string{"replace", "modify", "symlink"} {
			t.Run(name+"/"+fault, func(t *testing.T) {
				f := newProtectedRestoreFixture(t, true, false)
				path := filepath.Join(f.root, name)
				if fault == "replace" || fault == "symlink" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if fault == "replace" {
						if err := os.WriteFile(path, []byte("bad!"), 0o444); err != nil {
							t.Fatal(err)
						}
					} else if err := os.Symlink(f.spec.verifiedSnapshot.inputs.memory, path); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Chmod(path, 0o644); err != nil {
						t.Fatal(err)
					}
					file, err := os.OpenFile(path, os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, err = file.WriteAt([]byte("bad!"), 0)
					if closeErr := file.Close(); err != nil || closeErr != nil {
						t.Fatal(err, closeErr)
					}
					if err := os.Chmod(path, 0o444); err != nil {
						t.Fatal(err)
					}
				}
				transport := installProtectedRestoreTransport(f, func(*http.Request) (*http.Response, error) { return protectedRestoreResponse(204), nil })
				err := f.vmm.loadRestoredSnapshot(f.ctx, f.lease, f.root, f.spec, protectedRestoreBody(false))
				// A main drive is intentionally re-injected in place; its digest may
				// change. Replacing its inode, or any immutable input, is refused.
				if name == layerImageName && fault == "modify" {
					if err != nil || transport.calls.Load() != 1 {
						t.Fatal("private in-place injection was rejected", err)
					}
				} else if err == nil || transport.calls.Load() != 0 || f.spec.verifiedSnapshot.accepted {
					t.Fatal("changed immutable input or replacement reached native API", err)
				}
			})
		}
	}
}

func TestProtectedSnapshotLoadRefusesInvalidAuthorityBeforeStorage(t *testing.T) {
	for _, fault := range []string{"missing", "hash", "token", "expiry", "builder", "task", "skip", "custom-sidecar"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotRestoreSourceFixtureWithSidecar(t, true)
			bindSnapshotRestoreFixture(t, &f)
			switch fault {
			case "missing":
				f.request.Binding.SnapshotCaptureToken, f.request.Binding.SnapshotEvidenceHash = "", ""
			case "hash":
				f.request.Binding.SnapshotEvidenceHash = strings.Repeat("f", 64)
			case "token":
				f.request.Binding.SnapshotCaptureToken = f.request.Binding.Token
			case "expiry":
				f.request.Binding.ExpiresAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			case "builder":
				f.lease.IsBuilder = true
			case "task":
				f.request.Runtime.AppTask = true
			case "skip":
				f.request.Runtime.SkipReady = true
			case "custom-sidecar":
				f.request.Runtime.Workloads[1].DriveID = "unrecorded-path"
			}
			if err := f.vmm.RestoreSnapshotVerified(t.Context(), f.lease, f.request, false); err == nil || f.backend.gets.Load() != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 {
				t.Fatal("invalid grant acquired storage or native ownership", err)
			}
		})
	}
}

func TestProtectedSnapshotLoadRefusesChangedSpecOwnerAndCommand(t *testing.T) {
	for _, fault := range []string{"lease", "spec-key", "pause", "command", "memory", "resume", "unknown", "injection", "workload", "legacy", "expired", "canceled", "reenter"} {
		t.Run(fault, func(t *testing.T) {
			f := newProtectedRestoreFixture(t, true, false)
			lease, spec, body, ctx := f.lease, f.spec, protectedRestoreBody(false), f.ctx
			switch fault {
			case "lease":
				lease.UID++
			case "spec-key":
				spec.StorageKey = "snap/unrelated/mem"
			case "pause":
				spec.KeepPaused = true
			case "command":
				body["snapshot_path"] = "caller-path"
			case "memory":
				body["mem_backend"].(map[string]any)["backend_path"] = "caller-memory"
			case "resume":
				body["resume_vm"] = false
			case "unknown":
				body["caller_extension"] = true
			case "injection":
				spec.SecretsEnvJSON = []byte("changed injection")
			case "workload":
				spec.Workloads[1].Cmd = []string{"changed command"}
			case "legacy":
				spec.verifiedSnapshot = nil
			case "expired":
				spec.verifiedSnapshot.request.Binding.ExpiresAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			transport := installProtectedRestoreTransport(f, func(*http.Request) (*http.Response, error) { return protectedRestoreResponse(204), nil })
			var err error
			if fault == "legacy" || fault == "reenter" {
				err = f.vmm.beginProtectedNativeRestore(ctx, lease, spec)
			} else {
				err = f.vmm.loadRestoredSnapshot(ctx, lease, f.root, spec, body)
			}
			if err == nil || transport.calls.Load() != 0 || f.spec.verifiedSnapshot.accepted {
				t.Fatal("changed authority or replay reached load", err)
			}
		})
	}
}

func TestProtectedSnapshotLoadDoesNotConvertAPIRefusalOrPostAckMutationIntoReceipt(t *testing.T) {
	for _, fault := range []string{"refusal", "after-ack"} {
		t.Run(fault, func(t *testing.T) {
			f := newProtectedRestoreFixture(t, false, false)
			transport := installProtectedRestoreTransport(f, func(*http.Request) (*http.Response, error) {
				if fault == "refusal" {
					return protectedRestoreResponse(http.StatusBadRequest), nil
				}
				if err := os.Remove(filepath.Join(f.root, vmstateSnapshotName)); err != nil {
					t.Error(err)
				}
				return protectedRestoreResponse(http.StatusNoContent), nil
			})
			body := protectedRestoreBody(false)
			if err := f.vmm.loadRestoredSnapshot(f.ctx, f.lease, f.root, f.spec, body); err == nil || f.spec.verifiedSnapshot.accepted {
				t.Fatal("refused or unpinned snapshot load was accepted", err)
			}
			if err := f.vmm.loadRestoredSnapshot(f.ctx, f.lease, f.root, f.spec, body); !errors.Is(err, runtimeadmission.ErrReplay) || transport.calls.Load() != 1 {
				t.Fatal("failed load replay reached API", err)
			}
		})
	}
}

func TestProtectedSnapshotLoadTeardownJoinsBlockedCommandBeforeClosingPins(t *testing.T) {
	f := newProtectedRestoreFixture(t, false, false)
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	installProtectedRestoreTransport(f, func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		close(canceled)
		<-release
		return nil, req.Context().Err()
	})
	loaded := make(chan error, 1)
	go func() {
		err := f.vmm.loadRestoredSnapshot(f.ctx, f.lease, f.root, f.spec, protectedRestoreBody(false))
		f.flight.finish()
		loaded <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("command did not enter API")
	}
	pin := f.spec.verifiedSnapshot.inputs.owner.restoreBlobs[0].file
	done := make(chan error, 1)
	go func() { done <- f.vmm.releaseRuntimeSources(f.lease.Instance) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("teardown did not cancel load")
	}
	if _, err := pin.Stat(); err != nil {
		t.Fatal("pin closed before command exited", err)
	}
	select {
	case err := <-done:
		t.Fatal("teardown failed to join command", err)
	default:
	}
	close(release)
	select {
	case err := <-loaded:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("load did not leave")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("teardown did not join")
	}
	if _, err := pin.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("joined teardown retained pin", err)
	}
}

func TestProtectedSnapshotRuntimeCloneFreezesInjectionAndProbeInputs(t *testing.T) {
	original := ColdBootSpec{SecretsEnvJSON: []byte("secrets"), APIEnvJSON: []byte("env"), Workloads: []WorkloadSpec{{
		Cmd: []string{"run"}, SealedEnv: []SealedEnvEntry{{Ciphertext: []byte("sealed")}}, preparedEnvJSON: []byte("private"),
		StartupProbe: &api.SidecarProbe{Test: []string{"probe"}, ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(5 * time.Second)}},
	}}}
	copy := cloneSnapshotRestoreRuntime(original)
	original.SecretsEnvJSON[0], original.APIEnvJSON[0] = 'X', 'X'
	original.Workloads[0].Cmd[0], original.Workloads[0].StartupProbe.Test[0] = "changed", "changed"
	original.Workloads[0].StartupProbe.ImageTiming.IntervalNS = int64(time.Second)
	original.Workloads[0].SealedEnv[0].Ciphertext[0], original.Workloads[0].preparedEnvJSON[0] = 'X', 'X'
	if string(copy.SecretsEnvJSON) != "secrets" || string(copy.APIEnvJSON) != "env" || copy.Workloads[0].Cmd[0] != "run" || copy.Workloads[0].StartupProbe.Test[0] != "probe" || copy.Workloads[0].StartupProbe.ImageTiming.IntervalNS != int64(5*time.Second) || string(copy.Workloads[0].SealedEnv[0].Ciphertext) != "sealed" || string(copy.Workloads[0].preparedEnvJSON) != "private" {
		t.Fatal("caller mutation reached retained restore inputs")
	}
}
