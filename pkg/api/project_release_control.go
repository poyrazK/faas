package api

import (
	"context"
	"fmt"
	"time"
)

// ProjectReleaseControlRequest fences a decision against the inspected checkpoint.
type ProjectReleaseControlRequest struct {
	RequestID          string `json:"request_id"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

type ProjectReleaseControl struct {
	Action             string    `json:"action"`
	RequestID          string    `json:"request_id"`
	ExpectedGeneration uint64    `json:"expected_generation"`
	RequestedAt        time.Time `json:"requested_at"`
	Status             string    `json:"status"`
}

func ProjectReleaseControlActions(safe *ProjectSafeRelease) []string {
	if safe == nil || safe.Verification == nil || !safe.Verification.Progressive || safe.Verification.Generation == 0 {
		return nil
	}
	if c := safe.Verification.Control; c != nil && c.Status == "pending" {
		return nil
	}
	switch safe.Status {
	case "succeeded":
		if ProjectReleaseMonitoring(safe) {
			return []string{"abort"}
		}
		return nil
	case "verifying":
		return []string{"abort"}
	case "rollback_failed":
		return []string{"recover"}
	default:
		return nil
	}
}

func (c *Client) ControlProjectRelease(ctx context.Context, attemptID, action string, request ProjectReleaseControlRequest) (ProjectSubmissionReceipt, error) {
	var out ProjectSubmissionReceipt
	if !ValidDeploymentAttemptID(attemptID) || !ValidDeploymentAttemptID(request.RequestID) || request.ExpectedGeneration == 0 || (action != "abort" && action != "recover" && action != "contract") {
		return out, fmt.Errorf("invalid progressive deploy control request")
	}
	return out, c.do(ctx, "POST", "/v1/project-submissions/"+attemptID+"/"+action, request, &out)
}
