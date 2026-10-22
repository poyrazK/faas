package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// WorkflowInitialFailureOptions selects retained generation-zero failures.
type WorkflowInitialFailureOptions struct {
	Workflow string
	After    string
	Limit    int
}

func (o WorkflowInitialFailureOptions) Validate() error {
	return (WorkflowRecoveryHistoryOptions{Workflow: o.Workflow, After: o.After, Limit: o.Limit}).Validate()
}

type WorkflowInitialFailure struct {
	ID            string     `json:"id"`
	AppID         string     `json:"app_id"`
	RunID         string     `json:"run_id"`
	Workflow      string     `json:"workflow"`
	FailureStatus string     `json:"failure_status"`
	ObservedAt    time.Time  `json:"observed_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}
type WorkflowInitialFailures struct {
	ObservedAt time.Time                `json:"observed_at"`
	Coverage   string                   `json:"coverage"`
	Items      []WorkflowInitialFailure `json:"items"`
	NextAfter  string                   `json:"next_after,omitempty"`
}

// WorkflowInitialRecoveryPreview is a live diagnostic, never execution authorization.
type WorkflowInitialRecoveryPreview struct {
	Failure    WorkflowInitialFailure     `json:"failure"`
	ObservedAt time.Time                  `json:"observed_at"`
	Reason     string                     `json:"reason"`
	Blockers   []string                   `json:"blockers"`
	Target     *WorkflowRunRecoveryTarget `json:"target,omitempty"`
}

func (c *Client) ListWorkflowInitialFailures(ctx context.Context, app string, o WorkflowInitialFailureOptions) (WorkflowInitialFailures, error) {
	var out WorkflowInitialFailures
	if err := o.Validate(); err != nil {
		return out, err
	}
	q := url.Values{}
	if o.Workflow != "" {
		q.Set("workflow", o.Workflow)
	}
	if o.After != "" {
		q.Set("after", o.After)
	}
	if o.Limit != 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/workflow-failures?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) PreviewWorkflowInitialRecovery(ctx context.Context, app, id string) (WorkflowInitialRecoveryPreview, error) {
	var out WorkflowInitialRecoveryPreview
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/workflow-failures/"+url.PathEscape(id)+"/recovery-preview", nil, &out)
	return out, err
}

// WorkflowInitialFailurePolicyReason evaluates filters against a fresh preview.
// It never authorizes execution or reserves cooldown and budget capacity.
func WorkflowInitialFailurePolicyReason(p WorkflowRecoveryPolicy, preview WorkflowInitialRecoveryPreview) string {
	contains := func(values []string, value string) bool {
		for _, v := range values {
			if v == value {
				return true
			}
		}
		return false
	}
	if !p.Enabled {
		return "policy_disabled"
	}
	if p.Mode != "preview" && p.Mode != "automatic_initial" {
		return "initial_failure_requires_preview_or_automatic_initial"
	}
	if !contains(p.Triggers, "initial_failure") {
		return "trigger_excluded"
	}
	if !contains(p.WorkflowNames, preview.Failure.Workflow) {
		return "workflow_excluded"
	}
	if !contains(p.FailureStatuses, preview.Failure.FailureStatus) {
		return "failure_status_excluded"
	}
	if preview.Reason != "eligible" {
		return preview.Reason
	}
	if len(preview.Blockers) != 0 || preview.Target == nil || preview.Target.RunID != preview.Failure.RunID || preview.Target.FailureEvidenceID != preview.Failure.ID || preview.Target.ExpectedResumeCount != 0 || preview.Target.ParentPosition != 0 {
		return "initial_failure_evidence_unavailable"
	}
	return "eligible"
}
