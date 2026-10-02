package state

import (
	"context"
	"time"
)

// This is private ownership of the independent project's preparation, not an
// immutable export/import receipt or a ready stage database.
type ProjectEnvironmentClonePostgresCopyTarget struct {
	OperationID, SourceDatabaseID, AccountID, CaptureDatabaseID, TargetDatabaseID string
	State, ProviderResourceID                                                     string
	RequestStartedAt, ProviderCreatedAt, ObservedAt, PreparedAt, RetiredAt        time.Time
}

type ProjectEnvironmentClonePostgresCopyTargetObservation struct {
	ProviderResourceID string
	CreatedAt          time.Time
	Prepared           bool
}

type ProjectEnvironmentClonePostgresCopyTargetStore interface {
	ReserveProjectEnvironmentClonePostgresCopyTarget(context.Context, ProjectEnvironmentCloneLease, string, int) (ProjectEnvironmentClonePostgresCopyTarget, bool, error)
	ProjectEnvironmentClonePostgresCopyTargetForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyTarget, error)
	ClaimProjectEnvironmentClonePostgresCopyTargetRequest(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyTarget, bool, error)
	RecordProjectEnvironmentClonePostgresCopyTarget(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresCopyTargetObservation) (ProjectEnvironmentClonePostgresCopyTarget, error)
	// Only an undispatched reservation can retire without provider evidence.
	RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyTarget, error)
}

var _ ProjectEnvironmentClonePostgresCopyTargetStore = (*PgStore)(nil)
