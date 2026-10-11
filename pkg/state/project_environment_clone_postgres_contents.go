package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Original contents evidence is privately owned before source SQL reads. The
// first ciphertext wins; captured is not import, writer-release or readiness.
type ProjectEnvironmentClonePostgresContents struct {
	Scope                                           copyinventory.Scope
	DatabaseOID                                     uint32
	OwnerID, State, KeyID                           string
	InventoryFingerprint, InventoryCiphertextSHA256 string
	ArchiveOwnerID, ArchiveReservationSHA256        string
	ReaderOwnerID, ReaderIdentitySHA256             string
	ReservedBytes                                   int64
	CreatedAt, CapturedAt                           time.Time
	Sealed                                          copycontents.Sealed
}

type ProjectEnvironmentClonePostgresContentsRequest struct {
	Scope                       copyinventory.Scope
	DatabaseOID                 uint32
	InventoryFingerprint, KeyID string
	ReservedBytes               int64
}

// These are structural account holds, not a storage entitlement or spool quota.
type ProjectEnvironmentClonePostgresContentsLimits struct {
	Count int
	Bytes int64
}

type ProjectEnvironmentClonePostgresContentsStore interface {
	ProjectEnvironmentClonePostgresContentsForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresContents, error)
	ReserveProjectEnvironmentClonePostgresContents(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresContentsRequest, ProjectEnvironmentClonePostgresContentsLimits) (ProjectEnvironmentClonePostgresContents, bool, error)
	RecordProjectEnvironmentClonePostgresContents(context.Context, ProjectEnvironmentCloneLease, string, uint32, copycontents.Sealed) (ProjectEnvironmentClonePostgresContents, error)
}

var _ ProjectEnvironmentClonePostgresContentsStore = (*PgStore)(nil)

// Bind original immutable reader pins while observations/cleanup may progress.
// This digest is identity, not evidence that a provider reader is still available.
func (r ProjectEnvironmentClonePostgresCopyReader) IdentityFingerprint() string {
	r.Scope.CapturePoint, r.Scope.SnapshotCreatedAt, r.Scope.CaptureCreatedAt = r.Scope.CapturePoint.UTC(), r.Scope.SnapshotCreatedAt.UTC(), r.Scope.CaptureCreatedAt.UTC()
	raw, _ := json.Marshal(struct {
		Scope                               copyinventory.Scope
		OwnerID, EndpointID                 string
		RequestStartedAt, EndpointCreatedAt time.Time
	}{r.Scope, r.OwnerID, r.EndpointID, r.RequestStartedAt.UTC(), r.EndpointCreatedAt.UTC()})
	h := sha256.Sum256(append([]byte("gregale-postgres-copy-reader-identity-v1\x00"), raw...))
	return hex.EncodeToString(h[:])
}
