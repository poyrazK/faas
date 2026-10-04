package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
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

// ProbeSafeRelease verifies that both operation credentials reach APID's
// loopback listener. The shared deadline keeps lease renewal bounded when
// the listener hangs; neither request mutates deployment state.
func (c *InternalSafeDeployClient) ProbeSafeRelease(ctx context.Context) error {
	if c == nil || c.canary == nil || c.action == nil {
		return errors.New("safe deploy operator client not configured")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := probeSafeReleaseRoute(probeCtx, c.canary, "/v1/internal/safe-deploy/canary/readyz"); err != nil {
		return fmt.Errorf("canary operator probe: %w", err)
	}
	if err := probeSafeReleaseRoute(probeCtx, c.action, "/v1/internal/safe-deploy/action/readyz"); err != nil {
		return fmt.Errorf("recovery operator probe: %w", err)
	}
	return nil
}

func probeSafeReleaseRoute(ctx context.Context, c *Client, path string) error {
	if c.http == nil {
		return errors.New("operator HTTP client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("expected HTTP 204, got %d", resp.StatusCode)
	}
	return nil
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

// RecoverCanaryRouteHealth requests a fresh APID evaluation before stage timers
// and aggregate gates. It never sends historical evidence as an authorization.
func (c *InternalSafeDeployClient) RecoverCanaryRouteHealth(ctx context.Context, id string, expectedStep int) (CanaryRouteHealthRecoveryResponse, error) {
	var out CanaryRouteHealthRecoveryResponse
	err := c.action.do(ctx, http.MethodPost,
		"/v1/internal/safe-deploy/deployments/"+url.PathEscape(id)+"/route-health/recover",
		CanaryRouteHealthRecoveryRequest{ExpectedStep: expectedStep}, &out)
	return out, err
}
