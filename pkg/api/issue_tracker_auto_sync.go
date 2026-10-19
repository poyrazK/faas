package api

import "time"

// IssueTrackerAutoSync reports the opt-in tracker direction and durable worker outcome.
type IssueTrackerAutoSync struct {
	Direction             string     `json:"direction,omitempty"`
	ObservedState         string     `json:"observed_state,omitempty"`
	LinearOpenStateID     string     `json:"linear_open_state_id,omitempty"`
	LinearResolvedStateID string     `json:"linear_resolved_state_id,omitempty"`
	LinkID                string     `json:"link_id"`
	Enabled               bool       `json:"enabled"`
	Revision              int64      `json:"revision"`
	SourceIssueID         string     `json:"source_issue_id"`
	SourceState           string     `json:"source_state,omitempty"`
	ProviderState         string     `json:"provider_state,omitempty"`
	PendingState          string     `json:"pending_state,omitempty"`
	Outcome               string     `json:"outcome"`
	Reason                string     `json:"reason,omitempty"`
	LastCheckedAt         *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt           *time.Time `json:"next_check_at,omitempty"`
}

// UpdateIssueTrackerAutoSyncRequest sets one policy against a known revision and aligned native snapshot.
type UpdateIssueTrackerAutoSyncRequest struct {
	Direction             string                          `json:"direction,omitempty"`
	ExpectedLinearStatus  *NativeLinearIssueTrackerStatus `json:"expected_linear_status,omitempty"`
	LinearOpenStateID     string                          `json:"linear_open_state_id,omitempty"`
	LinearResolvedStateID string                          `json:"linear_resolved_state_id,omitempty"`
	Enabled               bool                            `json:"enabled"`
	ExpectedRevision      int64                           `json:"expected_revision"`
	ExpectedStatus        *NativeIssueTrackerStatus       `json:"expected_status,omitempty"`
	ExpectedIssueState    string                          `json:"expected_issue_state,omitempty"`
}

// NormalizeIssueTrackerSyncDirection preserves outbound behavior for older clients.
func NormalizeIssueTrackerSyncDirection(direction string) (string, error) {
	switch direction {
	case "", "outbound":
		return "outbound", nil
	case "inbound", "both":
		return direction, nil
	default:
		return "", ErrValidation("direction must be outbound, inbound or both")
	}
}
