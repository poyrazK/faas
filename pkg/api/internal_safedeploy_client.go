package api

import (
	"context"
	"net/http"
	"net/url"
)

// InternalSafeDeployClient is meterd's narrow, loopback-only APID client.
// Its two credentials authorize distinct operations on APID's operator
// listener; neither credential is a customer account API key. Do not use this
// client with the public API origin.
type InternalSafeDeployClient struct {
	canary *Client
	action *Client
}

func NewInternalSafeDeployClient(baseURL, canaryToken, actionToken string) *InternalSafeDeployClient {
	canary := NewClient(baseURL, canaryToken)
	action := NewClient(baseURL, actionToken)
	canary.SetCompletionCache(nil)
	action.SetCompletionCache(nil)
	return &InternalSafeDeployClient{canary: canary, action: action}
}

func (c *InternalSafeDeployClient) AdvanceCanary(ctx context.Context, id string, expectedStep int) (CanaryAdvanceResponse, error) {
	var out CanaryAdvanceResponse
	err := c.canary.do(ctx, http.MethodPost,
		"/v1/internal/safe-deploy/deployments/"+url.PathEscape(id)+"/canary/advance",
		AdvanceCanaryRequest{ExpectedStep: expectedStep}, &out)
	return out, err
}

func (c *InternalSafeDeployClient) RecoverRollout(ctx context.Context, slug, action, reason string) (RolloutTransitionResponse, error) {
	return c.RecoverRolloutAndIdempotencyKey(ctx, slug, action, reason, "")
}

func (c *InternalSafeDeployClient) RecoverRolloutAndIdempotencyKey(ctx context.Context, slug, action, reason, key string) (RolloutTransitionResponse, error) {
	var out RolloutTransitionResponse
	err := c.action.doWithIdempotencyKey(ctx, http.MethodPost,
		"/v1/internal/safe-deploy/apps/"+url.PathEscape(slug)+"/rollouts/recover",
		RecoverRolloutRequest{Action: action, Reason: reason}, &out, key)
	return out, err
}

func (c *InternalSafeDeployClient) RecoverDeploymentRolloutAndIdempotencyKey(ctx context.Context, deploymentID, predecessorDeploymentID, action, reason, key string) (RolloutTransitionResponse, error) {
	var out RolloutTransitionResponse
	err := c.action.doWithIdempotencyKey(ctx, http.MethodPost,
		"/v1/internal/safe-deploy/deployments/"+url.PathEscape(deploymentID)+"/rollouts/recover",
		RecoverDeploymentRolloutRequest{
			Action:                          action,
			Reason:                          reason,
			ExpectedPredecessorDeploymentID: predecessorDeploymentID,
		}, &out, key)
	return out, err
}

func (c *InternalSafeDeployClient) RollbackTo(ctx context.Context, slug, targetDeploymentID string) (DeploymentResponse, error) {
	return c.RollbackToWithRuleAndIdempotencyKey(ctx, slug, targetDeploymentID, "", "")
}

func (c *InternalSafeDeployClient) RollbackToWithRule(ctx context.Context, slug, targetDeploymentID, alertRuleID string) (DeploymentResponse, error) {
	return c.RollbackToWithRuleAndIdempotencyKey(ctx, slug, targetDeploymentID, alertRuleID, "")
}

func (c *InternalSafeDeployClient) RollbackToWithRuleAndIdempotencyKey(ctx context.Context, slug, targetDeploymentID, alertRuleID, key string) (DeploymentResponse, error) {
	var out DeploymentResponse
	body := RollbackRequest{TargetDeploymentID: &targetDeploymentID}
	if alertRuleID != "" {
		body.AlertRuleID = &alertRuleID
	}
	err := c.action.doWithClientAndIdempotencyKey(ctx, c.action.rollbackHTTP(), http.MethodPost,
		"/v1/internal/safe-deploy/apps/"+url.PathEscape(slug)+"/rollback",
		body, &out, key)
	return out, err
}
