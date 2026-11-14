package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
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
		ids[i] = environmentRollbackCanonicalID(id)
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
func (c *PreviewClient) GetEnvironmentRollbackReceipt(ctx context.Context, project, environment, promotion, attempt string) (EnvironmentRollbackReceipt, error) {
	var out EnvironmentRollbackReceipt
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/rollback-attempts/" + url.PathEscape(attempt)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// RollbackProjectEnvironmentPromotion returns native status as JSON; acceptance
// must be verified independently through the exact original receipt.
func (c *PreviewClient) RollbackProjectEnvironmentPromotion(ctx context.Context, project, environment, promotion string) (json.RawMessage, error) {
	var out json.RawMessage
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/rollback"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return out, err
	}
	c.addAuthHeader(req)
	req.Header.Set("Idempotency-Key", newUUIDv4())
	for key, values := range environmentRollbackHeaders(ctx) {
		req.Header[key] = values
	}
	return out, c.doReq(c.http, req, &out)
}

func (c *PreviewClient) GetOrgEnvironmentRollbackReceipt(ctx context.Context, organization, project, environment, promotion, attempt string) (EnvironmentRollbackReceipt, error) {
	var out EnvironmentRollbackReceipt
	path := "/v1/orgs/" + url.PathEscape(organization) + "/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/rollback-attempts/" + url.PathEscape(attempt)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

func environmentRollbackCanonicalID(id string) string {
	value := id
	if len(value) == 45 && strings.EqualFold(value[:9], "urn:uuid:") {
		value = value[9:]
	}
	if len(value) == 38 && value[0] == '{' && value[37] == '}' {
		value = value[1:37]
	}
	if len(value) == 36 {
		if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
			return id
		}
		value = strings.ReplaceAll(value, "-", "")
	}
	if len(value) != 32 {
		return id
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return id
	}
	value = hex.EncodeToString(raw)
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
}
