package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Private, write-once metadata, not complete dataset/copy/readiness authority.
type ProjectEnvironmentClonePostgresInventory struct {
	Sealed     copyinventory.Sealed
	CapturedAt time.Time
}

type ProjectEnvironmentClonePostgresInventoryStore interface {
	ProjectEnvironmentClonePostgresInventoryScopeForLease(context.Context, ProjectEnvironmentCloneLease, string) (copyinventory.Scope, error)
	ProjectEnvironmentClonePostgresInventoryForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresInventory, error)
	RecordProjectEnvironmentClonePostgresInventory(context.Context, ProjectEnvironmentCloneLease, string, copyinventory.Sealed) (ProjectEnvironmentClonePostgresInventory, bool, error)
}

var _ ProjectEnvironmentClonePostgresInventoryStore = (*PgStore)(nil)
