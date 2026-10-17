package api

import "time"

// IssueTrackerLinearConnection contains only non-secret connection metadata.
type IssueTrackerLinearConnection struct {
	AuthMethod           string     `json:"auth_method,omitempty"`
	OAuthStatus          string     `json:"oauth_status,omitempty"`
	AccessTokenExpiresAt *time.Time `json:"access_token_expires_at,omitempty"`
	WebhookEnabled       bool       `json:"webhook_enabled,omitempty"`
	CallbackPath         string     `json:"callback_path,omitempty"`
	Connected            bool       `json:"connected"`
	Revision             int64      `json:"revision"`
	WorkspaceID          string     `json:"workspace_id,omitempty"`
	TeamID               string     `json:"team_id,omitempty"`
	ViewerID             string     `json:"viewer_id,omitempty"`
}

// UpdateIssueTrackerLinearConnectionRequest explicitly connects or disconnects.
type UpdateIssueTrackerLinearConnectionRequest struct {
	WebhookSecret    string `json:"webhook_secret,omitempty"`
	Connected        bool   `json:"connected"`
	ExpectedRevision int64  `json:"expected_revision"`
	TeamID           string `json:"team_id,omitempty"`
	APIKey           string `json:"api_key,omitempty"`
}
