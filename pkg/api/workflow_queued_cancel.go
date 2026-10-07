package api

import (
	"context"
	"net/url"
)

// WorkflowQueuedRunCancelRequest selects pending workflow runs to inspect or
// cancel. WorkflowName, when set, protects against selecting another workflow.
type WorkflowQueuedRunCancelRequest struct {
	WorkflowName string   `json:"workflow_name,omitempty"`
	RunIDs       []string `json:"run_ids"`
}

// WorkflowQueuedRunCancelOutcome is the result for one selected run. Preview
// outcomes are advisory; the cancel operation rechecks them under the run lock.
type WorkflowQueuedRunCancelOutcome struct {
	RunID        string  `json:"run_id"`
	WorkflowName string  `json:"workflow_name,omitempty"`
	Outcome      string  `json:"outcome"`
	Status       string  `json:"status,omitempty"`
	StartedAt    *string `json:"started_at,omitempty"`
	ScheduledFor *string `json:"scheduled_for,omitempty"`
	CreatedAt    *string `json:"created_at,omitempty"`
	CancelledAt  *string `json:"cancelled_at,omitempty"`
}

// WorkflowQueuedRunCancelResponse contains one classification per selected
// run, in the order supplied by the caller.
type WorkflowQueuedRunCancelResponse struct {
	Outcomes []WorkflowQueuedRunCancelOutcome `json:"outcomes"`
}

// PreviewUnstartedWorkflowRunCancellations classifies selected app-owned runs
// without changing them. Cancellation rechecks eligibility at commit time.
func (c *Client) PreviewUnstartedWorkflowRunCancellations(ctx context.Context, slug string, request WorkflowQueuedRunCancelRequest) (WorkflowQueuedRunCancelResponse, error) {
	var response WorkflowQueuedRunCancelResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/workflows/runs:cancel-preview"
	err := c.do(ctx, "POST", path, request, &response)
	return response, err
}

// CancelUnstartedWorkflowRuns atomically cancels the selected runs that remain
// pending and have never started.
func (c *Client) CancelUnstartedWorkflowRuns(ctx context.Context, slug string, request WorkflowQueuedRunCancelRequest) (WorkflowQueuedRunCancelResponse, error) {
	var response WorkflowQueuedRunCancelResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/workflows/runs:cancel-queued"
	err := c.do(ctx, "POST", path, request, &response)
	return response, err
}
