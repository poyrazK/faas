package state

import (
	"context"
	"time"
)

type ProjectEnvironmentCloneCheckpointObjectPublication struct {
	Plan        ProjectEnvironmentCloneCheckpointObjectPublicationPlan
	State       string
	WithdrawnAt *time.Time
}

type ProjectEnvironmentCloneCheckpointObjectPublicationStore interface {
	PublishProjectEnvironmentCloneCheckpointObjects(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectPublication, error)
	WithdrawProjectEnvironmentCloneCheckpointObjects(context.Context, ProjectEnvironmentCloneLease) error
}
