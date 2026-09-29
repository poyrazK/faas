package state

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ProjectEnvironmentCloneOperation pins the source identity for a complete
// clone. The resource list contains identities and capture points, never
// credential or customer-secret values.
type ProjectEnvironmentCloneOperation struct {
	ID                 string
	AccountID          string
	ProjectID          string
	SourceEnvironment  string
	TargetEnvironment  string
	IdempotencyKey     string
	SourceRevisionHash string
	SourceReleaseSetID string
	Status             string
	Revision           int64
	Resources          []ProjectEnvironmentCloneResource
	ErrorCode          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ProjectEnvironmentCloneResource is a non-secret checkpoint for one copied
// or remapped dependency. SourceVersion and CapturePoint are immutable provider
// version/recovery identifiers, not a live object key lookup.
type ProjectEnvironmentCloneResource struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	SourceID      string `json:"source_id,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	TargetID      string `json:"target_id,omitempty"`
	CapturePoint  string `json:"capture_point,omitempty"`
	Status        string `json:"status"`
}

const (
	CloneOperationPending      = "pending"
	CloneOperationCapturing    = "capturing"
	CloneOperationCopying      = "copying"
	CloneOperationPublishing   = "publishing"
	CloneOperationReady        = "ready"
	CloneOperationFailed       = "failed"
	CloneOperationCompensating = "compensating"
	CloneOperationCompensated  = "compensated"
)

var ErrInvalidProjectEnvironmentCloneOperation = errors.New("state: invalid project environment clone operation")

func validateProjectEnvironmentCloneOperation(op ProjectEnvironmentCloneOperation) error {
	if op.AccountID == "" || op.ProjectID == "" || op.SourceEnvironment == "" || op.TargetEnvironment == "" ||
		op.SourceEnvironment == op.TargetEnvironment || len(op.IdempotencyKey) == 0 || len(op.IdempotencyKey) > 255 ||
		len(op.SourceRevisionHash) != 64 || strings.ToLower(op.SourceRevisionHash) != op.SourceRevisionHash ||
		op.Revision != 0 || len(op.Resources) != 0 || op.ErrorCode != "" {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	if hash, err := hex.DecodeString(op.SourceRevisionHash); err != nil || len(hash) != 32 {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	return nil
}

func validCloneOperationTransition(from, to string) bool {
	if from == to {
		return from != CloneOperationReady && from != CloneOperationCompensated
	}
	switch from {
	case CloneOperationPending:
		return to == CloneOperationCapturing || to == CloneOperationFailed || to == CloneOperationCompensating
	case CloneOperationCapturing:
		return to == CloneOperationCopying || to == CloneOperationFailed || to == CloneOperationCompensating
	case CloneOperationCopying:
		return to == CloneOperationPublishing || to == CloneOperationFailed || to == CloneOperationCompensating
	case CloneOperationPublishing:
		return to == CloneOperationReady || to == CloneOperationFailed || to == CloneOperationCompensating
	case CloneOperationFailed:
		return to == CloneOperationCompensating
	case CloneOperationCompensating:
		return to == CloneOperationCompensated
	default:
		return false
	}
}

func validCloneOperationErrorCode(code string) bool {
	if len(code) > 100 {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func cloneProjectEnvironmentCloneOperation(op ProjectEnvironmentCloneOperation) ProjectEnvironmentCloneOperation {
	op.Resources = append([]ProjectEnvironmentCloneResource(nil), op.Resources...)
	return op
}

// ProjectEnvironmentCloneOperationStore is separate from Store while the
// complete-clone API is introduced, preserving existing narrow adapters.
type ProjectEnvironmentCloneOperationStore interface {
	CreateProjectEnvironmentCloneOperation(ctx context.Context, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneOperation, error)
	ProjectEnvironmentCloneOperationByID(ctx context.Context, accountID, projectID, id string) (ProjectEnvironmentCloneOperation, error)
	ProjectEnvironmentCloneOperationByIdempotencyKey(ctx context.Context, accountID, projectID, key string) (ProjectEnvironmentCloneOperation, error)
	AdvanceProjectEnvironmentCloneOperation(ctx context.Context, accountID, projectID, id, expectedStatus, nextStatus string, expectedRevision int64, resources []ProjectEnvironmentCloneResource, errorCode string) (ProjectEnvironmentCloneOperation, error)
}
