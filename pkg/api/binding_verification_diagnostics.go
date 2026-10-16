// adr:1359 — public observations and recovery guidance do not grant admission.
package api

import (
	"context"
	"net/http"
	"time"
)

type BindingVerificationDiagnosticBlocker struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Recovery     string `json:"recovery"`
	App          string `json:"app,omitempty"`
	Type         string `json:"type,omitempty"`
	Name         string `json:"name,omitempty"`
	Binding      string `json:"binding,omitempty"`
	Scope        string `json:"scope,omitempty"`
	StepIndex    *int   `json:"step_index,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	ResourceKind string `json:"resource_kind,omitempty"`
	Status       string `json:"status,omitempty"`
}

// Values come from a fixed public metadata whitelist, never arbitrary object
// serialization, credentials, provider origins, commands or guest output.
type BindingVerificationDifference struct {
	App      string `json:"app,omitempty"`
	Type     string `json:"type,omitempty"`
	Name     string `json:"name,omitempty"`
	Binding  string `json:"binding,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Field    string `json:"field"`
	Reviewed string `json:"reviewed"`
	Current  string `json:"current"`
}
type BindingVerificationRecoveryAction struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}
type BindingVerificationBudget struct {
	RunID           string `json:"run_id"`
	MaxConcurrency  int    `json:"max_concurrency"`
	ActiveResources int    `json:"active_resources"`
	WaitingSteps    int    `json:"waiting_steps"`
	Full            bool   `json:"full"`
}

type BindingVerificationRunDiagnostics struct {
	Budget       *BindingVerificationBudget `json:"budget,omitempty"`
	RunID        string                     `json:"run_id"`
	GraphRunID   string                     `json:"graph_run_id,omitempty"`
	App          string                     `json:"app"`
	DeploymentID string                     `json:"deployment_id"`
	Scope        string                     `json:"scope"`
	Status       string                     `json:"status"`
	CheckedAt    time.Time                  `json:"checked_at"`
	ExpiresAt    time.Time                  `json:"expires_at"`
	// Complete means all diagnostic reads succeeded, not that admission is allowed.
	Complete    bool                                   `json:"complete"`
	CheckPassed *bool                                  `json:"check_passed,omitempty"`
	Blockers    []BindingVerificationDiagnosticBlocker `json:"blockers"`
	Changes     []BindingVerificationDifference        `json:"changes"`
	Recovery    []BindingVerificationRecoveryAction    `json:"recovery"`
}
type ProjectBindingVerificationRunDiagnostics struct {
	Budget      *BindingVerificationBudget                   `json:"budget,omitempty"`
	RunID       string                                       `json:"run_id"`
	Project     string                                       `json:"project"`
	Environment string                                       `json:"environment"`
	Status      string                                       `json:"status"`
	CheckedAt   time.Time                                    `json:"checked_at"`
	ExpiresAt   time.Time                                    `json:"expires_at"`
	Complete    bool                                         `json:"complete"`
	Members     map[string]BindingVerificationRunDiagnostics `json:"members"`
	Blockers    []BindingVerificationDiagnosticBlocker       `json:"blockers"`
	Changes     []BindingVerificationDifference              `json:"changes"`
	Recovery    []BindingVerificationRecoveryAction          `json:"recovery"`
}

func (c *Client) GetBindingVerificationRunDiagnostics(ctx context.Context, app, id string) (BindingVerificationRunDiagnostics, error) {
	var out BindingVerificationRunDiagnostics
	err := c.do(ctx, http.MethodGet, bindingRunPath(app, id)+"/diagnostics", nil, &out)
	return out, err
}
func (c *Client) GetProjectBindingVerificationRunDiagnostics(ctx context.Context, project, environment, id string) (ProjectBindingVerificationRunDiagnostics, error) {
	var out ProjectBindingVerificationRunDiagnostics
	err := c.do(ctx, http.MethodGet, projectBindingRunPath(project, environment, id)+"/diagnostics", nil, &out)
	return out, err
}
