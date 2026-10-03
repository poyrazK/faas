package api

import (
	"context"
	"net/url"
)

const (
	ExecutionArtifactGrantDefaultTTLSeconds = 300
	ExecutionArtifactGrantMinTTLSeconds     = 30
	ExecutionArtifactGrantMaxTTLSeconds     = 3600
)

type CreateExecutionArtifactGrantRequest struct {
	ArtifactName     string `json:"artifact_name"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
}

// ExecutionArtifactGrantResponse returns the bearer capability once at
// creation. The service stores only its SHA-256 hash.
type ExecutionArtifactGrantResponse struct {
	ID                string `json:"id"`
	SourceExecutionID string `json:"source_execution_id"`
	ArtifactName      string `json:"artifact_name"`
	Token             string `json:"token"`
	ExpiresAt         string `json:"expires_at"`
}

type RevokeExecutionArtifactGrantResponse struct {
	ID        string `json:"id"`
	RevokedAt string `json:"revoked_at"`
}

func (c *Client) CreateExecutionArtifactGrant(ctx context.Context, executionID string, req CreateExecutionArtifactGrantRequest) (ExecutionArtifactGrantResponse, error) {
	var out ExecutionArtifactGrantResponse
	path := "/v1/executions/" + url.PathEscape(executionID) + "/artifact-grants"
	return out, c.do(ctx, "POST", path, req, &out)
}

func (c *Client) RevokeExecutionArtifactGrant(ctx context.Context, grantID string) (RevokeExecutionArtifactGrantResponse, error) {
	var out RevokeExecutionArtifactGrantResponse
	path := "/v1/execution-artifact-grants/" + url.PathEscape(grantID)
	return out, c.do(ctx, "DELETE", path, nil, &out)
}
