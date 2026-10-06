package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
)

// One encrypted, write-once bootstrap identity for an independently prepared
// target. This record grants no SQL dispatch, data-resource identity or readiness.
type ProjectEnvironmentClonePostgresTargetSQLPins struct {
	Sealed               copyarchive.SealedTarget
	InventoryFingerprint string
	CapturedAt           time.Time
}

type ProjectEnvironmentClonePostgresTargetSQLPinsStore interface {
	ProjectEnvironmentClonePostgresTargetSQLPinsForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresTargetSQLPins, error)
	RecordProjectEnvironmentClonePostgresTargetSQLPins(context.Context, ProjectEnvironmentCloneLease, string, string, copyarchive.SealedTarget) (ProjectEnvironmentClonePostgresTargetSQLPins, bool, error)
}

var _ ProjectEnvironmentClonePostgresTargetSQLPinsStore = (*PgStore)(nil)
