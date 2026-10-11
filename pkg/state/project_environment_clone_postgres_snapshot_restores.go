package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Private native storage intent. Restored is not target catalogue readiness,
// auxiliary SQL isolation, common-point consistency or publication authority.
type ProjectEnvironmentClonePostgresSnapshotRestore struct {
	OperationID, SourceDatabaseID, AccountID, TargetOwnerID        string
	BackendID, BackendFingerprint, State, TargetProviderResourceID string
	TargetCreatedAt, RequestStartedAt, ObservedAt, RestoredAt      time.Time
	DeletionStartedAt, DeletedAt                                   time.Time
	DeletionOperations                                             string
	AdoptedDatabaseID                                              string
	AdoptedAt                                                      time.Time
}

// Transfers the existing native owner into an unready private database row.
// It supplies no target readiness, SQL isolation or publication authority.
type ProjectEnvironmentClonePostgresSnapshotRestoreAdoptionStore interface {
	AdoptProjectEnvironmentClonePostgresSnapshotRestore(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneDatabaseTarget, bool, error)
}

func ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(op ProjectEnvironmentCloneOperation, sourceID string) string {
	sum := sha256.Sum256([]byte(op.ID + "\x00" + sourceID))
	return "checkpoint-" + hex.EncodeToString(sum[:12])
}

type ProjectEnvironmentClonePostgresSnapshotRestoreDeletion struct {
	TargetProviderResourceID string
	OperationIDs             []string
	Done                     bool
}

type ProjectEnvironmentClonePostgresSnapshotRestoreCleanupStore interface {
	ProjectEnvironmentClonePostgresSnapshotRestoreStore
	BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
	RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
	RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
	FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
}

type ProjectEnvironmentClonePostgresSnapshotRestoreObservation struct {
	ProviderSnapshotID, SourceDataResourceID, TargetProviderResourceID string
	CapturePoint, SnapshotCreatedAt, TargetCreatedAt                   time.Time
	Restored                                                           bool
}

type ProjectEnvironmentClonePostgresSnapshotRestoreStore interface {
	ReserveProjectEnvironmentClonePostgresSnapshotRestore(context.Context, ProjectEnvironmentCloneLease, string, int) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
	ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshotRestore, bool, error)
	ProjectEnvironmentClonePostgresSnapshotRestoreForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
	RecordProjectEnvironmentClonePostgresSnapshotRestore(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (ProjectEnvironmentClonePostgresSnapshotRestore, error)
}

func validateCloneSnapshotRestoreObservation(snapshot ProjectEnvironmentClonePostgresSnapshot, receipt ProjectEnvironmentClonePostgresSnapshotRestore, observed ProjectEnvironmentClonePostgresSnapshotRestoreObservation) error {
	if snapshot.State != "retained" || snapshot.OperationID != receipt.OperationID || snapshot.SourceDatabaseID != receipt.SourceDatabaseID ||
		snapshot.BackendID != receipt.BackendID || snapshot.BackendFingerprint != receipt.BackendFingerprint ||
		observed.ProviderSnapshotID != snapshot.ProviderSnapshotID || observed.SourceDataResourceID != snapshot.SourceDataResourceID ||
		!observed.CapturePoint.Equal(snapshot.CapturePoint) || !observed.SnapshotCreatedAt.Equal(snapshot.SnapshotCreatedAt) ||
		!validCloneSnapshotID(observed.TargetProviderResourceID) || observed.TargetProviderResourceID == snapshot.SourceDataResourceID ||
		observed.TargetProviderResourceID == snapshot.SourceProviderResourceID || observed.TargetCreatedAt.Before(snapshot.SnapshotCreatedAt) ||
		observed.TargetCreatedAt.Nanosecond()%1000 != 0 || receipt.State == "reserved" ||
		receipt.TargetProviderResourceID != "" && (receipt.TargetProviderResourceID != observed.TargetProviderResourceID || !receipt.TargetCreatedAt.Equal(observed.TargetCreatedAt)) ||
		receipt.State == "restored" && !observed.Restored {
		return ErrConflict
	}
	return nil
}

var _ ProjectEnvironmentClonePostgresSnapshotRestoreStore = (*PgStore)(nil)
var _ ProjectEnvironmentClonePostgresSnapshotRestoreCleanupStore = (*PgStore)(nil)
var _ ProjectEnvironmentClonePostgresSnapshotRestoreAdoptionStore = (*PgStore)(nil)
