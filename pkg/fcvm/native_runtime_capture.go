// adr: 20261022184951 — ordinary park and warm captures bind their own durable start.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

// WithNativeRuntimeCapture opts ordinary park and warm captures into the
// native producer. It has no effect without native process recovery; vmmd
// refuses the setting on its own.
func (v *JailerVMM) WithNativeRuntimeCapture() *JailerVMM {
	v.nativeRuntimeCapture = true
	return v
}

func (v *JailerVMM) nativeRuntimeCaptureEnabled() bool {
	return v != nil && v.nativeRecovery != nil && v.nativeRuntimeCapture
}

// nativeRuntimeCaptureRequest carries the Manager-owned facts an ordinary park
// or warm capture needs. Only Manager.Park and Manager.WarmSnapshot set it;
// wire callers cannot supply one.
type nativeRuntimeCaptureRequest struct {
	DeploymentID     string
	NativeGeneration string
	FCVersion        string
	Backing          BackingIdentity
}

// nativeRuntimeCapture returns the request for an ordinary park or warm
// capture, or nil when the VMM keeps the legacy path or its native refusal.
// Admitted (protocol-2), migration and sealed (ADR-733) captures never carry
// one: the producer publishes only to the VMM's own backend.
func (m *Manager) nativeRuntimeCapture(inst *Instance, spec SnapshotSpec) *nativeRuntimeCaptureRequest {
	v, ok := m.vmm.(interface{ nativeRuntimeCaptureEnabled() bool })
	if !ok || !v.nativeRuntimeCaptureEnabled() || spec.admittedParent.Binding.ProtocolVersion != 0 || spec.admittedGrant != nil ||
		spec.functionMigration != nil || spec.functionMigrationGrant != nil || spec.Storage != nil {
		return nil
	}
	m.backingMu.Lock()
	backing := m.instanceBacking[inst.Lease.Instance]
	m.backingMu.Unlock()
	return &nativeRuntimeCaptureRequest{DeploymentID: inst.DeploymentID, NativeGeneration: inst.nativeGeneration,
		FCVersion: m.fcVersion, Backing: backing}
}

// nativeRuntimeCaptureAuthority is the runtime counterpart of a qualification
// incoming record: the live physical launch, the scheduler's fresh capture
// namespace and its deadline. It grants no qualification, restore or artifact
// retirement authority.
type nativeRuntimeCaptureAuthority struct {
	Version          int                                 `json:"version"`
	InstanceID       string                              `json:"instance_id"`
	DeploymentID     string                              `json:"deployment_id"`
	NativeGeneration string                              `json:"native_generation"`
	KernelBootID     string                              `json:"kernel_boot_id"`
	NativeLease      Lease                               `json:"native_lease"`
	Keys             nativeSnapshotPublicationObjectKeys `json:"keys"`
	AcceptedAt       time.Time                           `json:"accepted_at"`
	Deadline         time.Time                           `json:"deadline"`
}

func (a *nativeRuntimeCaptureAuthority) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, nativeQualificationJSONFields(reflect.TypeOf(*a)))
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["native_lease"], nativeJournalLeaseFields()); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["keys"], []string{"memory", "vm_state", "drive", "backing"}); err != nil {
		return err
	}
	type plain nativeRuntimeCaptureAuthority
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(a))
}

// nativeRuntimeCaptureID is the scheduler's capture UUID inside a v2 memory
// key (snap/<deployment>/[warm/]captures/<capture>/v2/mem).
func nativeRuntimeCaptureID(memory string) string {
	parts := strings.Split(memory, "/")
	if len(parts) == 7 && parts[2] == state.SnapshotTierWarm {
		parts = append(parts[:2:2], parts[3:]...)
	}
	if len(parts) != 6 || parts[2] != "captures" {
		return ""
	}
	return parts[3]
}

func nativeRuntimeCaptureKeys(memory string) nativeSnapshotPublicationObjectKeys {
	snapshot := state.Snapshot{StorageKey: memory}
	return nativeSnapshotPublicationObjectKeys{Memory: memory, VMState: state.SnapshotVMStateKey(snapshot),
		Drive: state.SnapshotDriveKey(snapshot), Backing: state.SnapshotBackingKey(snapshot)}
}

func (a nativeRuntimeCaptureAuthority) validate() error {
	if err := validateNativeJournalLease(a.NativeLease); err != nil {
		return err
	}
	if a.Version != 1 || a.NativeLease.Instance != a.InstanceID || a.NativeLease.IsBuilder ||
		!canonicalNativeHelperID(a.NativeGeneration) || !canonicalNativeHelperID(a.KernelBootID) {
		return errors.New("native runtime capture: original physical owner is incomplete")
	}
	if runtimeadmission.CheckSnapshotCaptureKeys(a.DeploymentID, a.Keys.Memory, a.Keys.VMState, a.Keys.Drive) != nil ||
		a.Keys != nativeRuntimeCaptureKeys(a.Keys.Memory) || !canonicalNativeHelperID(nativeRuntimeCaptureID(a.Keys.Memory)) {
		return errors.New("native runtime capture: a fresh v2 capture namespace is required")
	}
	if a.AcceptedAt.IsZero() || !a.AcceptedAt.Before(a.Deadline) || a.Deadline.Sub(a.AcceptedAt) > api.NativeRuntimeCaptureMaxDuration {
		return errors.New("native runtime capture: invalid capture deadline")
	}
	return nil
}

// The capture record keeps the qualification shape so every producer check is
// shared. Its identity comes from the runtime authority instead of an attempt.
func (r nativeQualificationCaptureRecord) validateRuntime(a nativeRuntimeCaptureAuthority) error {
	if !r.Info.Capture.IsZero() {
		return errors.New("native runtime capture: protocol-1 capture cannot carry standards evidence")
	}
	if r.Version != 1 || r.InstanceID != a.InstanceID || r.CaptureID != nativeRuntimeCaptureID(a.Keys.Memory) ||
		r.NativeGeneration == "" || r.NativeGeneration != a.NativeGeneration || r.KernelBootID != a.KernelBootID ||
		r.StartedAt.Before(a.AcceptedAt) || !r.StartedAt.Before(a.Deadline) {
		return errors.New("native runtime capture: capture differs from its original authority")
	}
	if strings.TrimSpace(r.FCVersion) == "" || len(r.FCVersion) > 256 || r.FCVersion != strings.TrimSpace(r.FCVersion) {
		return errors.New("native runtime capture: capture Firecracker version is incomplete")
	}
	if r.CompletedAt.IsZero() {
		if !r.Info.IsZero() || r.Backing != (BackingIdentity{}) {
			return errors.New("native runtime capture: incomplete capture carries completion evidence")
		}
	} else if r.CompletedAt.Before(r.StartedAt) || !r.CompletedAt.Before(a.Deadline) ||
		r.Info.MemBytes <= 0 || r.Info.VMStateBytes <= 0 || r.Info.StoredBytes <= 0 || !r.Backing.complete() {
		return errors.New("native runtime capture: capture completion evidence is incomplete")
	}
	return nil
}

type nativeRuntimeCaptureRecord struct {
	Version   int                              `json:"version"`
	Authority nativeRuntimeCaptureAuthority    `json:"authority"`
	Capture   nativeQualificationCaptureRecord `json:"capture"`
}

func (r *nativeRuntimeCaptureRecord) UnmarshalJSON(data []byte) error {
	if _, err := nativeJournalObjectFields(data, []string{"version", "authority", "capture"}); err != nil {
		return err
	}
	type plain nativeRuntimeCaptureRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func (r nativeRuntimeCaptureRecord) validate() error {
	if r.Version != 1 {
		return errors.New("native runtime capture: unsupported record version")
	}
	return errors.Join(r.Authority.validate(), r.Capture.validateRuntime(r.Authority))
}

// One record per live instance. A completed record may be replaced by the
// next capture of the same VM; an incomplete one is never replaced, promoted or
// replayed. Its VM is retired through the native stop path.
type nativeRuntimeCaptureJournal struct {
	root  string
	owner *nativeLaunchJournal
	now   func() time.Time
}

func (j *nativeLaunchJournal) runtimeCaptures() *nativeRuntimeCaptureJournal {
	return &nativeRuntimeCaptureJournal{root: filepath.Join(j.root, "runtime-captures"), owner: j}
}

func (j *nativeRuntimeCaptureJournal) clock() time.Time {
	if j.now != nil {
		return j.now()
	}
	return time.Now()
}

// Files use the launch journal's exact instance name; alternate spellings
// cannot address another VM's record.
func (j *nativeRuntimeCaptureJournal) key(instance string) (string, error) {
	if !validNativeInstanceName(instance) {
		return "", errors.New("native runtime capture: invalid instance identity")
	}
	return instance, nil
}

// Lock order is runtime capture, then physical launch, as for qualification
// incoming authority. Teardown never takes this lock.
func (j *nativeRuntimeCaptureJournal) lock(ctx context.Context, instance string) (*os.File, error) {
	key, err := j.key(instance)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(j.root, 0o700); err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return nil, err
	}
	return lockNativeJournalFile(ctx, filepath.Join(j.root, key+".lock"))
}

func (j *nativeRuntimeCaptureJournal) path(instance string) (string, error) {
	key, err := j.key(instance)
	return filepath.Join(j.root, key+".json"), err
}

func (j *nativeRuntimeCaptureJournal) read(instance string) (record nativeRuntimeCaptureRecord, result error) {
	path, err := j.path(instance)
	if err != nil {
		return record, err
	}
	if err := errors.Join(checkNativeJournalPath(j.root, true), checkNativeJournalPath(path, false)); err != nil {
		return record, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	decoder := json.NewDecoder(io.LimitReader(file, api.NativeSnapshotPublicationRecordMaxBytes+1))
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, errors.New("native runtime capture: trailing record data")
	}
	if err := record.validate(); err != nil {
		return record, err
	}
	bootID, err := j.owner.currentBootID()
	if err != nil || record.Authority.InstanceID != instance || record.Authority.KernelBootID != bootID {
		return record, errors.Join(err, errors.New("native runtime capture: record belongs to another instance or kernel boot"))
	}
	return record, nil
}

// The caller holds the runtime capture lock.
func (j *nativeRuntimeCaptureJournal) write(record nativeRuntimeCaptureRecord) error {
	if err := record.validate(); err != nil {
		return err
	}
	path, err := j.path(record.Authority.InstanceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(j.root, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return err
	}
	current, err := j.read(record.Authority.InstanceID)
	switch {
	case err == nil && current.Authority == record.Authority && current.Capture.Equal(record.Capture):
		return nil
	case err == nil && current.Capture.CompletedAt.IsZero():
		// Only completion of the same start is legal after an incomplete start.
		if current.Authority != record.Authority || record.Capture.CompletedAt.IsZero() || current.Capture.CaptureID != record.Capture.CaptureID ||
			current.Capture.FCVersion != record.Capture.FCVersion || !current.Capture.StartedAt.Equal(record.Capture.StartedAt) {
			return errors.New("native runtime capture: an earlier capture of this VM is uncertain")
		}
	case err == nil:
		// A completed capture is replaced only by a fresh start.
		if !record.Capture.CompletedAt.IsZero() || current.Capture.CaptureID == record.Capture.CaptureID {
			return errors.New("native runtime capture: completed capture namespace cannot be reused")
		}
	case errors.Is(err, os.ErrNotExist):
		if !record.Capture.CompletedAt.IsZero() {
			return errors.New("native runtime capture: completion has no durable start")
		}
	default:
		return err
	}
	return writeNativeJournalValue(path, record)
}

// remove deletes only the exact completed record the caller just wrote.
func (j *nativeRuntimeCaptureJournal) remove(expected nativeRuntimeCaptureRecord) error {
	if expected.Capture.CompletedAt.IsZero() {
		return errors.New("native runtime capture: an incomplete capture record is retained")
	}
	current, err := j.read(expected.Authority.InstanceID)
	if err != nil {
		return err
	}
	if current.Authority != expected.Authority || !current.Capture.Equal(expected.Capture) {
		return errors.New("native runtime capture: record changed before removal")
	}
	path, err := j.path(expected.Authority.InstanceID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return j.removeBackings(expected.Capture)
}

func (j *nativeRuntimeCaptureJournal) backingPath(captureID string) (string, error) {
	if !canonicalNativeHelperID(captureID) {
		return "", errors.New("native runtime capture: invalid capture identity")
	}
	return filepath.Join(j.root, "backings", captureID+".json"), nil
}

func (j *nativeRuntimeCaptureJournal) writeBackings(r nativeSnapshotBackingRecord) error {
	if err := r.validate(r.Capture); err != nil {
		return err
	}
	path, err := j.backingPath(r.Capture.CaptureID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(filepath.Dir(path), true); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native runtime capture: capture already has backing evidence"))
	}
	return writeNativeJournalValue(path, r)
}

func (j *nativeRuntimeCaptureJournal) removeBackings(capture nativeQualificationCaptureRecord) error {
	path, err := j.backingPath(capture.CaptureID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
