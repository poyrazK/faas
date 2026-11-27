package state

import (
	"context"
	"time"
)

var _ ExecutionAdmissionStore = (*MemStore)(nil)

type executionAdmissionKey struct {
	accountID          string
	scopeHash, keyHash [32]byte
}

type executionAdmission struct {
	fingerprint [IdempotencyFingerprintSize]byte
	executionID string
	expiresAt   time.Time
}

func (m *MemStore) AdmitExecution(ctx context.Context, identity ExecutionAdmissionIdentity, prepare PrepareExecutionAdmission) (Execution, bool, error) {
	if err := validateExecutionAdmission(identity, prepare); err != nil {
		return Execution{}, false, err
	}
	// Preparation uses normal read methods, which acquire m.mu. Serialize
	// admissions separately, then publish the Run and identity under one m.mu.
	m.executionAdmissionMu.Lock()
	defer m.executionAdmissionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Execution{}, false, err
	}
	key := executionAdmissionKey{identity.AccountID, identity.ScopeHash, identity.KeyHash}
	at := time.Now().UTC()
	m.mu.Lock()
	if row, found, err := m.taskCallbackReplayLocked(identity.AccountID, identity.TaskCallback, identity.Fingerprint); found || err != nil {
		m.mu.Unlock()
		return row, true, err
	}
	entry, exists := m.executionAdmissions[key]
	if exists && entry.expiresAt.After(at) {
		err := compareExecutionAdmission(entry.fingerprint[:], identity.Fingerprint)
		row, found := m.executions[entry.executionID]
		row = cloneExecution(row)
		m.mu.Unlock()
		if err != nil {
			return Execution{}, false, err
		}
		if !found {
			return Execution{}, false, ErrNotFound
		}
		return row, true, nil
	}
	legacy, exists := m.idem[identity.AccountID+"\x00"+identity.LegacyKey]
	m.mu.Unlock()
	if exists && executionAdmissionExpires(legacy.created).After(at) {
		return Execution{}, false, ErrExecutionAdmissionLegacy
	}
	params, err := prepare(ctx, m)
	if err != nil {
		return Execution{}, false, err
	}
	if err := validateTaskAdmissionIdentity(identity, params); err != nil {
		return Execution{}, false, err
	}
	if params.AccountID != identity.AccountID {
		return Execution{}, false, ErrExecutionInvalid
	}
	if err := ctx.Err(); err != nil {
		return Execution{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, err := m.createExecutionLocked(params)
	if err != nil {
		return Execution{}, false, err
	}
	if m.executionAdmissions == nil {
		m.executionAdmissions = make(map[executionAdmissionKey]executionAdmission)
	}
	m.executionAdmissions[key] = executionAdmission{identity.Fingerprint, row.ID, executionAdmissionExpires(time.Now().UTC())}
	return row, false, nil
}
