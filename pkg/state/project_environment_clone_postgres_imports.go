package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
)

// Private import ownership is subordinate to one retained archive and one
// independent target reservation. Executed is not dataset or stage readiness.
// SQL names/credentials are never persisted by this ledger.
type ProjectEnvironmentClonePostgresImport struct {
	ImportID, State, ArchiveOwnerID, TargetDatabaseID string
	TargetProviderResourceID, TargetFingerprint       string
	TargetProviderCreatedAt, CreatedAt                time.Time
	ImportStartedAt, ExecutedAt                       time.Time
	Input                                             copyarchive.Receipt
}

func (i ProjectEnvironmentClonePostgresImport) MatchesTarget(t copyarchive.RestoreTarget) bool {
	fingerprint, err := t.Fingerprint()
	return err == nil && fingerprint == i.TargetFingerprint && t.OwnerID == i.TargetDatabaseID && t.ProviderResourceID == i.TargetProviderResourceID &&
		t.ProviderCreatedAt.Equal(i.TargetProviderCreatedAt) && t.Scope.Equal(i.Input.Scope)
}

type ProjectEnvironmentClonePostgresImportRequest struct {
	Input  copyarchive.Receipt
	Target copyarchive.RestoreTarget
}

type ProjectEnvironmentClonePostgresImportStore interface {
	ReserveProjectEnvironmentClonePostgresImport(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresImportRequest) (ProjectEnvironmentClonePostgresImport, bool, error)
	ProjectEnvironmentClonePostgresImportForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresImport, error)
	ClaimProjectEnvironmentClonePostgresImport(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresImport, bool, error)
	RecordProjectEnvironmentClonePostgresImportExecution(context.Context, ProjectEnvironmentCloneLease, string, uint32, copyarchive.RestoreExecution) (ProjectEnvironmentClonePostgresImport, error)
}

var _ ProjectEnvironmentClonePostgresImportStore = (*PgStore)(nil)
