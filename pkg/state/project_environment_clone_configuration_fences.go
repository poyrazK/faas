package state

import (
	"context"
	"fmt"
	"time"
)

var ErrProjectEnvironmentCloneConfigurationFenced = fmt.Errorf("source configuration is held for stage capture: %w", ErrConflict)

// A held fence pins the current typed configuration catalogue across worker
// handoff. It has no lease expiry and is not a complete writer/capture proof.
type ProjectEnvironmentCloneConfigurationFence struct {
	AccountID, ProjectID, OperationID, SourceEnvironment, SourceRevisionHash string
	Generation                                                               int64
	HeldAt                                                                   time.Time
}

type ProjectEnvironmentCloneConfigurationFenceStore interface {
	AcquireProjectEnvironmentCloneConfigurationFence(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationFence, error)
	ProjectEnvironmentCloneConfigurationFenceForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationFence, error)
	AbandonProjectEnvironmentCloneConfigurationFence(context.Context, ProjectEnvironmentCloneLease) error
}
