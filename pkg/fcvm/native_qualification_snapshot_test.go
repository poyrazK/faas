//go:build linux || darwin

// adr: 568 — portable original-attempt capture tests; not native KVM acceptance.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type qualificationCaptureVMM struct {
	*recoveryVMMFixture
	capture func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error)
}

// This is a portable producer fixture, not a supported native export adapter.
func (v *qualificationCaptureVMM) checkEnvironmentQualificationSnapshotSupport() error { return nil }

func (v *qualificationCaptureVMM) SnapshotKeepAlive(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.capture(ctx, lease, spec)
}

func qualificationCaptureFixture(t *testing.T) (*Manager, *nativeQualificationJournal, state.EnvironmentQualificationExecution, context.Context, *qualificationCaptureVMM, *atomic.Int32) {
	t.Helper()
	m, v, frame, _, ctx := nativeQualificationManagerFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	incoming, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	lease := qualificationLease(frame.InstanceID)
	if err := j.owner.prepare(nativeQualificationContext(ctx, incoming), lease); err != nil {
		t.Fatal(err)
	}
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.Authorized, physical.PID, physical.StartTime = true, 42, 101
	if err := j.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.WithStorage(backend)
	m.instanceBacking = map[string]BackingIdentity{frame.InstanceID: {Version: 1, Kernel: "sha256:" + strings.Repeat("a", 64), Base: "sha256:" + strings.Repeat("b", 64)}}
	m.live[frame.InstanceID] = &Instance{Lease: lease, Method: WakeColdBoot, nativeGeneration: physical.Generation, AppID: frame.AppID, DeploymentID: frame.DeploymentID}
	// This fixture models an already-live original process, never a recovered
	// or newly booted VM. Fake PID/journal evidence is not native acceptance.
	m.nativeRecoveryReady = true
	calls := &atomic.Int32{}
	captureVMM := &qualificationCaptureVMM{recoveryVMMFixture: m.vmm.(*recoveryVMMFixture)}
	captureVMM.capture = func(ctx context.Context, got Lease, spec SnapshotSpec) (SnapshotInfo, error) {
		calls.Add(1)
		permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
		if !ok || permit.Physical != physical || permit.Incoming.Execution != frame || !permit.Capture.CompletedAt.IsZero() {
			return SnapshotInfo{}, errors.New("capture omitted original process-bound output capability")
		}
		started, err := j.readCapture(permit.Incoming)
		if err != nil || started != permit.Capture {
			return SnapshotInfo{}, errors.Join(err, errors.New("output capability preceded durable capture start"))
		}
		if !sameNativePhysicalLease(got, lease) || !spec.ResumeBeforePublish || spec.BeforeCheckpoint || spec.StageMemPath != "" || spec.VMStatePath != "" {
			return SnapshotInfo{}, errors.New("capture did not preserve original lease or storage-only specification")
		}
		for _, key := range []string{spec.StorageKey, spec.VMStateStorageKey, state.SnapshotDriveKey(state.Snapshot{StorageKey: spec.StorageKey})} {
			if err := backend.Put(ctx, key, bytes.NewReader([]byte("fixture capture"))); err != nil {
				return SnapshotInfo{}, err
			}
		}
		return SnapshotInfo{MemBytes: 1024, VMStateBytes: 64, StoredBytes: 12288}, nil
	}
	m.vmm = captureVMM
	return m, j, frame, ctx, captureVMM, calls
}

func TestNativeQualificationSnapshotCompletesOriginalImmutableCapture(t *testing.T) {
	m, j, frame, ctx, _, calls := qualificationCaptureFixture(t)
	if _, err := m.WarmSnapshot(ctx, frame.InstanceID, SnapshotSpec{}); err == nil || calls.Load() != 0 {
		t.Fatal("generic capture gained reserved qualification authority", err)
	}
	proof, err := m.CaptureEnvironmentQualification(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	incoming, err := j.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if proof != qualificationSnapshotProof(incoming, SnapshotInfo{MemBytes: 1024, VMStateBytes: 64, StoredBytes: 12288}) || proof.CaptureID == proof.NativeGeneration {
		t.Fatal("capture borrowed another identity or namespace")
	}
	for _, key := range []string{proof.StorageKey, proof.VMStateStorageKey, proof.DriveStorageKey, proof.BackingStorageKey} {
		body, err := m.storage.Get(ctx, key)
		if err != nil {
			t.Fatal("completed capture omitted coupled object", err)
		}
		_ = body.Close()
	}
	for range 2 {
		repeated, err := m.CaptureEnvironmentQualification(ctx, frame)
		if err != nil || repeated != proof || calls.Load() != 1 {
			t.Fatal("duplicate delivery rewrote immutable capture", err)
		}
	}
	if _, err := j.owner.records(ctx); err != nil {
		t.Fatal("capture broke native restart inventory", err)
	}
	// Restarted ownership may inspect the receipt but cannot recreate Manager.live.
	restarted := NewManager(nil, m.vmm, Paths{}, "test-fc", nil, nil).WithNativeQualificationNodeID(frame.NodeID)
	restarted.WithStorage(m.storage)
	if got, err := restarted.CaptureEnvironmentQualification(ctx, frame); err == nil || got != (state.EnvironmentQualificationSnapshot{}) || restarted.LiveCount() != 0 {
		t.Fatal("journal recovery manufactured a live qualified VM", err)
	}
}

func TestNativeQualificationSnapshotRealBackendRemainsUnavailableBeforeEffects(t *testing.T) {
	for _, backend := range []string{"native_jailer", "generic_native_fixture"} {
		t.Run(backend, func(t *testing.T) {
			m, j, frame, ctx, v, calls := qualificationCaptureFixture(t)
			m.vmm = v.recoveryVMMFixture
			if backend == "native_jailer" {
				m.vmm = v.jailer
			}
			proof, err := m.CaptureEnvironmentQualification(ctx, frame)
			if !errors.Is(err, state.ErrConflict) || proof != (state.EnvironmentQualificationSnapshot{}) || calls.Load() != 0 || m.hasInstanceOperation(frame.InstanceID) {
				t.Fatal("missing native producer capability entered capture", err)
			}
			path, _ := j.capturePath(frame.InstanceID)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsupported native backend recorded capture start", err)
			}
		})
	}
}

func TestNativeQualificationSnapshotRejectsChangedOriginalAuthorityBeforeEffects(t *testing.T) {
	for _, change := range []string{"frame", "cleanup", "unconfigured", "revoked", "unbound", "expired", "physical_generation", "physical_lease", "unauthorized", "live_generation", "live_lease", "live_app", "live_deployment", "task", "job", "paused", "backing", "storage", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			m, j, frame, ctx, _, calls := qualificationCaptureFixture(t)
			incoming, err := j.read(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			physical, err := j.owner.read(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "frame":
				frame.Attempt++
			case "cleanup":
				frame.CleanupToken = uuid.NewString()
			case "unconfigured":
				m.WithNativeQualificationNodeID("")
			case "revoked":
				incoming.Revoked = true
			case "unbound":
				incoming.NativeGeneration, incoming.NativeLease = "", Lease{}
			case "expired":
				incoming.AcceptedAt, incoming.Deadline = time.Now().Add(-2*time.Second), time.Now().Add(-time.Second)
			case "physical_generation":
				physical.Generation = uuid.NewString()
			case "physical_lease":
				physical.Lease.MemoryMaxMiB++
			case "unauthorized":
				physical.Authorized, physical.PID, physical.StartTime = false, 0, 0
			case "live_generation":
				m.live[frame.InstanceID].nativeGeneration = uuid.NewString()
			case "live_lease":
				m.live[frame.InstanceID].Lease.CPUMillicores++
			case "live_app":
				m.live[frame.InstanceID].AppID = uuid.NewString()
			case "live_deployment":
				m.live[frame.InstanceID].DeploymentID = uuid.NewString()
			case "task":
				m.live[frame.InstanceID].AppTaskOnly = true
			case "job":
				m.live[frame.InstanceID].IsJob = true
			case "paused":
				m.live[frame.InstanceID].Paused = true
			case "backing":
				delete(m.instanceBacking, frame.InstanceID)
			case "storage":
				m.storage = nil
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if err := j.write(incoming); err != nil {
				t.Fatal(err)
			}
			if err := j.owner.write(physical); err != nil {
				t.Fatal(err)
			}
			if got, err := m.CaptureEnvironmentQualification(ctx, frame); err == nil || got != (state.EnvironmentQualificationSnapshot{}) || calls.Load() != 0 {
				t.Fatal("changed original authority reached capture effects", err)
			}
			path, _ := j.capturePath(incoming.Execution.InstanceID)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected capture published start authority", err)
			}
		})
	}
}

func TestNativeQualificationSnapshotUncertaintyCannotRecapture(t *testing.T) {
	for _, failure := range []string{"capture", "resume", "bytes", "physical_changed", "physical_pid", "physical_start_time", "backing_publish"} {
		t.Run(failure, func(t *testing.T) {
			m, j, frame, ctx, v, calls := qualificationCaptureFixture(t)
			capture := v.capture
			v.capture = func(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
				info, err := capture(ctx, lease, spec)
				switch failure {
				case "capture":
					return SnapshotInfo{}, errors.New("capture publication failed")
				case "resume":
					v.fakeVMM.resumeErr = errors.New("resume failed")
				case "bytes":
					info.VMStateBytes = 0
				case "physical_changed", "physical_pid", "physical_start_time":
					physical, err := j.owner.read(frame.InstanceID)
					if err != nil {
						return SnapshotInfo{}, err
					}
					switch failure {
					case "physical_changed":
						physical.Generation = uuid.NewString()
					case "physical_pid":
						physical.PID++
					case "physical_start_time":
						physical.StartTime++
					}
					if err := j.owner.write(physical); err != nil {
						return SnapshotInfo{}, err
					}
				case "backing_publish":
					// A storage error only after memory/device/drive publication.
					backing := state.SnapshotBackingKey(state.Snapshot{StorageKey: spec.StorageKey})
					local, _, err := m.storage.(storage.LocalPathResolver).LocalPath(backing)
					if err != nil {
						return SnapshotInfo{}, err
					}
					if err := os.MkdirAll(local, 0o700); err != nil {
						return SnapshotInfo{}, err
					}
				}
				return info, err
			}
			if proof, err := m.CaptureEnvironmentQualification(ctx, frame); err == nil || proof != (state.EnvironmentQualificationSnapshot{}) {
				t.Fatal("uncertain capture returned completion evidence", err)
			}
			incoming, err := j.read(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			record, err := j.readCapture(incoming)
			if err != nil || !record.CompletedAt.IsZero() || record.Info != (SnapshotInfo{}) {
				t.Fatal("uncertain capture was durably promoted", err)
			}
			v.capture = capture
			v.fakeVMM.resumeErr = nil
			if proof, err := m.CaptureEnvironmentQualification(ctx, frame); err == nil || proof != (state.EnvironmentQualificationSnapshot{}) || calls.Load() != 1 {
				t.Fatal("retry overwrote uncertain original capture", err)
			}
		})
	}
}

func TestNativeQualificationSnapshotCancellationJoinsOriginalFlight(t *testing.T) {
	m, j, frame, ctx, v, _ := qualificationCaptureFixture(t)
	entered := make(chan struct{})
	v.capture = func(ctx context.Context, _ Lease, _ SnapshotSpec) (SnapshotInfo, error) {
		close(entered)
		<-ctx.Done()
		return SnapshotInfo{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, err := m.CaptureEnvironmentQualification(ctx, frame); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture never entered")
	}
	if err := m.cancelInFlightInstance(ctx, frame.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) || m.hasInstanceOperation(frame.InstanceID) {
		t.Fatal("stop did not cancel and join original capture", err)
	}
	incoming, err := j.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := j.readCapture(incoming)
	if err != nil || !record.CompletedAt.IsZero() {
		t.Fatal("cancelled capture gained completion", err)
	}
}

func TestNativeQualificationSnapshotRacingDeliveryCannotDuplicateEffects(t *testing.T) {
	m, _, frame, ctx, v, calls := qualificationCaptureFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	capture := v.capture
	v.capture = func(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
		close(entered)
		select {
		case <-release:
			return capture(ctx, lease, spec)
		case <-ctx.Done():
			return SnapshotInfo{}, ctx.Err()
		}
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := m.CaptureEnvironmentQualification(ctx, frame); first <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture never entered")
	}
	go func() { _, err := m.CaptureEnvironmentQualification(ctx, frame); second <- err }()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	// A caller racing the registered flight can be rejected, or replay the
	// completed receipt after the flight finishes. Neither starts a capture.
	_ = <-second
	if calls.Load() != 1 {
		t.Fatal("racing delivery started another physical capture")
	}
}

func TestNativeQualificationSnapshotDamagedJournalBlocksReplayAndRecovery(t *testing.T) {
	for _, damage := range []string{"identity", "missing", "duplicate", "unknown", "trailing", "symlink", "directory_symlink"} {
		t.Run(damage, func(t *testing.T) {
			m, j, frame, ctx, _, calls := qualificationCaptureFixture(t)
			if _, err := m.CaptureEnvironmentQualification(ctx, frame); err != nil {
				t.Fatal(err)
			}
			path, _ := j.capturePath(frame.InstanceID)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "identity":
				fields["native_generation"], _ = json.Marshal(uuid.NewString())
			case "missing":
				delete(fields, "completed_at")
			case "unknown":
				fields["qualified"] = json.RawMessage("true")
			}
			if damage == "identity" || damage == "missing" || damage == "unknown" {
				body, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "duplicate":
				body = bytes.Replace(body, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "trailing":
				body = append(body, []byte("{}")...)
			case "symlink", "directory_symlink":
				target := path
				if damage == "directory_symlink" {
					target = filepath.Dir(path)
				}
				original := filepath.Join(t.TempDir(), "original")
				if err := os.Rename(target, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, target); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "duplicate" || damage == "trailing" {
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if proof, err := m.CaptureEnvironmentQualification(ctx, frame); err == nil || proof != (state.EnvironmentQualificationSnapshot{}) || calls.Load() != 1 {
				t.Fatal("damaged capture replayed completion", err)
			}
			if _, err := j.owner.records(ctx); err == nil {
				t.Fatal("damaged capture was ignored by native recovery")
			}
		})
	}
}
