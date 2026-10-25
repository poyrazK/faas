package api

import (
	"context"
	"fmt"
)

// ProjectSubmissionReceipt is read-only recovery for one project apply attempt.
type ProjectSubmissionReceipt struct {
	SelectionComplete bool          `json:"selection_complete"`
	AttemptID         string        `json:"attempt_id"`
	ProjectSlug       string        `json:"project_slug"`
	Environment       string        `json:"environment,omitempty"`
	Status            string        `json:"status"`
	Apply             ApplyResponse `json:"apply"`
}

func (c *Client) GetProjectSubmission(ctx context.Context, attemptID string) (ProjectSubmissionReceipt, error) {
	var out ProjectSubmissionReceipt
	if !ValidDeploymentAttemptID(attemptID) {
		return out, fmt.Errorf("invalid project submission attempt ID")
	}
	return out, c.do(ctx, "GET", "/v1/project-submissions/"+attemptID, nil, &out)
}
