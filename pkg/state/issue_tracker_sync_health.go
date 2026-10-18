package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Sync health is local evidence. Inspections never resume policies or authorize writes.
type IssueTrackerSyncHealthStore interface {
	GetIssueTrackerSyncHealth(context.Context, string, string, string, string) (IssueTrackerSyncHealth, error)
	RecordIssueTrackerSyncInspection(context.Context, IssueTrackerSyncInspection) error
}

type IssueTrackerSyncHealth struct {
	LastSuccessfulAt *time.Time
	History          []IssueTrackerSyncHistory
}

type IssueTrackerSyncHistory struct {
	ID, EventType, Direction, Outcome, Reason, SourceState, ObservedState, PendingState string
	PolicyRevision                                                                      int64
	CreatedAt                                                                           time.Time
}

type IssueTrackerSyncInspection struct {
	Record        IssueTrackerAutoSyncRecord
	GuardRevision int64
	Observed      api.IssueTrackerProviderStatus
	Reason        string
	Failed        bool
}
