package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// RollbackOperation describes durable intent and progress, never a reusable grant.
type RollbackOperation struct {
	ID                  string                `json:"id"`
	AppID               string                `json:"app_id"`
	Scope               string                `json:"scope"`
	TargetDeploymentID  string                `json:"target_deployment_id"`
	CurrentDeploymentID string                `json:"current_deployment_id"`
	Status              string                `json:"status"`
	Reason              string                `json:"reason,omitempty"`
	Code                string                `json:"code,omitempty"`
	Blockers            []BindingCheckFinding `json:"blockers,omitempty"`
	Service             bool                  `json:"service"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
	CompletedAt         *time.Time            `json:"completed_at,omitempty"`
	AuditID             string                `json:"audit_id,omitempty"`
}

func (c *Client) CheckedRollback(ctx context.Context, slug string, request RollbackRequest) (DeploymentResponse, error) {
	var out DeploymentResponse
	body, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/apps/"+url.PathEscape(slug)+"/rollback", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	c.addAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", newUUIDv4())
	if id, _ := ctx.Value(rollbackAttemptContextKey{}).(string); id != "" {
		req.Header.Set("Rollback-Attempt-ID", id)
	}
	err = c.doReq(c.http, req, &out)
	return out, err
}
func (c *Client) GetRollbackOperation(ctx context.Context, slug, id string) (RollbackOperation, error) {
	var out RollbackOperation
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/rollbacks/"+url.PathEscape(id), nil, &out)
	return out, err
}

type RollbackRequest struct {
	TargetDeploymentID          *string `json:"target_deployment_id,omitempty"`
	ExpectedCurrentDeploymentID *string `json:"expected_current_deployment_id,omitempty"`
	Reason                      string  `json:"reason,omitempty"`
	AlertRuleID                 *string `json:"alert_rule_id,omitempty"`
}

type rollbackAttemptContextKey struct{}

// ContextWithRollbackAttempt sets the optional durable checked-rollback identity.
func ContextWithRollbackAttempt(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, rollbackAttemptContextKey{}, id)
}
