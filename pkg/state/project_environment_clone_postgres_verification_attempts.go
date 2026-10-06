package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
)

// The first row retains unmatched closure of the original owner. Later rows
// hold distinct owners, recipient/budget reservations and immutable evidence.
// Failed means closed without durable comparison evidence, not data equality.
type ProjectEnvironmentClonePostgresVerificationAttempt struct {
	ProjectEnvironmentClonePostgresVerification
	Attempt                                                      int32
	PreviousVerificationID                                       string
	PreviousOpenedAt, PreviousClosedAt, WindowOpenedAt, FailedAt time.Time
	TargetDatabaseOID                                            uint32
}

type ProjectEnvironmentClonePostgresVerificationFailure struct {
	Target      copyarchive.RestoreTarget
	Preparation copydatabases.Receipt
	Closure     copydatabases.VerificationClosure
}

type ProjectEnvironmentClonePostgresVerificationRetryRequest struct {
	ProjectEnvironmentClonePostgresVerificationRequest
	PreviousVerificationID string
}

// An opaque closure and a fresh lease are needed to record failure. A retry
// reservation grants retained proof storage only; native access still needs
// the original preparation and the actual predecessor closure.
type ProjectEnvironmentClonePostgresVerificationAttemptStore interface {
	ProjectEnvironmentClonePostgresVerificationAttemptsForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) ([]ProjectEnvironmentClonePostgresVerificationAttempt, error)
	RecordProjectEnvironmentClonePostgresVerificationFailure(context.Context, ProjectEnvironmentCloneLease, string, uint32, string, ProjectEnvironmentClonePostgresVerificationFailure) (ProjectEnvironmentClonePostgresVerificationAttempt, error)
	ReserveProjectEnvironmentClonePostgresVerificationRetry(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresVerificationRetryRequest) (ProjectEnvironmentClonePostgresVerificationAttempt, bool, error)
	ClaimProjectEnvironmentClonePostgresVerificationAttempt(context.Context, ProjectEnvironmentCloneLease, string, uint32, string) (ProjectEnvironmentClonePostgresVerificationAttempt, bool, error)
	RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(context.Context, ProjectEnvironmentCloneLease, string, uint32, string, copycontents.SealedMatch) (ProjectEnvironmentClonePostgresVerificationAttempt, error)
	RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(context.Context, ProjectEnvironmentCloneLease, string, uint32, string, ProjectEnvironmentClonePostgresVerificationCompletion) (ProjectEnvironmentClonePostgresVerificationAttempt, error)
}

var _ ProjectEnvironmentClonePostgresVerificationAttemptStore = (*PgStore)(nil)
