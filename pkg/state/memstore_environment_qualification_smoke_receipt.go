package state

import (
	"context"
	"time"
)

var _ EnvironmentQualificationSmokeReceiptStore = (*MemStore)(nil)

func (m *MemStore) RecordEnvironmentQualificationSmokeReceipt(ctx context.Context,
	claimed EnvironmentWorkloadQualificationRequest, evidence EnvironmentQualificationSmokeEvidence) (EnvironmentQualificationSmokeReceipt, error) {
	if !qualificationRecoveryUUIDValid(claimed.ID) || claimed.Attempt < 1 {
		return EnvironmentQualificationSmokeReceipt{}, ErrInvalidArgument
	}
	if err := evidence.ValidateFor(claimed, evidence.InstanceID); err != nil {
		return EnvironmentQualificationSmokeReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationSmokeReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := qualificationSmokeReceiptKey(claimed.ID, claimed.Attempt)
	if prior, exists := m.qualificationSmokeReceipts[key]; exists {
		restore, hasRestore := m.qualificationRestoreReceipts[key]
		if !hasRestore || !qualificationSmokeReceiptMatchesRequest(prior, claimed, restore) ||
			prior.Resource != evidence.Resource || prior.InstanceID != evidence.InstanceID || prior.PolicyID != evidence.PolicyID ||
			prior.PolicySHA256 != evidence.PolicySHA256 || prior.ResultSHA256 != evidence.ResultSHA256 {
			return EnvironmentQualificationSmokeReceipt{}, ErrConflict
		}
		return prior, nil
	}
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return EnvironmentQualificationSmokeReceipt{}, err
	}
	if !qualificationClaimIdentityMatches(current, claimed) || !qualificationLeaseMatches(current, claimed, time.Now()) ||
		m.qualificationCurrentLocked(memory, current) != nil {
		return EnvironmentQualificationSmokeReceipt{}, ErrConflict
	}
	capture, hasCapture := m.qualificationSnapshots[current.ReservedInstanceID]
	original, hasOriginal := m.qualificationExecutions[current.ReservedInstanceID]
	restore, hasRestore := m.qualificationRestoreReceipts[key]
	target, hasTarget := m.qualificationExecutions[restore.InstanceID]
	if !hasCapture || !hasOriginal || !hasRestore || !hasTarget || restore.RequestID != current.ID || restore.Attempt != current.Attempt ||
		restore.CaptureInstanceID != current.ReservedInstanceID ||
		restore.InstanceID != evidence.InstanceID ||
		!qualificationRestoreReceiptValid(current, original, capture, target, restore.Inputs) ||
		!m.runtimeConfigInputsFreshLocked(current.AppID, restore.Inputs) {
		return EnvironmentQualificationSmokeReceipt{}, ErrConflict
	}
	receipt := EnvironmentQualificationSmokeReceipt{RequestID: current.ID, Attempt: current.Attempt, GraphID: current.GraphID,
		CaptureInstanceID: current.ReservedInstanceID, InstanceID: restore.InstanceID, Resource: evidence.Resource,
		PolicyID: evidence.PolicyID, PolicySHA256: evidence.PolicySHA256, ResultSHA256: evidence.ResultSHA256, RecordedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationSmokeReceipt{}, err
	}
	if m.qualificationSmokeReceipts == nil {
		m.qualificationSmokeReceipts = map[string]EnvironmentQualificationSmokeReceipt{}
	}
	m.qualificationSmokeReceipts[key] = receipt
	return receipt, nil
}

func (m *MemStore) EnvironmentQualificationSmokeReceipt(ctx context.Context, requestID string, attempt int64) (EnvironmentQualificationSmokeReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationSmokeReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationSmokeReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, exists := m.qualificationSmokeReceipts[qualificationSmokeReceiptKey(requestID, attempt)]
	if !exists {
		return EnvironmentQualificationSmokeReceipt{}, ErrNotFound
	}
	return receipt, nil
}
