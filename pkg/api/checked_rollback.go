package api

import (
	"context"
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
	err := c.doWithClientAndIdempotencyKey(ctx, c.rollbackHTTP(), http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/rollback", request, &out, "")
	return out, err
}
func (c *Client) GetRollbackOperation(ctx context.Context, slug, id string) (RollbackOperation, error) {
	var out RollbackOperation
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/rollbacks/"+url.PathEscape(id), nil, &out)
	return out, err
}
