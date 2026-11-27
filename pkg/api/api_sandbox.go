package api

import "time"

const (
	APISandboxSlugPrefix              = "api-sbx-"
	APISandboxPartnerInvitePrefix     = "sbx_inv_"
	APISandboxPartnerConsumerRef      = "gregale-api-sandbox-partner"
	APISandboxVerificationConsumerRef = "gregale-api-sandbox-verification"
)

type APISandboxCreateRequest struct {
	SmokePath        string `json:"smoke_path,omitempty"`
	ScenarioRevision string `json:"scenario_revision,omitempty"`
	PartnerLabel     string `json:"partner_label,omitempty"`
}

type APISandboxVerification struct {
	DeploymentID  string    `json:"deployment_id"`
	CheckedAt     time.Time `json:"checked_at"`
	OpenAPIStatus int       `json:"openapi_status"`
	SmokeStatus   int       `json:"smoke_status,omitempty"`
}

type APISandboxFailure struct {
	Code       string `json:"code"`
	Detail     string `json:"detail"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// APISandboxResponse describes an expiring deployment of a retained release.
type APISandboxResponse struct {
	ID                 string                     `json:"id"`
	SourceDeploymentID string                     `json:"source_deployment_id"`
	DeploymentID       string                     `json:"deployment_id,omitempty"`
	Status             string                     `json:"status"`
	URL                string                     `json:"url"`
	OpenAPIURL         string                     `json:"openapi_url"`
	ExpiresAt          time.Time                  `json:"expires_at"`
	SmokePath          string                     `json:"smoke_path,omitempty"`
	Failure            *APISandboxFailure         `json:"failure,omitempty"`
	Verification       *APISandboxVerification    `json:"verification,omitempty"`
	Scenario           *APISandboxScenarioReceipt `json:"scenario,omitempty"`
	Partner            *APISandboxPartnerResponse `json:"partner,omitempty"`
	PartnerStepCount   int                        `json:"partner_step_count,omitempty"`
}

type APISandboxListResponse struct {
	Sandboxes []APISandboxResponse `json:"sandboxes"`
}

// APISandboxInviteResponse returns a sandbox credential once. The caller
// should use the token in an HTTPS Authorization header.
type APISandboxInviteResponse struct {
	Sandbox APISandboxResponse `json:"sandbox"`
	Token   string             `json:"token"`
}

// APISandboxPartnerResponse is owner-only progress for a private handoff.
type APISandboxPartnerResponse struct {
	Label          string     `json:"label"`
	InvitedAt      *time.Time `json:"invited_at,omitempty"`
	RedeemedAt     *time.Time `json:"redeemed_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	FirstSuccessAt *time.Time `json:"first_success_at,omitempty"`
}

type APISandboxPartnerInviteResponse struct {
	Partner APISandboxPartnerResponse `json:"partner"`
	Token   string                    `json:"token"`
}
