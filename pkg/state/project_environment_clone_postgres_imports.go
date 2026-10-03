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
	ImportID, State, ArchiveOwnerID, TargetDatabaseID      string
	TargetProviderResourceID, TargetFingerprint            string
	DatabaseSQLPinsCiphertextSHA256                        string
	DatabasePlanCiphertextSHA256, ArchiveReservationSHA256 string
	TargetProviderCreatedAt, CreatedAt                     time.Time
	ImportStartedAt, ExecutedAt                            time.Time
	Input                                                  copyarchive.Receipt
}

// Legacy unbound owners cannot be adopted by the maintenance importer. An exact
// ciphertext identity binds this import to the first retained SQL preparation.
func (i ProjectEnvironmentClonePostgresImport) MatchesDatabaseSQLPins(p ProjectEnvironmentClonePostgresDatabaseSQLPins) bool {
	return i.DatabaseSQLPinsCiphertextSHA256 != "" && i.DatabaseSQLPinsCiphertextSHA256 == p.Sealed.CiphertextSHA256 &&
		i.DatabasePlanCiphertextSHA256 == p.DatabasePlanCiphertextSHA256 && i.ArchiveReservationSHA256 == p.ArchiveReservationSHA256 &&
		i.ArchiveOwnerID == p.ArchiveOwnerID && i.TargetFingerprint == p.Sealed.Fingerprint && i.TargetDatabaseID == p.Sealed.OwnerID &&
		i.TargetProviderResourceID == p.Sealed.ProviderResourceID && i.TargetProviderCreatedAt.Equal(p.Sealed.ProviderCreatedAt) && i.Input.Scope.Equal(p.Sealed.Scope)
}

func (i ProjectEnvironmentClonePostgresImport) MatchesTarget(t copyarchive.RestoreTarget) bool {
	fingerprint, err := t.Fingerprint()
	return err == nil && fingerprint == i.TargetFingerprint && t.OwnerID == i.TargetDatabaseID && t.ProviderResourceID == i.TargetProviderResourceID &&
		t.ProviderCreatedAt.Equal(i.TargetProviderCreatedAt) && t.Scope.Equal(i.Input.Scope)
}

type ProjectEnvironmentClonePostgresImportRequest struct {
	Input                                                  copyarchive.Receipt
	Target                                                 copyarchive.RestoreTarget
	DatabaseSQLPinsCiphertextSHA256                        string
	DatabasePlanCiphertextSHA256, ArchiveReservationSHA256 string
}

func (r ProjectEnvironmentClonePostgresImportRequest) bindingValid() bool {
	if r.DatabaseSQLPinsCiphertextSHA256 == "" && r.DatabasePlanCiphertextSHA256 == "" && r.ArchiveReservationSHA256 == "" {
		return true
	}
	return validCloneObjectSHA256(r.DatabaseSQLPinsCiphertextSHA256) && validCloneObjectSHA256(r.DatabasePlanCiphertextSHA256) && validCloneObjectSHA256(r.ArchiveReservationSHA256)
}

type ProjectEnvironmentClonePostgresImportStore interface {
	ReserveProjectEnvironmentClonePostgresImport(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresImportRequest) (ProjectEnvironmentClonePostgresImport, bool, error)
	ProjectEnvironmentClonePostgresImportForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresImport, error)
	ClaimProjectEnvironmentClonePostgresImport(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresImport, bool, error)
	RecordProjectEnvironmentClonePostgresImportExecution(context.Context, ProjectEnvironmentCloneLease, string, uint32, copyarchive.RestoreExecution) (ProjectEnvironmentClonePostgresImport, error)
}

var _ ProjectEnvironmentClonePostgresImportStore = (*PgStore)(nil)
