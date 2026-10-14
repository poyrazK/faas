package api

import "time"

// IssueTrackerAutoSync reports the opt-in Gregale-to-GitHub policy and durable worker outcome.
type IssueTrackerAutoSync struct {
	LinkID        string     `json:"link_id"`
	Enabled       bool       `json:"enabled"`
	Revision      int64      `json:"revision"`
	SourceIssueID string     `json:"source_issue_id"`
	SourceState   string     `json:"source_state,omitempty"`
	ProviderState string     `json:"provider_state,omitempty"`
	PendingState  string     `json:"pending_state,omitempty"`
	Outcome       string     `json:"outcome"`
	Reason        string     `json:"reason,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt   *time.Time `json:"next_check_at,omitempty"`
}

// UpdateIssueTrackerAutoSyncRequest sets one policy against a known revision and aligned native snapshot.
type UpdateIssueTrackerAutoSyncRequest struct {
	Enabled            bool                      `json:"enabled"`
	ExpectedRevision   int64                     `json:"expected_revision"`
	ExpectedStatus     *NativeIssueTrackerStatus `json:"expected_status,omitempty"`
	ExpectedIssueState string                    `json:"expected_issue_state,omitempty"`
}
