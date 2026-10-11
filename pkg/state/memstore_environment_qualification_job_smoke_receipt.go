package state

import (
	"context"
	"time"
)

var _ EnvironmentQualificationJobSmokeReceiptStore = (*MemStore)(nil)

func (m *MemStore) RecordEnvironmentQualificationJobSmokeReceipt(ctx context.Context,
	claimed EnvironmentWorkloadQualificationRequest, evidence EnvironmentQualificationJobSmokeEvidence) (EnvironmentQualificationJobSmokeReceipt, error) {
	if !qualificationRecoveryUUIDValid(claimed.ID) || claimed.Attempt < 1 {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrInvalidArgument
	}
	if err := evidence.ValidateFor(claimed, evidence.InstanceID); err != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := qualificationSmokeReceiptKey(claimed.ID, claimed.Attempt)
	if prior, exists := m.qualificationJobSmokeReceipts[key]; exists {
		if prior.GraphID != claimed.GraphID || prior.InstanceID != evidence.InstanceID || prior.Resource != evidence.Resource ||
			prior.PolicyID != evidence.PolicyID || prior.PolicySHA256 != evidence.PolicySHA256 || prior.ResultSHA256 != evidence.ResultSHA256 {
			return EnvironmentQualificationJobSmokeReceipt{}, ErrConflict
		}
		return prior, nil
	}
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, err
	}
	if current.ExecutionMode != "job" || !qualificationClaimIdentityMatches(current, claimed) ||
		!qualificationLeaseMatches(current, claimed, time.Now()) || m.qualificationCurrentLocked(memory, current) != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrConflict
	}
	var graph EnvironmentWorkloadGraph
	for _, candidate := range memory.graphs {
		if candidate.ID == current.GraphID {
			graph = candidate
			break
		}
	}
	if !qualificationGraphJobQueueBindingsSupported(graph, current.Resource) {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrConflict
	}
	status, exists := m.qualificationExecutions[current.ReservedInstanceID]
	instance, instanceExists := m.instances[current.ReservedInstanceID]
	config, configExists := m.qualificationConfigReceipts[current.ReservedInstanceID]
	if !exists || !instanceExists || status.Execution.RequestID != current.ID || status.Execution.GraphID != current.GraphID ||
		status.Execution.Attempt != current.Attempt || status.Execution.InstanceID != evidence.InstanceID || !status.DispatchStarted ||
		status.RetiredAt == nil || status.Retirement == nil || status.Retirement.Kind != QualificationNativeRetired ||
		!status.Retirement.ProcessesExited || !status.Retirement.ResourcesRemoved ||
		State(instance.State) != StateStopped || instance.ID != evidence.InstanceID || !configExists ||
		!qualificationConfigReceiptMatchesFrame(config, current, status.Execution) {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrConflict
	}
	if _, exists := m.qualificationSmokeReceipts[key]; exists {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrConflict
	}
	receipt := EnvironmentQualificationJobSmokeReceipt{RequestID: current.ID, Attempt: current.Attempt, GraphID: current.GraphID,
		InstanceID: evidence.InstanceID, Resource: evidence.Resource, PolicyID: evidence.PolicyID,
		PolicySHA256: evidence.PolicySHA256, ResultSHA256: evidence.ResultSHA256, RecordedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, err
	}
	if m.qualificationJobSmokeReceipts == nil {
		m.qualificationJobSmokeReceipts = map[string]EnvironmentQualificationJobSmokeReceipt{}
	}
	m.qualificationJobSmokeReceipts[key] = receipt
	return receipt, nil
}

func (m *MemStore) EnvironmentQualificationJobSmokeReceipt(ctx context.Context, requestID string, attempt int64) (EnvironmentQualificationJobSmokeReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, exists := m.qualificationJobSmokeReceipts[qualificationSmokeReceiptKey(requestID, attempt)]
	if !exists {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrNotFound
	}
	return receipt, nil
}
