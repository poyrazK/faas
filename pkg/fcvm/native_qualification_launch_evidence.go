package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// NativeQualificationLaunchEvidence correlates one original incoming execution
// with its durable physical generation. Correlation grants no replay, cleanup,
// producer-closure or evidence-release authority. Private capabilities are omitted.
type NativeQualificationLaunchEvidence struct {
	RequestID, AppID, DeploymentID, NodeID, WakeID, IncomingGeneration string
	RequestRevoked                                                     bool
	Native                                                             NativeLaunchEvidence
}

// ObserveEnvironmentQualificationLaunch authenticates the full original frame
// against the configured local node and both durable journals. Unbound or missing
// physical publication remains uncertainty, including after request revocation.
func (m *Manager) ObserveEnvironmentQualificationLaunch(ctx context.Context, frame state.EnvironmentQualificationExecution) (NativeQualificationLaunchEvidence, error) {
	if m == nil {
		return NativeQualificationLaunchEvidence{}, fmt.Errorf("native qualification evidence: manager unavailable: %w", state.ErrConflict)
	}
	journal, err := m.qualificationJournal(frame)
	if err != nil {
		return NativeQualificationLaunchEvidence{}, err
	}
	out, err := journal.observeLaunchBinding(ctx, frame)
	if err != nil {
		return NativeQualificationLaunchEvidence{}, fmt.Errorf("native qualification evidence: observe binding: %w", err)
	}
	return out, nil
}

// ObserveEnvironmentQualificationLaunchGeneration requires both the original
// request and the exact previously observed native incarnation. A prepared
// generation gaining a PID changes its identity and must be observed explicitly.
// This read grants no authority to act on the returned evidence.
func (m *Manager) ObserveEnvironmentQualificationLaunchGeneration(ctx context.Context, frame state.EnvironmentQualificationExecution, expected NativeLaunchIdentity) (NativeQualificationLaunchEvidence, error) {
	if !validPinnedNativeLaunchIdentity(expected) || expected.InstanceID != frame.InstanceID {
		return NativeQualificationLaunchEvidence{}, fmt.Errorf("native qualification evidence: invalid pinned identity: %w", state.ErrInvalidArgument)
	}
	observed, err := m.ObserveEnvironmentQualificationLaunch(ctx, frame)
	if err != nil {
		return NativeQualificationLaunchEvidence{}, err
	}
	if observed.Native.Identity != expected {
		return NativeQualificationLaunchEvidence{}, fmt.Errorf("native qualification evidence: pinned incarnation changed: %w", state.ErrConflict)
	}
	return observed, nil
}

func (j *nativeQualificationJournal) observeLaunchBinding(ctx context.Context, frame state.EnvironmentQualificationExecution) (out NativeQualificationLaunchEvidence, err error) {
	incomingLock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, incomingLock.Close())
		if err != nil {
			out = NativeQualificationLaunchEvidence{}
		}
	}()
	incoming, err := j.read(frame.InstanceID)
	if err != nil {
		return out, err
	}
	if incoming.Execution != frame || !incoming.CreateStarted || incoming.NativeGeneration == "" {
		return out, errors.New("native qualification evidence: original request is changed or unbound")
	}
	physicalLock, err := j.owner.lock(ctx, frame.InstanceID)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, physicalLock.Close())
		if err != nil {
			out = NativeQualificationLaunchEvidence{}
		}
	}()
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		return out, err
	}
	if physical.Generation != incoming.NativeGeneration || physical.KernelBootID != incoming.KernelBootID || !sameNativePhysicalLease(physical.Lease, incoming.NativeLease) {
		return out, errors.New("native qualification evidence: original physical binding changed")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out = NativeQualificationLaunchEvidence{RequestID: frame.RequestID, AppID: frame.AppID, DeploymentID: frame.DeploymentID, NodeID: frame.NodeID, WakeID: frame.WakeID, IncomingGeneration: incoming.Generation, RequestRevoked: incoming.Revoked, Native: nativeLaunchEvidenceFromRecord(physical)}
	return out, nil
}
