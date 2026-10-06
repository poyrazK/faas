package api

import (
	"context"
	"net/url"
)

// RolloutRecoveryReceipt describes the committed exact canary abort. Binding
// reports are present when the restored deployment's release policy enforces
// checks. RestoredTrafficPercent is the weight committed by this operation.
type RolloutRecoveryReceipt struct {
	DeploymentID            string               `json:"deployment_id"`
	PredecessorDeploymentID string               `json:"predecessor_deployment_id"`
	RestoredTrafficPercent  int                  `json:"restored_traffic_percent"`
	BindingsChecks          []BindingCheckReport `json:"bindings_checks,omitempty"`
}

// RecoverCanaryRollout restores an exact retained predecessor without choosing
// a newer deployment if the observed rollout has changed.
func (c *Client) RecoverCanaryRollout(ctx context.Context, slug, deploymentID, predecessorID, reason string) (RolloutTransitionResponse, error) {
	return c.RecoverExactRollout(ctx, slug, deploymentID, predecessorID, reason)
}

// RecoverExactRollout aborts a pinned canary or requests the reverse handoff
// of a pinned service rollout. ServiceRecovery confirms acceptance only.
func (c *Client) RecoverExactRollout(ctx context.Context, slug, deploymentID, predecessorID, reason string) (RolloutTransitionResponse, error) {
	var out RolloutTransitionResponse
	body := RecoverRolloutRequest{Action: "abort", Reason: reason, DeploymentID: deploymentID, ExpectedPredecessorDeploymentID: predecessorID}
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/rollouts/recover", body, &out)
	return out, err
}
