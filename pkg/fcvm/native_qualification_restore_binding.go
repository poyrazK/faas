package fcvm

import (
	"context"
	"errors"
	"os"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeQualificationRestoreContextKey struct{}

type nativeQualificationRestoreLoadContextKey struct{}
type nativeQualificationRestoreLoadPermit struct {
	instance, generation string
	used                 atomic.Bool
	completed            atomic.Bool
	channelsUsed         atomic.Bool
}

// This internal capability belongs to the first incoming restore delivery.
// Recovered records retain cleanup/allocation evidence, never a producer permit.
func nativeQualificationRestoreContext(ctx context.Context, record nativeQualificationRestoreRecord) context.Context {
	ctx = context.WithValue(ctx, nativeQualificationRestoreContextKey{}, record)
	permit, _ := ctx.Value(nativeQualificationRestoreLoadContextKey{}).(*nativeQualificationRestoreLoadPermit)
	if permit == nil || permit.instance != record.Execution.InstanceID || permit.generation != record.Generation {
		permit = &nativeQualificationRestoreLoadPermit{instance: record.Execution.InstanceID, generation: record.Generation}
	}
	return context.WithValue(ctx, nativeQualificationRestoreLoadContextKey{}, permit)
}

// Consume before the first journal publication. The original live call cannot
// replay after journal damage or a lost write acknowledgement. Inventory never
// reconstructs this process-local capability from a retained target record.
func consumeNativeQualificationRestoreLoad(ctx context.Context, target nativeQualificationRestoreRecord) error {
	permit, _ := ctx.Value(nativeQualificationRestoreLoadContextKey{}).(*nativeQualificationRestoreLoadPermit)
	if permit == nil || permit.instance != target.Execution.InstanceID || permit.generation != target.Generation || !permit.used.CompareAndSwap(false, true) {
		return errors.New("native restore load: original one-shot producer was consumed or is unavailable")
	}
	return nil
}

type nativeQualificationProducer struct {
	Execution        state.EnvironmentQualificationExecution
	NativeGeneration string
	NativeLease      Lease
	capture          *nativeQualificationRecord
	restore          *nativeQualificationRestoreRecord
}

func (p *nativeQualificationProducer) bind(ctx context.Context, owner *nativeLaunchJournal, lease Lease) (string, error) {
	if p.restore != nil {
		r, err := owner.qualifications(p.Execution.NodeID).restores().bindNative(ctx, *p.restore, lease)
		return r.NativeGeneration, err
	}
	r, err := owner.qualifications(p.Execution.NodeID).bindNative(ctx, *p.capture, lease)
	return r.NativeGeneration, err
}

// Called with the common target incoming lock held. Source completion and full
// retirement must still match before the first target physical effect.
func (j *nativeQualificationRestoreJournal) bindNative(ctx context.Context, r nativeQualificationRestoreRecord, lease Lease) (nativeQualificationRestoreRecord, error) {
	if r.NativeGeneration != "" {
		return r, errors.New("native qualification restore: original target already has a physical binding")
	}
	if err := validateNativeQualificationLease(r.Execution, lease); err != nil {
		return r, err
	}
	if lease.MemoryMaxMiB != r.Execution.RAMMB {
		return r, errors.New("native qualification restore: target guest RAM differs from original captured reservation")
	}
	if _, err := j.requireCapture(ctx, r); err != nil {
		return r, err
	}
	if err := j.incoming.requireNativeAbsent(lease.Instance); err != nil {
		return r, err
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	lease.processGeneration = 0
	r.NativeGeneration, r.NativeLease = uuid.NewString(), lease
	return r, j.write(r)
}

func (j *nativeQualificationRestoreJournal) producer(ctx context.Context, instance string, permit nativeQualificationRestoreRecord) (*nativeQualificationProducer, error) {
	r, err := j.read(instance)
	if err != nil {
		return nil, err
	}
	if r.Execution != permit.Execution || r.Generation != permit.Generation || r.KernelBootID != permit.KernelBootID || r.Capture != permit.Capture ||
		!r.AcceptedAt.Equal(permit.AcceptedAt) || !r.Deadline.Equal(permit.Deadline) ||
		r.Execution.InstanceID != instance || !r.CreateStarted || r.Revoked || !j.incoming.clock().Before(r.Deadline) {
		return nil, errors.New("native qualification restore: target producer changed, expired or was revoked")
	}
	if _, err := j.requireCapture(ctx, r); err != nil {
		return nil, err
	}
	return &nativeQualificationProducer{Execution: r.Execution, NativeGeneration: r.NativeGeneration, NativeLease: r.NativeLease, restore: &r}, nil
}

func (j *nativeQualificationRestoreJournal) revokeNative(ctx context.Context, r nativeQualificationRestoreRecord) error {
	if r.NativeGeneration == "" {
		return nil
	}
	physical, err := j.incoming.owner.read(r.Execution.InstanceID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if physical.Generation != r.NativeGeneration || physical.KernelBootID != r.KernelBootID || !sameNativePhysicalLease(physical.Lease, r.NativeLease) {
		return errors.New("native qualification restore: revocation cannot borrow another physical owner")
	}
	_, err = j.incoming.owner.revoke(ctx, r.Execution.InstanceID)
	return err
}

func (j *nativeQualificationRestoreJournal) retirement(ctx context.Context, frame state.EnvironmentQualificationExecution) (proof state.EnvironmentQualificationRetirement, result error) {
	lock, err := j.incoming.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	r, err := j.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if r.Execution != frame || !r.Revoked || r.NativeGeneration == "" {
		return proof, errors.New("native qualification restore: original target has no retirement authority")
	}
	physicalLock, err := j.incoming.owner.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	physical, err := j.incoming.owner.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if physical.Generation != r.NativeGeneration || physical.KernelBootID != r.KernelBootID || !sameNativePhysicalLease(physical.Lease, r.NativeLease) ||
		!physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		return proof, errors.New("native qualification restore: original target physical retirement is unconfirmed")
	}
	return state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: r.Generation, NativeGeneration: physical.Generation,
		KernelBootID: physical.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}, nil
}
