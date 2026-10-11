package api

import (
	"context"
	"net/url"
)

// WorkflowRunDiagnosticsResponse is a read-only observation, not an admission
// reservation. Resume rechecks the current generation and all admission gates.
type WorkflowRunDiagnosticsResponse struct {
	RunID          string                   `json:"run_id"`
	WorkflowName   string                   `json:"workflow_name"`
	Status         string                   `json:"status"`
	ObservedAt     string                   `json:"observed_at"`
	DeploymentID   string                   `json:"deployment_id,omitempty"`
	LegacyUnpinned bool                     `json:"legacy_unpinned"`
	StateReason    string                   `json:"state_reason"`
	NextWakeAt     *string                  `json:"next_wake_at,omitempty"`
	DueAgeSeconds  float64                  `json:"due_age_seconds"`
	StaleLease     bool                     `json:"stale_lease"`
	Steps          []WorkflowDiagnosticStep `json:"steps"`
	Resume         WorkflowResumePreview    `json:"resume"`
}

type WorkflowDiagnosticStep struct {
	StepName    string  `json:"step_name"`
	Status      string  `json:"status"`
	Kind        string  `json:"kind"`
	Attempt     int     `json:"attempt"`
	RetryBase   int     `json:"retry_base"`
	NextRetryAt *string `json:"next_retry_at,omitempty"`
	NextCheckAt *string `json:"next_check_at,omitempty"`
}

type WorkflowResumePreview struct {
	Eligible            bool                        `json:"eligible"`
	ExpectedResumeCount int                         `json:"expected_resume_count"`
	ReopenedSteps       []string                    `json:"reopened_steps"`
	PreservedSteps      []string                    `json:"preserved_steps"`
	Blockers            []WorkflowDiagnosticBlocker `json:"blockers"`
}

// Code and StepName contain no input, output, error text or credential values.
type WorkflowDiagnosticBlocker struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	StepName string `json:"step_name,omitempty"`
}

// WorkflowRecoveryBlocker supplies fixed explanations without customer values.
func WorkflowRecoveryBlocker(code, step string) WorkflowDiagnosticBlocker {
	messages := map[string]string{
		"run_not_failed":                "Only failed or dead runs can resume.",
		"resume_limit_reached":          "This run has reached its continuation limit.",
		"cancelled":                     "Cancelled runs cannot resume.",
		"invalid_definition":            "The captured definition is invalid for the current plan.",
		"incomplete_step_state":         "Persisted step or batch state is incomplete.",
		"active_step":                   "A step still has an active call or wait.",
		"handler_executed":              "A failure or timeout handler, or its continuation, has already executed.",
		"failed_control_step":           "A failed wait, join or control step cannot be reopened.",
		"failure_before_dispatch":       "The failed action never reached an executor attempt.",
		"unsafe_mutation":               "An attempted external mutation lacks declared provider idempotency support.",
		"no_failed_actions":             "No replayable failed actions were found.",
		"active_attempt":                "An executor attempt is still running.",
		"account_inactive":              "The account is not eligible to execute workflows.",
		"plan_not_allowed":              "The current plan does not include workflows.",
		"app_deleted":                   "The application has been deleted.",
		"maintenance":                   "The application is in maintenance mode.",
		"tenant_required":               "The application now requires a tenant identity that this run does not have.",
		"tenant_unavailable":            "The run's tenant is inactive or no longer linked to the application.",
		"deployment_unavailable":        "No live default deployment is available for resume admission.",
		"pinned_deployment_unavailable": "The run's original pinned deployment is unavailable.",
		"integration_unavailable":       "A captured external action lacks an enabled integration, credential or permitted application binding.",
		"active_run_quota":              "The application's active-run quota is full.",
		"runtime_disabled":              "Workflow execution is disabled on this API server.",
	}
	return WorkflowDiagnosticBlocker{Code: code, StepName: step, Message: messages[code]}
}

func (c *Client) GetWorkflowRunDiagnostics(ctx context.Context, id string) (WorkflowRunDiagnosticsResponse, error) {
	var out WorkflowRunDiagnosticsResponse
	err := c.do(ctx, "GET", "/v1/workflows/runs/"+url.PathEscape(id)+"/diagnostics", nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfWorkflowRunDiagnostics(ctx context.Context, id string) (WorkflowRunDiagnosticsResponse, error) {
	var out WorkflowRunDiagnosticsResponse
	err := c.do(ctx, "GET", "/v1/platform-tenant-self/workflows/runs/"+url.PathEscape(id)+"/diagnostics", nil, &out)
	return out, err
}
