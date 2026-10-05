package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Reader ownership is separate from a dataset receipt. Availability is observed
// compute metadata and must be independently rechecked before private SQL use.
type ProjectEnvironmentClonePostgresCopyReader struct {
	Scope                                              copyinventory.Scope
	OwnerID, State, EndpointID                         string
	RequestStartedAt, EndpointCreatedAt, ObservedAt    time.Time
	Available                                          bool
	CleanupRequestedAt, CleanupDispatchedAt, RetiredAt time.Time
	DeletionOperations, CaptureDeletionOperations      string
}

type ProjectEnvironmentClonePostgresCopyReaderObservation struct {
	EndpointID string
	CreatedAt  time.Time
	Available  bool
}

type ProjectEnvironmentClonePostgresCopyReaderDeletion struct {
	EndpointID          string
	CreatedAt           time.Time
	OperationIDs        []string
	CaptureOperationIDs []string
	Done                bool
}

type ProjectEnvironmentClonePostgresCopyReaderStore interface {
	ReserveProjectEnvironmentClonePostgresCopyReader(context.Context, ProjectEnvironmentCloneLease, string, int) (ProjectEnvironmentClonePostgresCopyReader, bool, error)
	ProjectEnvironmentClonePostgresCopyReaderForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyReader, error)
	ClaimProjectEnvironmentClonePostgresCopyReaderRequest(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyReader, bool, error)
	RecordProjectEnvironmentClonePostgresCopyReader(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresCopyReaderObservation) (ProjectEnvironmentClonePostgresCopyReader, error)
	BeginProjectEnvironmentClonePostgresCopyReaderCleanup(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyReader, error)
	ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresCopyReader, bool, error)
	RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresCopyReaderDeletion) (ProjectEnvironmentClonePostgresCopyReader, error)
	FinishProjectEnvironmentClonePostgresCopyReaderCleanup(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresCopyReaderDeletion) (ProjectEnvironmentClonePostgresCopyReader, error)
}

var _ ProjectEnvironmentClonePostgresCopyReaderStore = (*PgStore)(nil)
