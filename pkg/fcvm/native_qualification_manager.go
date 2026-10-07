package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// WithNativeQualificationNodeID binds the attempt-aware surface to the local
// compute identity. Configure it before serving; an unset identity fails closed.
// This does not enable the scheduler consumer or the native lifecycle mode.
func (m *Manager) WithNativeQualificationNodeID(nodeID string) *Manager {
	m.nativeQualificationNodeID = nodeID
	return m
}

func (m *Manager) qualificationJournal(frame state.EnvironmentQualificationExecution) (*nativeQualificationJournal, error) {
	if m.nativeQualificationNodeID == "" || !nativeQualificationUUID(m.nativeQualificationNodeID) {
		return nil, fmt.Errorf("native qualification: local node is not configured: %w", state.ErrConflict)
	}
	if err := validateNativeQualificationFrame(frame, m.nativeQualificationNodeID); err != nil {
		return nil, errors.Join(state.ErrInvalidArgument, err)
	}
	v := m.nativeVMM()
	if v == nil {
		return nil, fmt.Errorf("native qualification: journal-backed lifecycle is unavailable: %w", state.ErrConflict)
	}
	return v.nativeRecoveryRuntime().journal.qualifications(m.nativeQualificationNodeID), nil
}

func validateNativeQualificationWake(ctx context.Context, frame state.EnvironmentQualificationExecution, req WakeRequest) error {
	fields, ok := wire.FromContext(ctx)
	if !ok || fields.WakeID != frame.WakeID || req.Instance != frame.InstanceID || req.AppID != frame.AppID ||
		req.DeploymentID != frame.DeploymentID || req.MemSizeMiB != frame.RAMMB || !nativeQualificationUUID(req.AccountID) ||
		!req.Plan.Valid() || req.VcpuCount <= 0 || req.BaseKey == "" || frame.Artifact.RootfsKey == "" || req.LayerKey != frame.Artifact.RootfsKey ||
		req.Snapshot != nil || req.KeepPaused || req.ExecutionOnly || req.AppTaskOnly || req.ExportDir != "" || req.BuildTimeoutSec != 0 ||
		req.ExecutionLeaseToken != "" || len(req.ExecutionOutboundIntegrationIDs) != 0 {
		return fmt.Errorf("native qualification: boot differs from the original workload execution: %w", state.ErrInvalidArgument)
	}
	return nil
}

// WakeEnvironmentQualification claims one original incoming attempt before
// any allocation or native effect. Duplicate delivery cannot create another
// physical generation. Schedd supplies the frozen workload payload; vmmd
// verifies its placement, artifact and guest reservation at this boundary.
func (m *Manager) WakeEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, req WakeRequest) (*Instance, error) {
	j, err := m.qualificationJournal(frame)
	if err != nil {
		return nil, err
	}
	if err := validateNativeQualificationWake(ctx, frame, req); err != nil {
		return nil, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, err
	}
	req, receiptWaiter, err := m.prepareQualificationConfigReceipt(req)
	if err != nil {
		return nil, fmt.Errorf("native qualification: prepare guest configuration receipt: %w", err)
	}
	defer m.clearQualificationConfigReceipt(frame.InstanceID, receiptWaiter)
	record, err := j.claim(ctx, frame)
	if err != nil {
		return nil, err
	}
	inst, err := m.Wake(nativeQualificationContext(ctx, record), req)
	if err != nil {
		return nil, err
	}
	if err := m.waitForQualificationConfigReceipt(ctx, frame.InstanceID, receiptWaiter); err != nil {
		return nil, err
	}
	return inst, nil
}

// RetireEnvironmentQualification revokes delayed creation, joins Manager's
// original boot/teardown and verifies durable physical retirement. A request
// tombstone, absent parent, or generic destroy result cannot supply a receipt.
// Unbound attempts remain charged until native exclusion proof is implemented.
func (m *Manager) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (state.EnvironmentQualificationRetirement, error) {
	j, err := m.qualificationJournal(frame)
	if err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	record, err := j.revoke(ctx, frame)
	if err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	if record.NativeGeneration == "" {
		return state.EnvironmentQualificationRetirement{}, fmt.Errorf("native qualification: no bound physical retirement evidence: %w", state.ErrConflict)
	}
	if err := m.Destroy(ctx, frame.InstanceID); err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	return j.retirement(ctx, frame)
}

// Incoming generation is the stable receipt identity only after the original
// physical record proves exit and complete resource removal. No extra receipt
// is minted on retries; the original incoming and physical UUIDs stay distinct.
func (j *nativeQualificationJournal) retirement(ctx context.Context, frame state.EnvironmentQualificationExecution) (proof state.EnvironmentQualificationRetirement, result error) {
	lock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	record, err := j.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if record.Execution != frame || !record.Revoked || record.NativeGeneration == "" {
		return proof, fmt.Errorf("native qualification: original attempt has no retirement authority: %w", state.ErrConflict)
	}
	physicalLock, err := j.owner.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if physical.Generation != record.NativeGeneration || physical.KernelBootID != record.KernelBootID ||
		!sameNativePhysicalLease(physical.Lease, record.NativeLease) || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		return proof, fmt.Errorf("native qualification: original physical retirement is unconfirmed: %w", state.ErrConflict)
	}
	return state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: record.Generation,
		NativeGeneration: physical.Generation, KernelBootID: physical.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}, nil
}
