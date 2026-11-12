package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

const EnvironmentPromotionAttemptHeader = "Environment-Promotion-Attempt-ID"
const EnvironmentPromotionHashHeader = "Environment-Promotion-Request-Hash"

// EnvironmentPromotionReceipt proves admission of one original promotion, not
// promotion completion, handoff completion or current health.
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
		if parsed, err := uuid.Parse(id); err == nil {
			ids[i] = parsed.String()
		}
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
func (c *Client) GetEnvironmentPromotionReceipt(ctx context.Context, project, environment, attempt string) (EnvironmentPromotionReceipt, error) {
	var out EnvironmentPromotionReceipt
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotion-attempts/" + url.PathEscape(attempt)
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgEnvironmentPromotionReceipt reads a company promotion's original admission.
func (c *Client) GetOrgEnvironmentPromotionReceipt(ctx context.Context, organization, project, environment, attempt string) (EnvironmentPromotionReceipt, error) {
	var out EnvironmentPromotionReceipt
	path := "/v1/orgs/" + url.PathEscape(organization) + "/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotion-attempts/" + url.PathEscape(attempt)
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodGet, path, nil, &out)
}
