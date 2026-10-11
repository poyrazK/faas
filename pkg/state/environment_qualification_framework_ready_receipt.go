package state

import (
	"context"
	"strings"
	"time"
)

const qualificationFrameworkReadyWarmupMaxMS = int64(1<<32 - 1)

// EnvironmentQualificationFrameworkReadyReceipt retains the restored guest's
// first successful framework-ready signal for one private restore attempt.
// The separate HTTP smoke receipt still proves the reviewed application health
// policy; this receipt alone grants no qualification or serving authority.
type EnvironmentQualificationFrameworkReadyReceipt struct {
	RequestID         string    `json:"request_id"`
	Attempt           int64     `json:"attempt"`
	GraphID           string    `json:"graph_id"`
	CaptureInstanceID string    `json:"capture_instance_id"`
	InstanceID        string    `json:"instance_id"`
	Runtime           string    `json:"runtime"`
	WarmupMS          int64     `json:"warmup_ms"`
	RecordedAt        time.Time `json:"recorded_at"`
}

// EnvironmentQualificationFrameworkReadyReceiptStore durably records the
// instance-bound framework-ready event from a private restored target.
type EnvironmentQualificationFrameworkReadyReceiptStore interface {
	RecordEnvironmentQualificationFrameworkReadyReceipt(context.Context, EnvironmentQualificationExecution,
		string, int64) (EnvironmentQualificationFrameworkReadyReceipt, error)
	EnvironmentQualificationFrameworkReadyReceipt(context.Context, string, int64) (EnvironmentQualificationFrameworkReadyReceipt, error)
}

func qualificationFrameworkReadyRuntimeValid(runtime string) bool {
	if runtime == "" || len(runtime) > 32 || strings.TrimSpace(runtime) != runtime {
		return false
	}
	for i := 0; i < len(runtime); i++ {
		c := runtime[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func qualificationFrameworkReadyReceiptMatches(receipt EnvironmentQualificationFrameworkReadyReceipt,
	frame EnvironmentQualificationExecution, runtime string, warmupMS int64) bool {
	return receipt.RequestID == frame.RequestID && receipt.Attempt == frame.Attempt && receipt.GraphID == frame.GraphID &&
		receipt.CaptureInstanceID == frame.CaptureInstanceID && receipt.InstanceID == frame.InstanceID &&
		receipt.Runtime == runtime && receipt.WarmupMS == warmupMS && !receipt.RecordedAt.IsZero()
}

func qualificationFrameworkReadyReceiptMatchesRestore(receipt EnvironmentQualificationFrameworkReadyReceipt,
	restore EnvironmentQualificationRestoreReceipt, graphID string) bool {
	return receipt.RequestID == restore.RequestID && receipt.Attempt == restore.Attempt && receipt.GraphID == graphID &&
		receipt.CaptureInstanceID == restore.CaptureInstanceID && receipt.InstanceID == restore.InstanceID &&
		qualificationFrameworkReadyRuntimeValid(receipt.Runtime) && receipt.WarmupMS >= 0 && !receipt.RecordedAt.IsZero()
}

func (m *MemStore) RecordEnvironmentQualificationFrameworkReadyReceipt(ctx context.Context,
	frame EnvironmentQualificationExecution, runtime string, warmupMS int64) (EnvironmentQualificationFrameworkReadyReceipt, error) {
	if !qualificationRecoveryUUIDValid(frame.RequestID) || !qualificationRecoveryUUIDValid(frame.GraphID) ||
		!qualificationRecoveryUUIDValid(frame.CaptureInstanceID) || !qualificationRecoveryUUIDValid(frame.InstanceID) ||
		frame.CaptureInstanceID == frame.InstanceID || frame.Attempt < 1 || !qualificationFrameworkReadyRuntimeValid(runtime) ||
		warmupMS < 0 || warmupMS > qualificationFrameworkReadyWarmupMaxMS {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := qualificationSmokeReceiptKey(frame.RequestID, frame.Attempt)
	if prior, exists := m.qualificationFrameworkReadyReceipts[key]; exists {
		if !qualificationFrameworkReadyReceiptMatches(prior, frame, runtime, warmupMS) {
			return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
		}
		return prior, nil
	}
	memory, current, err := m.qualificationLocked(frame.RequestID)
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	if current.Attempt != frame.Attempt || current.GraphID != frame.GraphID || current.ReservedInstanceID != frame.CaptureInstanceID ||
		current.Phase != "claimed" || current.FrozenInputs.RuntimeBase != "" && current.FrozenInputs.RuntimeBase != runtime ||
		!qualificationLeaseMatches(current, current, time.Now()) || m.qualificationCurrentLocked(memory, current) != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	status, hasExecution := m.qualificationExecutions[frame.InstanceID]
	instance, hasInstance := m.instances[frame.InstanceID]
	config, hasConfig := m.instanceRuntimeConfigReceipts[frame.InstanceID]
	capture, hasCapture := m.qualificationSnapshots[frame.CaptureInstanceID]
	sourceStatus, hasSourceExecution := m.qualificationExecutions[frame.CaptureInstanceID]
	if !hasExecution || !hasInstance || !hasConfig || !hasCapture || status.Execution != frame || !status.DispatchStarted ||
		!hasSourceExecution || !qualificationRestoreCaptureMatches(current, sourceStatus, capture) ||
		status.CaptureInstanceID != frame.CaptureInstanceID || status.RetiredAt != nil ||
		frame != qualificationRestoreExecution(current, instance, frame.CleanupToken, frame.CaptureInstanceID) ||
		instance.State != string(StateRunning) || instance.WakeID != frame.WakeID || config.WakeID != frame.WakeID ||
		!qualificationRuntimeValuesEqual(config.Inputs, capture.Inputs) || !m.runtimeConfigInputsFreshLocked(current.AppID, config.Inputs) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	receipt := EnvironmentQualificationFrameworkReadyReceipt{RequestID: frame.RequestID, Attempt: frame.Attempt, GraphID: frame.GraphID,
		CaptureInstanceID: frame.CaptureInstanceID, InstanceID: frame.InstanceID, Runtime: runtime, WarmupMS: warmupMS, RecordedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	if m.qualificationFrameworkReadyReceipts == nil {
		m.qualificationFrameworkReadyReceipts = map[string]EnvironmentQualificationFrameworkReadyReceipt{}
	}
	m.qualificationFrameworkReadyReceipts[key] = receipt
	return receipt, nil
}

func (m *MemStore) EnvironmentQualificationFrameworkReadyReceipt(ctx context.Context, requestID string,
	attempt int64) (EnvironmentQualificationFrameworkReadyReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, exists := m.qualificationFrameworkReadyReceipts[qualificationSmokeReceiptKey(requestID, attempt)]
	if !exists {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrNotFound
	}
	return receipt, nil
}
