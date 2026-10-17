package api

import "time"

type PutIssueTrackerLinkRequest struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}
type IssueTrackerLink struct {
	ID                 string    `json:"id"`
	OriginalIssueID    string    `json:"original_issue_id"`
	Provider           string    `json:"provider"`
	ExternalKey        string    `json:"external_key"`
	URL                string    `json:"url"`
	Title              string    `json:"title,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	CreatedByAccountID string    `json:"created_by_account_id,omitempty"`
	UpdatedByAccountID string    `json:"updated_by_account_id,omitempty"`
}
type ListIssueTrackerLinksResponse struct {
	Items      []IssueTrackerLink `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
