package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"time"
)

const DeploymentPromotionAttemptHeader = "Deployment-Promotion-Attempt-ID"

// DeploymentPromotionReceipt proves historical guarded traffic acceptance only.
// Current traffic and health must be read independently.
type DeploymentPromotionReceipt struct {
	AttemptID                   string    `json:"attempt_id"`
	RequestHash                 string    `json:"request_hash"`
	AppID                       string    `json:"app_id"`
	DeploymentID                string    `json:"deployment_id"`
	ExpectedServingDeploymentID string    `json:"expected_serving_deployment_id"`
	Scope                       string    `json:"scope"`
	ProjectID                   string    `json:"project_id,omitempty"`
	EnvironmentID               string    `json:"environment_id,omitempty"`
	AcceptedAt                  time.Time `json:"accepted_at"`
}

// DeploymentPromotionRequestHash binds the canonical candidate/serving identities and exact remaining body.
func DeploymentPromotionRequestHash(id string, request UpdateDeploymentTrafficRequest) (string, error) {
	// Traffic guards accept compact and dashed UUIDs as the same identity.
	if parsed, err := uuid.Parse(id); err == nil {
		id = parsed.String()
	}
	if request.ExpectedServingDeploymentID != nil {
		if parsed, err := uuid.Parse(*request.ExpectedServingDeploymentID); err == nil {
			canonical := parsed.String()
			request.ExpectedServingDeploymentID = &canonical
		}
	}

	body, err := json.Marshal(struct {
		DeploymentID string
		Request      UpdateDeploymentTrafficRequest
	}{id, request})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

type deploymentPromotionAttemptKey struct{}

// ContextWithDeploymentPromotionAttempt affects only guarded traffic PATCHes.
func ContextWithDeploymentPromotionAttempt(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, deploymentPromotionAttemptKey{}, id)
}
func deploymentPromotionHeaders(ctx context.Context) http.Header {
	id, _ := ctx.Value(deploymentPromotionAttemptKey{}).(string)
	if id == "" {
		return nil
	}
	h := make(http.Header)
	h.Set(DeploymentPromotionAttemptHeader, id)
	return h
}

// GetDeploymentPromotionReceipt never retries a promotion or forwards write keys.
func (c *Client) GetDeploymentPromotionReceipt(ctx context.Context, slug, attemptID string) (DeploymentPromotionReceipt, error) {
	var out DeploymentPromotionReceipt
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/deployment-promotions/"+url.PathEscape(attemptID), nil, &out)
}
