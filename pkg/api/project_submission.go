package api

import (
	"context"
	"fmt"
	"time"
)

// ProjectSubmissionRetention is how long a settled project apply receipt
// stays readable. It exceeds the seven-day client session retention, so a
// saved session can always recover its outcome (ADR-20261021094500).
const ProjectSubmissionRetention = 30 * 24 * time.Hour

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
