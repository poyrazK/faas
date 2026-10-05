package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Private transfer ownership. Retained means the encrypted archive was read
// back and verified, not that every database/global was imported or is ready.
type ProjectEnvironmentClonePostgresArchive struct {
	Scope                                       copyinventory.Scope
	DatabaseOID                                 uint32
	OwnerID, State, InventoryFingerprint, KeyID string
	StorageID, StorageFingerprint, StorageKey   string
	ReservedBytes                               int64
	CreatedAt, UploadStartedAt, RetainedAt      time.Time
	Receipt                                     copyarchive.Receipt
}

type ProjectEnvironmentClonePostgresArchiveRequest struct {
	Scope                                                      copyinventory.Scope
	DatabaseOID                                                uint32
	InventoryFingerprint, KeyID, StorageID, StorageFingerprint string
	ReservedBytes                                              int64
}

// Limits are temporary structural admission, not customer storage entitlement.
// All reservations continue charging until a qualified cleanup is implemented.
type ProjectEnvironmentClonePostgresArchiveLimits struct {
	Count int
	Bytes int64
}

type ProjectEnvironmentClonePostgresArchiveStore interface {
	ReserveProjectEnvironmentClonePostgresArchive(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresArchiveRequest, ProjectEnvironmentClonePostgresArchiveLimits) (ProjectEnvironmentClonePostgresArchive, bool, error)
	ProjectEnvironmentClonePostgresArchiveForLease(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresArchive, error)
	ClaimProjectEnvironmentClonePostgresArchiveUpload(context.Context, ProjectEnvironmentCloneLease, string, uint32) (ProjectEnvironmentClonePostgresArchive, bool, error)
	RecordProjectEnvironmentClonePostgresArchive(context.Context, ProjectEnvironmentCloneLease, string, uint32, copyarchive.Receipt) (ProjectEnvironmentClonePostgresArchive, error)
}

var _ ProjectEnvironmentClonePostgresArchiveStore = (*PgStore)(nil)
