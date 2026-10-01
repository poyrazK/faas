package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// IssueStore is a focused persistence boundary owned by apid. Daemons send
// evidence to apid; they never open their own connection to these tables.
type IssueStore interface {
	CreateIssueToken(context.Context, string, string, api.CreateIssueIngestTokenRequest, []byte, api.IssueLimits) (api.IssueIngestToken, error)
	FindIssueToken(context.Context, []byte, time.Time) (IssueCredential, error)
	ListIssueTokens(context.Context, string) ([]api.IssueIngestToken, error)
	RevokeIssueToken(context.Context, string, string) error
	RecordIssue(context.Context, RecordIssueParams) (api.IssueEventResponse, error)
	ListIssues(context.Context, string, IssueListFilter, IssueCursor) (api.ListIssuesResponse, error)
	GetIssueDetail(context.Context, string, string, time.Time, time.Time, IssueDetailCursors) (api.IssueDetail, error)
	ActOnIssue(context.Context, string, string, string, api.IssueActionRequest, time.Time) (api.Issue, error)
}

type IssueCredential struct {
	ID, AccountID, AppID, DeploymentID, Environment string
}

type RecordIssueParams struct {
	Credential                      IssueCredential
	Event                           api.IssueEvent
	Fingerprint, Title, PayloadHash string
	GroupingVersion                 int
	Limits                          api.IssueLimits
	Now                             time.Time
}

type IssueCursor struct {
	Time time.Time
	ID   string

	// Impact fields are present only on list cursors whose query ranks or
	// filters by the fixed 24-hour customer-impact window.
	Sort            string     `json:"sort,omitempty"`
	MinCustomers    int64      `json:"min_customers,omitempty"`
	ImpactCustomers int64      `json:"impact_customers,omitempty"`
	ImpactWindowEnd *time.Time `json:"impact_window_end,omitempty"`
}

// IssueListFilter scopes an issue inbox without changing its pagination shape.
type IssueListFilter struct {
	State             string
	Environment       string
	AssigneeAccountID string
	Unassigned        bool
	Sort              string
	MinCustomers      int64
}

var ErrIssueEventConflict = errors.New("issue event ID reused with another payload")
var ErrIssueQuota = errors.New("issue storage quota exceeded")
var ErrIssueRateLimited = errors.New("issue ingestion rate exceeded")

type IssueDetailCursors struct{ Events, Releases, Activity IssueCursor }

// IssueLimitError supplies the enforced bound without exposing event data.
type IssueLimitError struct {
	Resource        string
	Limit, Observed int64
	Rate            bool
}

func (e *IssueLimitError) Error() string { return "issue limit exceeded: " + e.Resource }
func (e *IssueLimitError) Unwrap() error {
	if e.Rate {
		return ErrIssueRateLimited
	}
	return ErrIssueQuota
}
