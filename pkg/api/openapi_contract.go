package api

import "time"

// OpenAPIContractDiffResponse is the read-only production contract check.
// It is intentionally separate from DiffResponse: deploydiff describes
// application configuration changes, while this response describes the
// customer-facing OpenAPI surface.
type OpenAPIContractDiffResponse struct {
	AppID                string                    `json:"app_id"`
	Scope                string                    `json:"scope"`
	BaselineDeploymentID string                    `json:"baseline_deployment_id,omitempty"`
	BaselineSHA256       string                    `json:"baseline_sha256,omitempty"`
	ProposedSHA256       string                    `json:"proposed_sha256"`
	BaselineCapturedAt   *time.Time                `json:"baseline_captured_at,omitempty"`
	Blocking             bool                      `json:"blocking"`
	Breaks               []OpenAPIContractBreak    `json:"breaks"`
	Additions            []OpenAPIContractAddition `json:"additions"`
}

type OpenAPIContractBreak struct {
	Path         string      `json:"path"`
	Method       string      `json:"method"`
	Status       string      `json:"status,omitempty"`
	Kind         string      `json:"kind"`
	PathInSchema string      `json:"path_in_schema,omitempty"`
	Before       interface{} `json:"before,omitempty"`
	After        interface{} `json:"after,omitempty"`
}

type OpenAPIContractAddition struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Method       string `json:"method,omitempty"`
	Status       string `json:"status,omitempty"`
	PathInSchema string `json:"path_in_schema,omitempty"`
	Field        string `json:"field"`
}
