package state

import (
	"context"
	"encoding/hex"
	"strings"
	"time"
)

// EnvironmentQualificationConfigReceipt retains the digest of the API
// environment that the guest acknowledged during this exact private attempt.
// It contains no environment values and grants no routing or activation
// authority by itself.
type EnvironmentQualificationConfigReceipt struct {
	RequestID         string    `json:"request_id"`
	Attempt           int64     `json:"attempt"`
	GraphID           string    `json:"graph_id"`
	InstanceID        string    `json:"instance_id"`
	CaptureInstanceID string    `json:"capture_instance_id,omitempty"`
	APIEnvSHA256      string    `json:"api_env_sha256"`
	RecordedAt        time.Time `json:"recorded_at"`
}

// EnvironmentQualificationConfigReceiptStore durably retains the hash after
// vmmd has accepted the guest's attempt-bound configuration acknowledgement.
type EnvironmentQualificationConfigReceiptStore interface {
	RecordEnvironmentQualificationConfigReceipt(context.Context, EnvironmentWorkloadQualificationRequest,
		EnvironmentQualificationExecution, string) (EnvironmentQualificationConfigReceipt, error)
	EnvironmentQualificationConfigReceipt(context.Context, string) (EnvironmentQualificationConfigReceipt, error)
}

func qualificationConfigDigestValid(digest string) bool {
	if len(digest) != hex.EncodedLen(32) || strings.ToLower(digest) != digest {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func qualificationConfigReceiptMatchesFrame(receipt EnvironmentQualificationConfigReceipt,
	claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution) bool {
	return qualificationConfigReceiptMatchesAttempt(receipt, claimed, frame.InstanceID, frame.CaptureInstanceID) &&
		frame.RequestID == claimed.ID &&
		frame.GraphID == claimed.GraphID && frame.Attempt == claimed.Attempt
}

func qualificationConfigReceiptMatchesAttempt(receipt EnvironmentQualificationConfigReceipt,
	claimed EnvironmentWorkloadQualificationRequest, instanceID, captureInstanceID string) bool {
	return receipt.RequestID == claimed.ID && receipt.Attempt == claimed.Attempt && receipt.GraphID == claimed.GraphID &&
		receipt.InstanceID == instanceID && receipt.CaptureInstanceID == captureInstanceID && qualificationConfigDigestValid(receipt.APIEnvSHA256)
}

func (m *MemStore) RecordEnvironmentQualificationConfigReceipt(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest,
	frame EnvironmentQualificationExecution, apiEnvSHA256 string) (EnvironmentQualificationConfigReceipt, error) {
	var zero EnvironmentQualificationConfigReceipt
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !qualificationRecoveryUUIDValid(claimed.ID) || !qualificationRecoveryUUIDValid(claimed.GraphID) ||
		!qualificationRecoveryUUIDValid(frame.InstanceID) || claimed.Attempt < 1 || !qualificationConfigDigestValid(apiEnvSHA256) ||
		frame.CaptureInstanceID != "" && frame.CaptureInstanceID != claimed.ReservedInstanceID {
		return zero, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if prior, exists := m.qualificationConfigReceipts[frame.InstanceID]; exists {
		if !qualificationConfigReceiptMatchesFrame(prior, claimed, frame) || prior.APIEnvSHA256 != apiEnvSHA256 {
			return zero, ErrConflict
		}
		return prior, nil
	}
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return zero, err
	}
	if !qualificationClaimIdentityMatches(current, claimed) || !qualificationLeaseMatches(current, claimed, time.Now()) ||
		current.Attempt != frame.Attempt || frame.RequestID != current.ID || frame.GraphID != current.GraphID ||
		frame.AppID != current.AppID || frame.DeploymentID != current.DeploymentID || frame.Resource != current.Resource {
		return zero, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return zero, err
	}
	status, hasExecution := m.qualificationExecutions[frame.InstanceID]
	instance, hasInstance := m.instances[frame.InstanceID]
	config, hasConfig := m.instanceRuntimeConfigReceipts[frame.InstanceID]
	if !hasExecution || !hasInstance || !hasConfig || status.Execution != frame || !status.DispatchStarted || status.RetiredAt != nil ||
		instance.State != string(StateRunning) || instance.WakeID != frame.WakeID || config.WakeID != frame.WakeID ||
		!m.runtimeConfigInputsFreshLocked(current.AppID, config.Inputs) {
		return zero, ErrConflict
	}
	receipt := EnvironmentQualificationConfigReceipt{RequestID: current.ID, Attempt: current.Attempt, GraphID: current.GraphID,
		InstanceID: frame.InstanceID, CaptureInstanceID: frame.CaptureInstanceID, APIEnvSHA256: apiEnvSHA256, RecordedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if m.qualificationConfigReceipts == nil {
		m.qualificationConfigReceipts = map[string]EnvironmentQualificationConfigReceipt{}
	}
	m.qualificationConfigReceipts[frame.InstanceID] = receipt
	return receipt, nil
}

func (m *MemStore) EnvironmentQualificationConfigReceipt(ctx context.Context, instanceID string) (EnvironmentQualificationConfigReceipt, error) {
	if !qualificationRecoveryUUIDValid(instanceID) {
		return EnvironmentQualificationConfigReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationConfigReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, exists := m.qualificationConfigReceipts[instanceID]
	if !exists {
		return EnvironmentQualificationConfigReceipt{}, ErrNotFound
	}
	return receipt, nil
}
