package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProjectEnvironmentCloneLease belongs to an internal worker. The token must
// never enter public status/receipt output. Keep Operation at the revision
// returned by the worker's latest successful state transition before renewal
// or release. Provider calls must use the lease deadline and frozen identities.
type ProjectEnvironmentCloneLease struct {
	Operation    ProjectEnvironmentCloneOperation
	Token        string `json:"-"`
	ExpiresAt    time.Time
	AttemptCount int32
}

type ProjectEnvironmentCloneWorkerLeaseStore interface {
	ClaimNextProjectEnvironmentClone(context.Context, string, time.Duration) (ProjectEnvironmentCloneLease, error)
	RenewProjectEnvironmentCloneLease(context.Context, ProjectEnvironmentCloneLease, time.Duration) (ProjectEnvironmentCloneLease, error)
	ReleaseProjectEnvironmentCloneLease(context.Context, ProjectEnvironmentCloneLease, time.Duration) error
}

type projectEnvironmentCloneLeaseState struct {
	token                string
	until, nextAttemptAt time.Time
	attemptCount         int32
}

func cloneOperationWorkerEligible(status string) bool {
	switch status {
	case CloneOperationPending, CloneOperationCapturing, CloneOperationCopying, CloneOperationPublishing, CloneOperationCompensating:
		return true
	default:
		return false
	}
}

func validCloneLeaseToken(token string) bool {
	id, err := uuid.Parse(token)
	return err == nil && id != uuid.Nil && id.String() == token
}

func validCloneLeaseDuration(duration time.Duration) bool {
	return duration > 0 && duration%time.Microsecond == 0
}

func validCloneLeaseIdentity(lease ProjectEnvironmentCloneLease) bool {
	op := lease.Operation
	return op.ID != "" && op.AccountID != "" && op.ProjectID != "" && op.Revision > 0 && validCloneLeaseToken(lease.Token)
}

var _ ProjectEnvironmentCloneWorkerLeaseStore = (*MemStore)(nil)
var _ ProjectEnvironmentCloneWorkerLeaseStore = (*PgStore)(nil)
