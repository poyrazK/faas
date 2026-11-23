package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
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
	ProjectRollbackID   string                `json:"project_rollback_id,omitempty"`
	// Plain rollbacks (ADR-1446) also report what started them
	// (manual, auto_5xx, alert), the measured time from request to serving,
	// and a failure detail. Checked rollbacks leave these empty.
	Trigger    string `json:"trigger,omitempty"`
	RecoveryMS *int64 `json:"recovery_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (c *Client) CheckedRollback(ctx context.Context, slug string, request RollbackRequest) (DeploymentResponse, error) {
	var out DeploymentResponse
	err := c.doWithClientAndHeadersAndIdempotencyKey(ctx, c.rollbackHTTP(), http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/rollback", request, &out, "", rollbackAttemptHeaders(ctx))
	return out, err
}
func (c *Client) GetRollbackOperation(ctx context.Context, slug, id string) (RollbackOperation, error) {
	var out RollbackOperation
	err := c.doWithoutIdempotencyKey(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/rollbacks/"+url.PathEscape(id), nil, &out)
	return out, err
}

// PreflightCheckedRollback reads admission checks for an exact rollback pair.
func (c *Client) PreflightCheckedRollback(ctx context.Context, slug, current, target string, ack bool) ([]BindingCheckFinding, error) {
	query := url.Values{
		"expected_current_deployment_id": {current},
		"target_deployment_id":           {target},
		"acknowledge_releases":           {strconv.FormatBool(ack)},
	}
	var warnings []BindingCheckFinding
	if err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/rollbacks/preflight?"+query.Encode(), nil, &warnings); err != nil {
		return nil, err
	}
	if warnings == nil {
		return nil, errors.New("server returned no rollback preflight result")
	}
	return warnings, nil
}
