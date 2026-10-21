package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	CodeDeploymentAttemptAccepted = "deployment_attempt_accepted"
	CodeDeploymentAttemptPending  = "deployment_attempt_pending"
)

const DeploymentAttemptHeader = "Deployment-Attempt-ID"

func ValidDeploymentAttemptID(id string) bool {
	if len(id) != DeploymentAttemptIDBytes {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

type deploymentSubmissionAttemptKey struct{}

// ContextWithDeploymentAttempt adds an opaque, persisted recovery identity to
// deployment submissions. It does not change other requests or retry keys.
func ContextWithDeploymentAttempt(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, deploymentSubmissionAttemptKey{}, id)
}

func deploymentAttemptHeaders(ctx context.Context) http.Header {
	id, _ := ctx.Value(deploymentSubmissionAttemptKey{}).(string)
	if id == "" {
		return nil
	}
	headers := make(http.Header)
	headers.Set(DeploymentAttemptHeader, id)
	return headers
}

func (c *Client) GetDeploymentSubmission(ctx context.Context, slug, attemptID string) (DeploymentSubmissionReceipt, error) {
	var out DeploymentSubmissionReceipt
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+slug+"/deployment-submissions/"+attemptID, nil, &out)
	return out, err
}
