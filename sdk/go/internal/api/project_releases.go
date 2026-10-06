package api

import "time"

type PublishProjectReleaseSetRequest struct {
	// Present (including an empty string for no graph) selects checked activation.
	ExpectedActiveReleaseID *string           `json:"expected_active_release_id,omitempty"`
	TTLSeconds              int               `json:"ttl_seconds"`
	Deployments             map[string]string `json:"deployments"`
}

type ProjectReleaseSetResponse struct {
	BindingsCheck *ProjectReleaseCheckResponse      `json:"bindings_check,omitempty"`
	ID            string                            `json:"id"`
	AccountID     string                            `json:"account_id"`
	ProjectID     string                            `json:"project_id"`
	Environment   string                            `json:"environment"`
	Active        bool                              `json:"active"`
	TTLSeconds    int                               `json:"ttl_seconds"`
	ExpiresAt     *time.Time                        `json:"expires_at,omitempty"`
	CreatedAt     time.Time                         `json:"created_at"`
	Members       []ProjectReleaseSetMemberResponse `json:"members"`
}

type ProjectReleaseSetMemberResponse struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
}

type ProjectReleaseSetListResponse struct {
	Items      []ProjectReleaseSetResponse `json:"items"`
	NextBefore string                      `json:"next_before,omitempty"`
}
