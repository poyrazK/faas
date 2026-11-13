package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const EnvironmentPromotionAttemptHeader = "Environment-Promotion-Attempt-ID"
const EnvironmentPromotionHashHeader = "Environment-Promotion-Request-Hash"

// EnvironmentPromotionReceipt proves admission of one original promotion, not
// workload restoration, handoff completion or current health.
type EnvironmentPromotionReceipt struct {
	AttemptID         string    `json:"attempt_id"`
	RequestHash       string    `json:"request_hash"`
	PromotionID       string    `json:"promotion_id"`
	PromotionHash     string    `json:"promotion_hash"`
	ProjectID         string    `json:"project_id"`
	FromEnvironmentID string    `json:"from_environment_id"`
	EnvironmentID     string    `json:"environment_id"`
	AcceptedAt        time.Time `json:"accepted_at"`
}

// EnvironmentPromotionRequestHash binds the original immutable release and scope.
func EnvironmentPromotionRequestHash(projectID, fromID, environmentID, promotionHash string) string {
	ids := []string{projectID, fromID, environmentID}
	for i, id := range ids {
		ids[i] = environmentRollbackCanonicalID(id)
	}
	body, _ := json.Marshal(append([]string{"environment_promotion"}, append(ids, promotionHash)...))
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type environmentPromotionAttemptKey struct{}
type environmentPromotionAttempt struct{ id, hash string }

// ContextWithEnvironmentPromotionAttempt affects only environment promotion POSTs.
func ContextWithEnvironmentPromotionAttempt(ctx context.Context, id, hash string) context.Context {
	return context.WithValue(ctx, environmentPromotionAttemptKey{}, environmentPromotionAttempt{id, hash})
}
func environmentPromotionHeaders(ctx context.Context) http.Header {
	attempt, ok := ctx.Value(environmentPromotionAttemptKey{}).(environmentPromotionAttempt)
	if !ok {
		return nil
	}
	h := make(http.Header)
	h.Set(EnvironmentPromotionAttemptHeader, attempt.id)
	h.Set(EnvironmentPromotionHashHeader, attempt.hash)
	return h
}

// GetEnvironmentPromotionReceipt never forwards mutation headers or retry keys.
func (c *PreviewClient) GetEnvironmentPromotionReceipt(ctx context.Context, project, environment, attempt string) (EnvironmentPromotionReceipt, error) {
	var out EnvironmentPromotionReceipt
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotion-attempts/" + url.PathEscape(attempt)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

func (c *PreviewClient) GetOrgEnvironmentPromotionReceipt(ctx context.Context, organization, project, environment, attempt string) (EnvironmentPromotionReceipt, error) {
	var out EnvironmentPromotionReceipt
	path := "/v1/orgs/" + url.PathEscape(organization) + "/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotion-attempts/" + url.PathEscape(attempt)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

type PromoteProjectEnvironmentRequest struct {
	FromEnvironment string `json:"from_environment"`
	PromotionToken  string `json:"promotion_token"`
	ApprovalToken   string `json:"approval_token,omitempty"`
	OverrideFreeze  string `json:"override_freeze,omitempty"`
}

func (c *PreviewClient) promoteEnvironment(ctx context.Context, path string, body PromoteProjectEnvironmentRequest) (json.RawMessage, error) {
	var out json.RawMessage
	payload, err := json.Marshal(body)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	c.addAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", newUUIDv4())
	for key, values := range environmentPromotionHeaders(ctx) {
		req.Header[key] = values
	}
	return out, c.doReq(c.http, req, &out)
}
func (c *PreviewClient) PromoteProjectEnvironment(ctx context.Context, project, environment string, body PromoteProjectEnvironmentRequest) (json.RawMessage, error) {
	return c.promoteEnvironment(ctx, "/v1/projects/"+url.PathEscape(project)+"/environments/"+url.PathEscape(environment)+"/promote", body)
}
func (c *PreviewClient) PromoteOrgProjectEnvironment(ctx context.Context, org, project, environment string, body PromoteProjectEnvironmentRequest) (json.RawMessage, error) {
	return c.promoteEnvironment(ctx, "/v1/orgs/"+url.PathEscape(org)+"/projects/"+url.PathEscape(project)+"/environments/"+url.PathEscape(environment)+"/promote", body)
}
