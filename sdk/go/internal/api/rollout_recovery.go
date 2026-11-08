package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

// Recovery requests and receipts are shared with the public API schema.
type RecoverRolloutRequest struct {
	ExpectedReview                  *AppReleaseReview `json:"expected_review,omitempty"`
	Action                          string            `json:"action"`
	Reason                          string            `json:"reason,omitempty"`
	DeploymentID                    string            `json:"deployment_id,omitempty"`
	ExpectedPredecessorDeploymentID string            `json:"expected_predecessor_deployment_id,omitempty"`
}
type RolloutTransitionResponse struct {
	Deployment      DeploymentResponse             `json:"deployment"`
	AuditID         string                         `json:"audit_id"`
	Recovery        *RolloutRecoveryReceipt        `json:"recovery,omitempty"`
	ServiceRecovery *ServiceRolloutRecoveryReceipt `json:"service_recovery,omitempty"`
}

// RecoverReviewedServiceRollout pins configuration as well as both generations.
func (c *Client) RecoverReviewedServiceRollout(ctx context.Context, slug, deploymentID, predecessorID, reason string, review AppReleaseReview) (RolloutTransitionResponse, error) {
	var out RolloutTransitionResponse
	body := RecoverRolloutRequest{Action: "abort", Reason: reason, DeploymentID: deploymentID, ExpectedPredecessorDeploymentID: predecessorID, ExpectedReview: &review}
	payload, err := json.Marshal(body)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/apps/"+url.PathEscape(slug)+"/rollouts/recover", bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	c.addAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", newUUIDv4())
	for key, values := range serviceAbortAttemptHeaders(ctx) {
		req.Header[key] = values
	}
	err = c.doReq(c.http, req, &out)
	return out, err
}

type AppReleaseReview struct {
	Scope                string   `json:"scope,omitempty"`
	ProjectID            string   `json:"project_id,omitempty"`
	EnvironmentID        string   `json:"environment_id,omitempty"`
	WorkloadRevision     int64    `json:"workload_revision,omitempty"`
	WorkloadConfigHash   string   `json:"workload_config_hash,omitempty"`
	Protected            bool     `json:"protected,omitempty"`
	AppID                string   `json:"app_id"`
	ConfigurationHash    string   `json:"configuration_hash"`
	LatestDeploymentID   string   `json:"latest_deployment_id"`
	ServingDeploymentIDs []string `json:"serving_deployment_ids"`
}

func (c *Client) GetAppReleaseReview(ctx context.Context, slug string) (AppReleaseReview, error) {
	var out AppReleaseReview
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/release-review", nil, &out)
	return out, err
}
