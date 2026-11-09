package api

import (
	"context"
	"net/http"
	"net/url"
)

// StartStoredWorkflowLiveSuiteRequest freezes a server-managed selection. The
// caller supplies a fresh UUIDv7 suite ID and reuses it after a lost response.
type StartStoredWorkflowLiveSuiteRequest struct {
	ExpectedVersion          int64    `json:"expected_version"`
	ExpectedPublishedVersion int64    `json:"expected_published_version"`
	ExpectedDeploymentID     string   `json:"expected_deployment_id"`
	Cases                    []string `json:"cases"`
	ConfirmRealActions       bool     `json:"confirm_real_actions"`
}

type AdvanceStoredWorkflowLiveSuiteRequest struct {
	ConfirmRealActions bool `json:"confirm_real_actions"`
}

func (c *Client) StartStoredWorkflowLiveSuite(ctx context.Context, slug, name, suiteID string, body StartStoredWorkflowLiveSuiteRequest) (StoredWorkflowLiveSuite, error) {
	var out StoredWorkflowLiveSuite
	err := c.do(ctx, http.MethodPost, storedWorkflowLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID)+"/start", body, &out)
	return out, err
}

func (c *Client) AdvanceStoredWorkflowLiveSuite(ctx context.Context, slug, name, suiteID string, body AdvanceStoredWorkflowLiveSuiteRequest) (StoredWorkflowLiveSuite, error) {
	var out StoredWorkflowLiveSuite
	err := c.do(ctx, http.MethodPost, storedWorkflowLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID)+"/advance", body, &out)
	return out, err
}
