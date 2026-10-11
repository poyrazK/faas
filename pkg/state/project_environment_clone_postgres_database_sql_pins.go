package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Each source database has one original encrypted preparation receipt. Exact
// complete-plan and archive reservation ownership are prerequisites; this ledger
// grants no SQL dispatch, archive import, data-resource identity or readiness.
type ProjectEnvironmentClonePostgresDatabaseSQLPins struct {
	Sealed                                                                 copydatabases.SealedPreparation
	DatabasePlanCiphertextSHA256, ArchiveReservationSHA256, ArchiveOwnerID string
	CapturedAt                                                             time.Time
}

type ProjectEnvironmentClonePostgresDatabaseSQLPinsStore interface {
	ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresDatabaseSQLPins, error)
	RecordProjectEnvironmentClonePostgresDatabaseSQLPins(context.Context, ProjectEnvironmentCloneLease, string, uint32, string, string, copydatabases.SealedPreparation) (ProjectEnvironmentClonePostgresDatabaseSQLPins, bool, error)
}

var _ ProjectEnvironmentClonePostgresDatabaseSQLPinsStore = (*PgStore)(nil)

// Pin every immutable field of the charged reservation while allowing its upload
// state and eventual archive receipt to progress. Source SQL OIDs are already the
// archive's durable index; SQL names and target identities remain encrypted.
func (a ProjectEnvironmentClonePostgresArchive) ReservationFingerprint() string {
	a.Scope.CapturePoint, a.Scope.SnapshotCreatedAt, a.Scope.CaptureCreatedAt = a.Scope.CapturePoint.UTC(), a.Scope.SnapshotCreatedAt.UTC(), a.Scope.CaptureCreatedAt.UTC()
	raw, _ := json.Marshal(struct {
		Scope                                                                           copyinventory.Scope
		DatabaseOID                                                                     uint32
		OwnerID, InventoryFingerprint, KeyID, StorageID, StorageFingerprint, StorageKey string
		ReservedBytes                                                                   int64
		CreatedAt                                                                       time.Time
	}{a.Scope, a.DatabaseOID, a.OwnerID, a.InventoryFingerprint, a.KeyID, a.StorageID, a.StorageFingerprint, a.StorageKey, a.ReservedBytes, a.CreatedAt.UTC()})
	h := sha256.Sum256(append([]byte("gregale-postgres-copy-archive-reservation-v1\x00"), raw...))
	return hex.EncodeToString(h[:])
}
