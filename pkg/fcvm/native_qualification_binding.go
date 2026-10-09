package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeQualificationContextKey struct{}

// Only the attempt-aware native owner may carry this capability into a boot.
// An ordinary instance ID or scheduler reservation confers no such authority.
func nativeQualificationContext(ctx context.Context, record nativeQualificationRecord) context.Context {
	return context.WithValue(ctx, nativeQualificationContextKey{}, record)
}

func (j *nativeLaunchJournal) qualifications(nodeID string) *nativeQualificationJournal {
	return &nativeQualificationJournal{root: filepath.Join(j.root, "qualifications"), owner: j, nodeID: nodeID}
}

func validateNativeQualificationLease(frame state.EnvironmentQualificationExecution, lease Lease) error {
	if err := validateNativeJournalLease(lease); err != nil {
		return err
	}
	if lease.Instance != frame.InstanceID || !lease.Plan.Valid() || lease.IsBuilder || lease.Networkless || lease.MemoryMaxMiB < frame.RAMMB {
		return errors.New("native qualification: lease differs from the original workload reservation")
	}
	return nil
}

// Lock order is incoming request, then physical launch. Generic UUID callers
// take the same incoming lock even before a request exists, so claim cannot
// interleave with their physical publication. Noncanonical UUID spellings
// reach the same lock; only the exact original spelling can carry authority.
func (j *nativeLaunchJournal) lockQualificationProducer(ctx context.Context, instance string) (*os.File, *nativeQualificationProducer, error) {
	permit, permitted := ctx.Value(nativeQualificationContextKey{}).(nativeQualificationRecord)
	restorePermit, restoring := ctx.Value(nativeQualificationRestoreContextKey{}).(nativeQualificationRestoreRecord)
	id, err := uuid.Parse(instance)
	if err != nil || id == uuid.Nil {
		if permitted || restoring {
			return nil, nil, errors.New("native qualification: producer has no original instance identity")
		}
		return nil, nil, ctx.Err()
	}
	q := j.qualifications(permit.Execution.NodeID)
	if restoring {
		q = j.qualifications(restorePermit.Execution.NodeID)
	}
	lock, err := q.lock(ctx, id.String())
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.File, *nativeQualificationProducer, error) {
		return nil, nil, errors.Join(err, lock.Close())
	}
	path, _ := q.path(id.String())
	restorePath, _ := q.restores().path(id.String())
	if _, err := os.Lstat(restorePath); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fail(err)
		}
		if !restoring || permitted {
			return fail(errors.New("native qualification restore: reserved target requires its original producer"))
		}
		if err := q.requireProfileAbsent(instance, false); err != nil {
			return fail(err)
		}
		producer, err := q.restores().producer(ctx, instance, restorePermit)
		if err != nil {
			return fail(err)
		}
		return lock, producer, nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if permitted || restoring {
			return fail(errors.New("native qualification: producer has no durable incoming authority"))
		}
		return lock, nil, nil
	} else if err != nil {
		return fail(err)
	}
	if !permitted || restoring {
		return fail(errors.New("native qualification: reserved instance requires its original attempt"))
	}
	record, err := q.read(instance)
	if err != nil {
		return fail(err)
	}
	if record.Execution != permit.Execution || record.Generation != permit.Generation || record.KernelBootID != permit.KernelBootID ||
		record.Execution.InstanceID != instance || !record.CreateStarted || record.Revoked || !q.clock().Before(record.Deadline) {
		return fail(errors.New("native qualification: producer authority changed, expired or revoked"))
	}
	return lock, &nativeQualificationProducer{Execution: record.Execution, NativeGeneration: record.NativeGeneration, NativeLease: record.NativeLease, capture: &record}, nil
}

func (j *nativeLaunchJournal) checkQualificationProducer(ctx context.Context, instance string) error {
	lock, _, err := j.lockQualificationProducer(ctx, instance)
	if lock != nil {
		err = errors.Join(err, lock.Close())
	}
	return err
}

// The caller holds the incoming lock through physical publication. A lost
// acknowledgement leaves the planned lease quarantined; it cannot authorize a
// second preparation or a replacement generation.
func (j *nativeQualificationJournal) bindNative(ctx context.Context, record nativeQualificationRecord, lease Lease) (nativeQualificationRecord, error) {
	if record.NativeGeneration != "" {
		return record, errors.New("native qualification: original physical binding already exists")
	}
	if err := validateNativeQualificationLease(record.Execution, lease); err != nil {
		return record, err
	}
	if err := j.requireNativeAbsent(lease.Instance); err != nil {
		return record, err
	}
	if err := ctx.Err(); err != nil {
		return record, err
	}
	lease.processGeneration = 0
	record.NativeGeneration, record.NativeLease = uuid.NewString(), lease
	return record, j.write(record)
}

// noNativeEffectsRetirement proves the incoming attempt was durably revoked
// and never acquired a physical owner. The caller separately joins Manager
// operations and releases any allocator reservation before returning it.
func (j *nativeQualificationJournal) noNativeEffectsRetirement(ctx context.Context, frame state.EnvironmentQualificationExecution) (proof state.EnvironmentQualificationRetirement, result error) {
	lock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	record, err := j.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if record.Execution != frame || !record.Revoked {
		return proof, state.ErrConflict
	}
	physicalLock, err := j.owner.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	if _, err := j.owner.read(frame.InstanceID); !errors.Is(err, os.ErrNotExist) {
		return proof, errors.Join(err, state.ErrConflict)
	}
	proof = state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeEffectsAbsent, ReceiptID: record.Generation,
		KernelBootID: record.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}
	return proof, ctx.Err()
}

// Any existing physical spelling, including a damaged record, prevents a
// qualification claim from borrowing the instance's previous native owner.
func (j *nativeQualificationJournal) requireNativeAbsent(instance string) error {
	entries, err := os.ReadDir(j.owner.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	want, _ := uuid.Parse(instance)
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".json")
		id, err := uuid.Parse(name)
		if ok && err == nil && id == want {
			return errors.New("native qualification: instance already has physical ownership")
		}
	}
	return nil
}

// Revocation is first durable in the incoming journal. Repeating it completes
// the physical fence after a crash between these writes. This does not assert
// process exit, resource removal or absence of a native producer.
func (j *nativeQualificationJournal) revokeNative(ctx context.Context, record nativeQualificationRecord) error {
	if record.NativeGeneration == "" {
		return nil
	}
	physical, err := j.owner.read(record.Execution.InstanceID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if physical.Generation != record.NativeGeneration || physical.KernelBootID != record.KernelBootID || !sameNativePhysicalLease(physical.Lease, record.NativeLease) {
		return errors.New("native qualification: revocation cannot borrow changed physical ownership")
	}
	_, err = j.owner.revoke(ctx, record.Execution.InstanceID)
	return err
}

// A planned lease whose physical publication is uncertain remains allocated
// after daemon restart. Missing physical records confer no retirement evidence.
func (j *nativeQualificationJournal) recoveryLeases(ctx context.Context, physical []nativeLaunchRecord) ([]Lease, error) {
	entries, err := os.ReadDir(j.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var leases []Lease
	for _, entry := range entries {
		if entry.Name() == "captures" || entry.Name() == "restores" {
			// Receipts carry no native allocation authority. Validate their
			// original identity after the incoming inventory.
			continue
		}
		if strings.HasSuffix(entry.Name(), ".lock") || strings.HasPrefix(entry.Name(), ".launch-") {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		id, err := uuid.Parse(instance)
		if !ok || err != nil || id == uuid.Nil || id.String() != instance || !entry.Type().IsRegular() {
			return nil, errors.New("native qualification: unexpected incoming journal entry")
		}
		lock, err := j.lock(ctx, instance)
		if err != nil {
			return nil, err
		}
		record, err := j.read(instance)
		err = errors.Join(err, lock.Close())
		if err != nil {
			return nil, err
		}
		found := false
		for _, parent := range physical {
			parentID, err := uuid.Parse(parent.Lease.Instance)
			if err != nil || parentID != id {
				continue
			}
			if found || record.NativeGeneration == "" || parent.Generation != record.NativeGeneration || parent.KernelBootID != record.KernelBootID || !sameNativePhysicalLease(parent.Lease, record.NativeLease) {
				return nil, errors.New("native qualification: recovered physical binding changed or is ambiguous")
			}
			found = true
		}
		if !found && record.NativeGeneration != "" {
			leases = append(leases, record.NativeLease)
		}
	}
	if err := j.validateCaptures(ctx); err != nil {
		return nil, err
	}
	restores, err := j.restores().recoveryLeases(ctx, physical)
	return append(leases, restores...), errors.Join(ctx.Err(), err)
}
