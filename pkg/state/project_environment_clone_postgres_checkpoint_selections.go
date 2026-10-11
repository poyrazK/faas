package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
)

// Private immutable intent precedes connection closure. Retained does not
// attest complete selection, admission closure, drainage or a capture point.
type ProjectEnvironmentClonePostgresCheckpointSelection struct {
	Sealed     checkpointselection.Sealed
	RetainedAt time.Time
}

type ProjectEnvironmentClonePostgresCheckpointSelectionStore interface {
	ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(context.Context, ProjectEnvironmentCloneLease, string) (checkpointselection.Scope, error)
	ProjectEnvironmentClonePostgresCheckpointSelectionForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCheckpointSelection, error)
	RecordProjectEnvironmentClonePostgresCheckpointSelection(context.Context, ProjectEnvironmentCloneLease, string, checkpointselection.Sealed) (ProjectEnvironmentClonePostgresCheckpointSelection, bool, error)
}
