package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
)

// One write-once role plan per independently prepared target. The original
// inventory and bootstrap pin ciphertext hashes also bind its prerequisites.
// This record contains no plaintext SQL identifiers and proves no executed DDL.
type ProjectEnvironmentClonePostgresRolePlan struct {
	Sealed                                                copyroles.Sealed
	TargetDatabaseID                                      string
	InventoryCiphertextSHA256, TargetPinsCiphertextSHA256 string
	CapturedAt                                            time.Time
}

type ProjectEnvironmentClonePostgresRolePlanStore interface {
	ProjectEnvironmentClonePostgresRolePlanForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresRolePlan, error)
	RecordProjectEnvironmentClonePostgresRolePlan(context.Context, ProjectEnvironmentCloneLease, string, copyroles.Sealed) (ProjectEnvironmentClonePostgresRolePlan, bool, error)
}

var _ ProjectEnvironmentClonePostgresRolePlanStore = (*PgStore)(nil)
