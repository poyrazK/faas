package api

import (
	"context"
	"net/url"
)

type ResumeWorkflowRunRequest struct {
	ExpectedResumeCount *int `json:"expected_resume_count"`
}
type WorkflowResumeResponse struct {
	RunID          string   `json:"run_id"`
	ResumeNumber   int      `json:"resume_number"`
	AccountID      string   `json:"account_id"`
	PreviousStatus string   `json:"previous_status"`
	PreviousError  *string  `json:"previous_error,omitempty"`
	ResumedSteps   []string `json:"resumed_steps"`
	CreatedAt      string   `json:"created_at"`
}
type ListWorkflowResumesResponse struct {
	Resumes []WorkflowResumeResponse `json:"resumes"`
}

func (c *Client) ResumeWorkflowRun(ctx context.Context, id string, body ResumeWorkflowRunRequest) (WorkflowRunResponse, error) {
	var out WorkflowRunResponse
	err := c.do(ctx, "POST", "/v1/workflows/runs/"+url.PathEscape(id)+"/resume", body, &out)
	return out, err
}
func (c *Client) ListWorkflowResumes(ctx context.Context, id string) (ListWorkflowResumesResponse, error) {
	var out ListWorkflowResumesResponse
	err := c.do(ctx, "GET", "/v1/workflows/runs/"+url.PathEscape(id)+"/resumes", nil, &out)
	return out, err
}
