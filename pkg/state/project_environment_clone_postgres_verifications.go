package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Verification is subordinate to the first contents, import and child SQL pins.
// Compared is not closed access; verified is not complete dataset/stage readiness.
type ProjectEnvironmentClonePostgresVerification struct {
	Scope                                                                                copyinventory.Scope
	DatabaseOID                                                                          uint32
	VerificationID, State, ContentsOwnerID, ContentsCiphertextSHA256                     string
	ManifestFingerprint, ImportID, DatabaseSQLPinsCiphertextSHA256                       string
	DatabasePlanCiphertextSHA256, ArchiveReservationSHA256, TargetFingerprint            string
	KeyID                                                                                string
	ReservedBytes                                                                        int64
	CreatedAt, ImportStartedAt, RequestStartedAt, ComparedAt, NativeClosedAt, VerifiedAt time.Time
	Sealed                                                                               copycontents.SealedMatch
}

type ProjectEnvironmentClonePostgresVerificationRequest struct {
	Scope                                                     copyinventory.Scope
	DatabaseOID                                               uint32
	ContentsCiphertextSHA256, DatabaseSQLPinsCiphertextSHA256 string
	ImportID, TargetFingerprint, KeyID                        string
	ReservedBytes                                             int64
}

// Actual opaque capabilities are required for final closure publication. A
// metadata-only command receipt or synthesized timestamp cannot complete this.
type ProjectEnvironmentClonePostgresVerificationCompletion struct {
	Manifest    copycontents.Manifest
	Match       copycontents.RetainedMatch
	Target      copyarchive.RestoreTarget
	Preparation copydatabases.Receipt
	Closure     copydatabases.VerificationClosure
}

type ProjectEnvironmentClonePostgresVerificationStore interface {
	ProjectEnvironmentClonePostgresVerificationForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresVerification, error)
	ReserveProjectEnvironmentClonePostgresVerification(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresVerificationRequest) (ProjectEnvironmentClonePostgresVerification, bool, error)
	ClaimProjectEnvironmentClonePostgresVerification(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresVerification, bool, error)
	RecordProjectEnvironmentClonePostgresVerificationMatch(context.Context, ProjectEnvironmentCloneLease, string, uint32, copycontents.SealedMatch) (ProjectEnvironmentClonePostgresVerification, error)
	RecordProjectEnvironmentClonePostgresVerificationClosure(context.Context, ProjectEnvironmentCloneLease, string, uint32, ProjectEnvironmentClonePostgresVerificationCompletion) (ProjectEnvironmentClonePostgresVerification, error)
}

var _ ProjectEnvironmentClonePostgresVerificationStore = (*PgStore)(nil)
