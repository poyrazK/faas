package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type IssueTrackerAutoSyncRecord struct {
	SourceRevision, PendingSourceRevision         int64
	Completed                                     bool // The atomic inbound transaction already completed this claim.
	AppID, AccountID, BindingFingerprint, ClaimID string
	ClaimUntil                                    time.Time
	Baseline                                      api.IssueTrackerProviderStatus
	Policy                                        api.IssueTrackerAutoSync
}
type IssueTrackerAutoSyncStore interface {
	GetIssueTrackerAutoSync(context.Context, string, string, string, string) (IssueTrackerAutoSyncRecord, error)
	ConfigureIssueTrackerAutoSync(context.Context, string, string, string, IssueTrackerAutoSyncRecord, int64) (IssueTrackerAutoSyncRecord, error)
	ClaimIssueTrackerAutoSync(context.Context) (IssueTrackerAutoSyncRecord, error)
	CheckIssueTrackerAutoSyncClaim(context.Context, IssueTrackerAutoSyncRecord) error
	MarkIssueTrackerAutoSyncPending(context.Context, IssueTrackerAutoSyncRecord, string) (IssueTrackerAutoSyncRecord, error)
	FinishIssueTrackerAutoSync(context.Context, IssueTrackerAutoSyncRecord) (IssueTrackerAutoSyncRecord, error)
}
