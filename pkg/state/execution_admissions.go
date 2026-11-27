package state

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrExecutionAdmissionConflict   = errors.New("state: execution idempotency key reused")
	ErrExecutionAdmissionKeyChanged = errors.New("state: execution admission fingerprint key changed")
	ErrExecutionAdmissionLegacy     = errors.New("state: execution key belongs to legacy response cache")
)

// ExecutionAdmissionIdentity binds a scoped key to a request without storing
// source, input or grant tokens. LegacyKey fences keys issued before atomic
// admission; it must never be persisted in the new ledger or logged.
type ExecutionAdmissionIdentity struct {
	TaskCallback *ExecutionTaskCallback
	AccountID    string
	ScopeHash    [sha256.Size]byte
	KeyHash      [sha256.Size]byte
	Fingerprint  [IdempotencyFingerprintSize]byte
	LegacyKey    string
}

// ExecutionAdmissionReader keeps preparation reads on the admission's own
// transaction/connection. Implementations must not acquire another pool slot.
type ExecutionAdmissionReader interface {
	ExecutionByID(context.Context, string, string) (Execution, error)
	ExecutionArtifactGrantByToken(context.Context, string, []byte, time.Time) (ExecutionArtifactGrant, error)
}

type PrepareExecutionAdmission func(context.Context, ExecutionAdmissionReader) (CreateExecutionParams, error)

// ExecutionAdmissionStore replays before calling prepare. On first admission,
// the key, quota check, grant redemptions, payload, Run and initial event commit
// together. A preparation or persistence error must leave the key reusable.
type ExecutionAdmissionStore interface {
	AdmitExecution(context.Context, ExecutionAdmissionIdentity, PrepareExecutionAdmission) (Execution, bool, error)
}

func validateExecutionAdmission(identity ExecutionAdmissionIdentity, prepare PrepareExecutionAdmission) error {
	if validateExecutionTaskCallback(identity.TaskCallback) != nil {
		return ErrExecutionInvalid
	}
	if identity.AccountID == "" || prepare == nil || identity.LegacyKey == "" ||
		identity.ScopeHash == [sha256.Size]byte{} || identity.KeyHash == [sha256.Size]byte{} ||
		identity.Fingerprint == [IdempotencyFingerprintSize]byte{} {
		return ErrExecutionInvalid
	}
	return nil
}

func compareExecutionAdmission(stored []byte, requested [IdempotencyFingerprintSize]byte) error {
	if len(stored) != IdempotencyFingerprintSize {
		return ErrExecutionInvalid
	}
	// A rotation must never turn a bound request into an unbound one. Retain
	// the host HMAC key for the retention window or fail closed until expiry.
	if !hmac.Equal(stored[:IdempotencyFingerprintKeyIDSize], requested[:IdempotencyFingerprintKeyIDSize]) {
		return ErrExecutionAdmissionKeyChanged
	}
	if !hmac.Equal(stored, requested[:]) {
		return ErrExecutionAdmissionConflict
	}
	return nil
}

func executionAdmissionExpires(at time.Time) time.Time {
	return at.Add(time.Duration(api.ExecutionIdempotencyRetentionSeconds) * time.Second)
}

func validateTaskAdmissionIdentity(identity ExecutionAdmissionIdentity, p CreateExecutionParams) error {
	if p.Task != nil && p.Task.RequestDigest != identity.Fingerprint {
		return ErrExecutionInvalid
	}
	if identity.TaskCallback == nil {
		if p.Task != nil && p.Task.Callback != nil {
			return ErrExecutionInvalid
		}
		return nil
	}
	if p.Task == nil || p.Task.Callback == nil || *p.Task.Callback != *identity.TaskCallback {
		return ErrExecutionInvalid
	}
	return nil
}
