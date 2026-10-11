package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
)

// One private encrypted database creation plan per independently prepared target. Exact
// inventory, bootstrap-pin and role-plan ciphertexts remain its prerequisites.
// Seed SQL OIDs/time and all raw database configuration stay inside authenticated encryption.
type ProjectEnvironmentClonePostgresDatabasePlan struct {
	Sealed                                                                          copydatabases.Sealed
	TargetDatabaseID                                                                string
	InventoryCiphertextSHA256, TargetPinsCiphertextSHA256, RolePlanCiphertextSHA256 string
	CapturedAt                                                                      time.Time
}

type ProjectEnvironmentClonePostgresDatabasePlanStore interface {
	ProjectEnvironmentClonePostgresDatabasePlanForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresDatabasePlan, error)
	RecordProjectEnvironmentClonePostgresDatabasePlan(context.Context, ProjectEnvironmentCloneLease, string, string, copydatabases.Sealed) (ProjectEnvironmentClonePostgresDatabasePlan, bool, error)
}

var _ ProjectEnvironmentClonePostgresDatabasePlanStore = (*PgStore)(nil)
