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

const EnvironmentRollbackAttemptHeader = "Environment-Rollback-Attempt-ID"
const EnvironmentRollbackHashHeader = "Environment-Rollback-Request-Hash"

// EnvironmentRollbackReceipt proves admission of one original rollback, not
// workload restoration, handoff completion or current health.
type EnvironmentRollbackReceipt struct {
	AttemptID         string    `json:"attempt_id"`
	RequestHash       string    `json:"request_hash"`
	PromotionID       string    `json:"promotion_id"`
	PromotionHash     string    `json:"promotion_hash"`
	ProjectID         string    `json:"project_id"`
	FromEnvironmentID string    `json:"from_environment_id"`
	EnvironmentID     string    `json:"environment_id"`
	AcceptedAt        time.Time `json:"accepted_at"`
}

// EnvironmentRollbackRequestHash binds the original immutable release and scope.
func EnvironmentRollbackRequestHash(projectID, fromID, environmentID, promotionID, promotionHash string) string {
	ids := []string{projectID, fromID, environmentID, promotionID}
	for i, id := range ids {
		if parsed, err := uuid.Parse(id); err == nil {
			ids[i] = parsed.String()
		}
	}
	body, _ := json.Marshal(append([]string{"environment_rollback"}, append(ids, promotionHash)...))
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type environmentRollbackAttemptKey struct{}
type environmentRollbackAttempt struct{ id, hash string }

// ContextWithEnvironmentRollbackAttempt affects only environment rollback POSTs.
func ContextWithEnvironmentRollbackAttempt(ctx context.Context, id, hash string) context.Context {
	return context.WithValue(ctx, environmentRollbackAttemptKey{}, environmentRollbackAttempt{id, hash})
}
func environmentRollbackHeaders(ctx context.Context) http.Header {
	attempt, ok := ctx.Value(environmentRollbackAttemptKey{}).(environmentRollbackAttempt)
	if !ok {
		return nil
	}
	h := make(http.Header)
	h.Set(EnvironmentRollbackAttemptHeader, attempt.id)
	h.Set(EnvironmentRollbackHashHeader, attempt.hash)
	return h
}

// GetEnvironmentRollbackReceipt never forwards mutation headers or retry keys.
func (c *Client) GetEnvironmentRollbackReceipt(ctx context.Context, project, environment, promotion, attempt string) (EnvironmentRollbackReceipt, error) {
	var out EnvironmentRollbackReceipt
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/rollback-attempts/" + url.PathEscape(attempt)
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgEnvironmentRollbackReceipt reads a company rollback's original admission.
func (c *Client) GetOrgEnvironmentRollbackReceipt(ctx context.Context, organization, project, environment, promotion, attempt string) (EnvironmentRollbackReceipt, error) {
	var out EnvironmentRollbackReceipt
	path := "/v1/orgs/" + url.PathEscape(organization) + "/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/rollback-attempts/" + url.PathEscape(attempt)
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodGet, path, nil, &out)
}
