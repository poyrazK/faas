package api

import "time"

// Issue teams are app-scoped routing targets. Membership never grants app access.
type IssueTeam struct {
	ID                 string    `json:"id"`
	Key                string    `json:"key"`
	Name               string    `json:"name"`
	MemberAccountIDs   []string  `json:"member_account_ids"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	UpdatedByAccountID string    `json:"updated_by_account_id,omitempty"`
}
type PutIssueTeamRequest struct {
	Name             string   `json:"name"`
	MemberAccountIDs []string `json:"member_account_ids"`
}
type ListIssueTeamsResponse struct {
	Items []IssueTeam `json:"items"`
}
