package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// IssueTrackerInboundStore applies an explicitly reviewed provider change under
// the same app intent fence used by dashboard previews.
type IssueTrackerInboundStore interface {
	GetIssueTrackerInboundGuard(context.Context, string, string) (int64, error)
	ListIssueTrackerInboundDeployments(context.Context, string, string, string) ([]IssueTrackerFixingDeployment, error)
	ApplyIssueTrackerInbound(context.Context, IssueTrackerInboundApply) (api.Issue, error)
}

type IssueTrackerInboundApply struct {
	Record            IssueTrackerAutoSyncRecord
	GuardRevision     int64
	Observed          api.IssueTrackerProviderStatus
	FixedDeploymentID string
	Now               time.Time
}

func IssueTrackerInboundChanged() error {
	return api.NewProblem(409, "issue_tracker_inbound_changed", "Inbound sync needs a fresh review", "issue intent, tracker connection or sync policy changed; preview again")
}

type IssueTrackerFixingDeployment struct {
	ID, CommitSHA string
	CreatedAt     time.Time
}
