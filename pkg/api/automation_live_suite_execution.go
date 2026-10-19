package api

import (
	"context"
	"net/http"
	"net/url"
)

// StartAutomationLiveSuiteRequest freezes a server-managed selection. The
// caller supplies a fresh UUIDv7 suite ID and reuses it after a lost response.
type StartAutomationLiveSuiteRequest struct {
	ExpectedVersion          int64    `json:"expected_version"`
	ExpectedPublishedVersion int64    `json:"expected_published_version"`
	ExpectedDeploymentID     string   `json:"expected_deployment_id"`
	Cases                    []string `json:"cases"`
	ConfirmRealActions       bool     `json:"confirm_real_actions"`
}

type AdvanceAutomationLiveSuiteRequest struct {
	ConfirmRealActions bool `json:"confirm_real_actions"`
}

func (c *Client) StartAutomationLiveSuite(ctx context.Context, slug, name, suiteID string, body StartAutomationLiveSuiteRequest) (AutomationLiveSuite, error) {
	var out AutomationLiveSuite
	err := c.do(ctx, http.MethodPost, automationLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID)+"/start", body, &out)
	return out, err
}

func (c *Client) AdvanceAutomationLiveSuite(ctx context.Context, slug, name, suiteID string, body AdvanceAutomationLiveSuiteRequest) (AutomationLiveSuite, error) {
	var out AutomationLiveSuite
	err := c.do(ctx, http.MethodPost, automationLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID)+"/advance", body, &out)
	return out, err
}
