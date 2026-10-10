package state

import (
	"context"
	"time"
)

var _ EnvironmentQualificationRestoreReceiptStore = (*MemStore)(nil)

func (m *MemStore) RecordEnvironmentQualificationRestoreReceipt(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest,
	frame EnvironmentQualificationExecution, inputs RuntimeConfigInputs) (EnvironmentQualificationRestoreReceipt, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationRestoreReceipt{}, err
	}
	if !qualificationRecoveryUUIDValid(claimed.ID) || !qualificationRecoveryUUIDValid(frame.InstanceID) || claimed.Attempt < 1 || frame.CaptureInstanceID != claimed.ReservedInstanceID {
		return EnvironmentQualificationRestoreReceipt{}, ErrInvalidArgument
	}
	runtimeInputs := inputs
	inputs = normalizeQualificationRestoreInputs(inputs)
	m.mu.Lock()
	defer m.mu.Unlock()
	key := qualificationRestoreReceiptKey(claimed.ID, claimed.Attempt)
	if prior, exists := m.qualificationRestoreReceipts[key]; exists {
		if !qualificationRestoreReceiptMatchesRequest(prior, claimed, frame, inputs) {
			return EnvironmentQualificationRestoreReceipt{}, ErrConflict
		}
		return cloneQualificationRestoreReceipt(prior), nil
	}
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return EnvironmentQualificationRestoreReceipt{}, err
	}
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return EnvironmentQualificationRestoreReceipt{}, err
	}
	if !qualificationClaimIdentityMatches(current, claimed) || current.Attempt != frame.Attempt || frame.CaptureInstanceID != current.ReservedInstanceID {
		return EnvironmentQualificationRestoreReceipt{}, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return EnvironmentQualificationRestoreReceipt{}, err
	}
	capture, hasCapture := m.qualificationSnapshots[current.ReservedInstanceID]
	original, hasOriginal := m.qualificationExecutions[current.ReservedInstanceID]
	target, hasTarget := m.qualificationExecutions[frame.InstanceID]
	instance, hasInstance := m.instances[frame.InstanceID]
	config, hasConfig := m.instanceRuntimeConfigReceipts[frame.InstanceID]
	guestConfig, hasGuestConfig := m.qualificationConfigReceipts[frame.InstanceID]
	sourceGuestConfig, hasSourceGuestConfig := m.qualificationConfigReceipts[current.ReservedInstanceID]
	if !hasCapture || !hasOriginal || !hasTarget || !hasInstance || !hasConfig || target.Execution != frame ||
		instance.State != string(StateStopped) || instance.WakeID != frame.WakeID || config.WakeID != frame.WakeID ||
		!runtimeConfigInputsEqual(config.Inputs, runtimeInputs) || !m.runtimeConfigInputsFreshLocked(current.AppID, capture.Inputs) ||
		!m.runtimeConfigInputsFreshLocked(current.AppID, inputs) ||
		!qualificationRestoreReceiptValid(current, original, capture, target, runtimeInputs) {
		return EnvironmentQualificationRestoreReceipt{}, ErrConflict
	}
	if !hasSourceGuestConfig || !qualificationConfigReceiptMatchesAttempt(sourceGuestConfig, current,
		current.ReservedInstanceID, "") || !hasGuestConfig || !qualificationConfigReceiptMatchesFrame(guestConfig, current, frame) {
		return EnvironmentQualificationRestoreReceipt{}, ErrConflict
	}
	receipt := EnvironmentQualificationRestoreReceipt{RequestID: current.ID, Attempt: current.Attempt,
		CaptureInstanceID: current.ReservedInstanceID, InstanceID: frame.InstanceID, Inputs: cloneRuntimeConfigInputs(inputs), RecordedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationRestoreReceipt{}, err
	}
	if m.qualificationRestoreReceipts == nil {
		m.qualificationRestoreReceipts = map[string]EnvironmentQualificationRestoreReceipt{}
	}
	for otherKey, prior := range m.qualificationRestoreReceipts {
		if otherKey != key && prior.InstanceID == receipt.InstanceID {
			return EnvironmentQualificationRestoreReceipt{}, ErrConflict
		}
	}
	m.qualificationRestoreReceipts[key] = cloneQualificationRestoreReceipt(receipt)
	return receipt, nil
}

func (m *MemStore) EnvironmentQualificationRestoreReceipt(ctx context.Context, requestID string, attempt int64) (EnvironmentQualificationRestoreReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationRestoreReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationRestoreReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, exists := m.qualificationRestoreReceipts[qualificationRestoreReceiptKey(requestID, attempt)]
	if !exists {
		return EnvironmentQualificationRestoreReceipt{}, ErrNotFound
	}
	return cloneQualificationRestoreReceipt(receipt), nil
}
