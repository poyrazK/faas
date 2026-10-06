// adr: 568 — restore authority owns a distinct target and never adopts its capture.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This separate profile leaves the v2 capture journal unchanged. Capture pins
// evidence, not cleanup authority over the retired producer or its artifacts.
type nativeQualificationRestoreRecord struct {
	Version          int                                     `json:"version"`
	Generation       string                                  `json:"generation"`
	KernelBootID     string                                  `json:"kernel_boot_id"`
	Execution        state.EnvironmentQualificationExecution `json:"execution"`
	CleanupToken     string                                  `json:"cleanup_token"`
	Capture          state.EnvironmentQualificationSnapshot  `json:"capture"`
	AcceptedAt       time.Time                               `json:"accepted_at"`
	Deadline         time.Time                               `json:"deadline"`
	CreateStarted    bool                                    `json:"create_started"`
	Revoked          bool                                    `json:"revoked"`
	NativeGeneration string                                  `json:"native_generation"`
	NativeLease      Lease                                   `json:"native_lease"`
}

func (r nativeQualificationRestoreRecord) String() string {
	return fmt.Sprintf("%s native_restore=%s started=%t revoked=%t", r.Execution.String(), r.Generation, r.CreateStarted, r.Revoked)
}
func (r nativeQualificationRestoreRecord) GoString() string { return r.String() }

func (r *nativeQualificationRestoreRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, nativeQualificationJSONFields(reflect.TypeOf(*r)))
	if err != nil {
		return err
	}
	frameFields := append(nativeQualificationJSONFields(reflect.TypeOf(r.Execution)), "capture_instance_id")
	execution, err := nativeJournalObjectFields(fields["execution"], frameFields)
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(execution["artifact"], nativeQualificationJSONFields(reflect.TypeOf(r.Execution.Artifact))); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["capture"], nativeQualificationJSONFields(reflect.TypeOf(r.Capture))); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["native_lease"], nativeJournalLeaseFields()); err != nil {
		return err
	}
	type plain nativeQualificationRestoreRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode((*plain)(r)); err != nil {
		return err
	}
	r.Execution.CleanupToken = r.CleanupToken
	return nil
}

func validateNativeQualificationRestoreFrame(frame state.EnvironmentQualificationExecution, nodeID string) error {
	capture, captureErr := uuid.Parse(frame.CaptureInstanceID)
	target, targetErr := uuid.Parse(frame.InstanceID)
	if targetErr != nil || captureErr != nil || capture == uuid.Nil || capture == target || !nativeQualificationUUID(frame.CaptureInstanceID) {
		return errors.New("native qualification restore: separate original capture identity is required")
	}
	frame.CaptureInstanceID = ""
	return validateNativeQualificationFrame(frame, nodeID)
}

func (r nativeQualificationRestoreRecord) validate(nodeID string) error {
	if err := validateNativeQualificationRestoreFrame(r.Execution, nodeID); err != nil {
		return err
	}
	if r.Version != 1 || !canonicalNativeHelperID(r.Generation) || !canonicalNativeHelperID(r.KernelBootID) ||
		r.CleanupToken != r.Execution.CleanupToken || r.AcceptedAt.IsZero() || state.ValidateEnvironmentQualificationSnapshot(r.Execution, r.Capture) != nil ||
		r.Generation == r.Capture.CaptureID || r.KernelBootID != r.Capture.KernelBootID ||
		r.CreateStarted && (!r.AcceptedAt.Before(r.Deadline) || r.Deadline.Sub(r.AcceptedAt) > api.EnvironmentGitOpsQualificationMaxLeaseDuration) ||
		!r.CreateStarted && (!r.Revoked || !r.Deadline.IsZero()) {
		return errors.New("native qualification restore: incoming target or capture evidence is incomplete")
	}
	if r.NativeGeneration == "" {
		if r.NativeLease != (Lease{}) {
			return errors.New("native qualification restore: target lease has no generation")
		}
	} else if !r.CreateStarted || !canonicalNativeHelperID(r.NativeGeneration) || r.NativeGeneration == r.Generation ||
		r.NativeGeneration == r.Capture.NativeGeneration || validateNativeQualificationLease(r.Execution, r.NativeLease) != nil || r.NativeLease.MemoryMaxMiB != r.Execution.RAMMB {
		return errors.New("native qualification restore: target physical binding is invalid")
	}
	return nil
}

type nativeQualificationRestoreJournal struct {
	incoming   *nativeQualificationJournal
	writeValue func(string, nativeQualificationRestoreRecord) error
}

func (j *nativeQualificationJournal) restores() *nativeQualificationRestoreJournal {
	return &nativeQualificationRestoreJournal{incoming: j}
}

func (j *nativeQualificationRestoreJournal) root() string {
	return filepath.Join(j.incoming.root, "restores")
}
func (j *nativeQualificationRestoreJournal) path(instance string) (string, error) {
	key, err := j.incoming.key(instance)
	return filepath.Join(j.root(), key+".json"), err
}

func (j *nativeQualificationRestoreJournal) read(instance string) (r nativeQualificationRestoreRecord, result error) {
	path, err := j.path(instance)
	if err != nil {
		return r, err
	}
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return r, err
	}
	if err := checkNativeJournalPath(path, false); err != nil {
		return r, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return r, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	d := json.NewDecoder(file)
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, errors.New("native qualification restore: trailing incoming data")
	}
	node := j.incoming.nodeID
	if node == "" {
		node = r.Execution.NodeID
	}
	if err := r.validate(node); err != nil {
		return r, err
	}
	key, _ := j.incoming.key(r.Execution.InstanceID)
	want, _ := j.incoming.key(instance)
	boot, err := j.incoming.owner.currentBootID()
	if err != nil || key != want || r.KernelBootID != boot {
		return r, errors.Join(err, errors.New("native qualification restore: target belongs to another instance or kernel boot"))
	}
	return r, nil
}

func (j *nativeQualificationRestoreJournal) write(r nativeQualificationRestoreRecord) error {
	if err := r.validate(j.incoming.nodeID); err != nil {
		return err
	}
	if err := os.MkdirAll(j.root(), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return err
	}
	path, err := j.path(r.Execution.InstanceID)
	if err != nil {
		return err
	}
	if j.writeValue != nil {
		return j.writeValue(path, r)
	}
	return writeNativeJournalValue(path, r)
}

// Target incoming -> captured incoming -> captured physical is the lock order.
// The captured producer is already fully retired, so it cannot launch, mutate
// its completion, or acquire target authority. Restore of a restore is refused.
func (j *nativeQualificationRestoreJournal) capture(ctx context.Context, frame state.EnvironmentQualificationExecution) (capture nativeQualificationCaptureRecord, result error) {
	q := j.incoming.owner.qualifications(frame.NodeID)
	lock, err := q.lock(ctx, frame.CaptureInstanceID)
	if err != nil {
		return capture, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	source, err := q.read(frame.CaptureInstanceID)
	if err != nil {
		return capture, err
	}
	want := frame
	want.InstanceID, want.WakeID, want.CleanupToken, want.CaptureInstanceID = source.Execution.InstanceID, source.Execution.WakeID, source.Execution.CleanupToken, ""
	sourceWake, _ := uuid.Parse(source.Execution.WakeID)
	targetWake, _ := uuid.Parse(frame.WakeID)
	sourceCleanup, _ := uuid.Parse(source.Execution.CleanupToken)
	targetCleanup, _ := uuid.Parse(frame.CleanupToken)
	if source.Execution != want || source.Execution.InstanceID != frame.CaptureInstanceID ||
		sourceWake == targetWake || sourceCleanup == targetCleanup || !source.Revoked || source.NativeGeneration == "" {
		return capture, errors.New("native qualification restore: target differs from the retired original execution")
	}
	capture, err = q.readCapture(source)
	if err != nil || capture.CompletedAt.IsZero() {
		return capture, errors.Join(err, errors.New("native qualification restore: original capture is incomplete"))
	}
	physicalLock, err := q.owner.lock(ctx, source.Execution.InstanceID)
	if err != nil {
		return capture, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	physical, err := q.owner.read(source.Execution.InstanceID)
	if err != nil || physical.Generation != source.NativeGeneration || physical.KernelBootID != source.KernelBootID ||
		!sameNativePhysicalLease(physical.Lease, source.NativeLease) || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		return capture, errors.Join(err, errors.New("native qualification restore: original producer retirement is unconfirmed"))
	}
	return capture, ctx.Err()
}

func (j *nativeQualificationRestoreJournal) requireCapture(ctx context.Context, r nativeQualificationRestoreRecord) (nativeQualificationCaptureRecord, error) {
	capture, err := j.capture(ctx, r.Execution)
	if err != nil {
		return capture, err
	}
	proof := qualificationSnapshotProof(nativeQualificationRecord{Generation: capture.CaptureID, KernelBootID: capture.KernelBootID,
		NativeGeneration: capture.NativeGeneration, Execution: state.EnvironmentQualificationExecution{DeploymentID: r.Execution.DeploymentID}}, capture.Info)
	if proof != r.Capture {
		return capture, errors.New("native qualification restore: original capture evidence changed")
	}
	return capture, nil
}

func (j *nativeQualificationRestoreJournal) update(ctx context.Context, frame state.EnvironmentQualificationExecution, create bool) (r nativeQualificationRestoreRecord, result error) {
	if err := validateNativeQualificationRestoreFrame(frame, j.incoming.nodeID); err != nil {
		return r, err
	}
	lock, err := j.incoming.lock(ctx, frame.InstanceID)
	if err != nil {
		return r, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if err := j.incoming.requireProfileAbsent(frame.InstanceID, false); err != nil {
		return r, err
	}
	r, err = j.read(frame.InstanceID)
	if err == nil {
		if r.Execution != frame || create {
			return r, errors.New("native qualification restore: original target already exists or changed")
		}
		// Cleanup stays available even if retained capture evidence is damaged.
		if r.Revoked {
			return r, j.revokeNative(ctx, r)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		capture, err := j.capture(ctx, frame)
		if err != nil {
			return r, err
		}
		boot, err := j.incoming.owner.currentBootID()
		if err != nil {
			return r, err
		}
		if create {
			if err := j.incoming.requireNativeAbsent(frame.InstanceID); err != nil {
				return r, err
			}
		}
		r = nativeQualificationRestoreRecord{Version: 1, Generation: uuid.NewString(), KernelBootID: boot, Execution: frame,
			CleanupToken: frame.CleanupToken, AcceptedAt: j.incoming.clock().UTC(), Capture: qualificationSnapshotProof(
				nativeQualificationRecord{Generation: capture.CaptureID, KernelBootID: capture.KernelBootID, NativeGeneration: capture.NativeGeneration, Execution: frame}, capture.Info)}
	} else {
		return r, err
	}
	if create {
		deadline, ok := ctx.Deadline()
		if !ok || !r.AcceptedAt.Before(deadline) || deadline.Sub(r.AcceptedAt) > api.EnvironmentGitOpsQualificationMaxLeaseDuration {
			return r, errors.New("native qualification restore: bounded original dispatch deadline is required")
		}
		r.CreateStarted, r.Deadline = true, deadline
	} else {
		r.Revoked = true
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if err := j.write(r); err != nil {
		return r, err
	}
	if !create {
		return r, j.revokeNative(ctx, r)
	}
	return r, nil
}

func (j *nativeQualificationRestoreJournal) claim(ctx context.Context, frame state.EnvironmentQualificationExecution) (nativeQualificationRestoreRecord, error) {
	return j.update(ctx, frame, true)
}
func (j *nativeQualificationRestoreJournal) revoke(ctx context.Context, frame state.EnvironmentQualificationExecution) (nativeQualificationRestoreRecord, error) {
	return j.update(ctx, frame, false)
}

// Both profiles share the canonical incoming lock. An ambiguous or damaged
// opposite-profile file holds authority; it is never overwritten or adopted.
func (j *nativeQualificationJournal) requireProfileAbsent(instance string, restore bool) error {
	path, err := j.path(instance)
	if restore {
		path, err = j.restores().path(instance)
	}
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native qualification: instance already belongs to another incoming profile"))
	}
	return nil
}

func (j *nativeQualificationRestoreJournal) recoveryLeases(ctx context.Context, physical []nativeLaunchRecord) ([]Lease, error) {
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(j.root())
	if err != nil {
		return nil, err
	}
	var leases []Lease
	for _, entry := range entries {
		if entry.Name() == "loads" {
			if !entry.IsDir() {
				return nil, errors.New("native qualification restore: effect evidence directory changed")
			}
			continue
		}
		if strings.HasPrefix(entry.Name(), ".launch-") {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		key, err := j.incoming.key(instance)
		if !ok || err != nil || key != instance || !entry.Type().IsRegular() {
			return nil, errors.New("native qualification restore: unexpected incoming entry")
		}
		lease, found, err := j.recoverLease(ctx, instance, physical)
		if err != nil {
			return nil, err
		}
		if found {
			leases = append(leases, lease)
		}
	}
	return leases, errors.Join(j.loads().validateInventory(ctx), ctx.Err())
}

func (j *nativeQualificationRestoreJournal) recoverLease(ctx context.Context, instance string, physical []nativeLaunchRecord) (lease Lease, planned bool, result error) {
	lock, err := j.incoming.lock(ctx, instance)
	if err != nil {
		return lease, false, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if err := j.incoming.requireProfileAbsent(instance, false); err != nil {
		return lease, false, err
	}
	r, err := j.read(instance)
	if err != nil {
		return lease, false, err
	}
	if _, err := j.requireCapture(ctx, r); err != nil {
		return lease, false, err
	}
	id, _ := uuid.Parse(instance)
	found := false
	for _, parent := range physical {
		parentID, err := uuid.Parse(parent.Lease.Instance)
		if err != nil || parentID != id {
			continue
		}
		if found || r.NativeGeneration == "" || parent.Generation != r.NativeGeneration || parent.KernelBootID != r.KernelBootID || !sameNativePhysicalLease(parent.Lease, r.NativeLease) {
			return lease, false, errors.New("native qualification restore: recovered target binding changed or is ambiguous")
		}
		found = true
	}
	return r.NativeLease, !found && r.NativeGeneration != "", nil
}
