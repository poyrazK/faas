package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// AlertRollback is the durable result of one production alert fire. Binding
// evidence is rechecked on every attempt; the deployment pair never changes.
type AlertRollback struct {
	DeploymentEvidence      *AlertRollbackDeploymentEvidence `json:"deployment_evidence,omitempty"`
	Historical              bool                             `json:"historical,omitempty"`
	RollbackOperationID     string                           `json:"rollback_operation_id,omitempty"`
	RollbackPhase           string                           `json:"rollback_phase,omitempty"`
	RollbackRoutingAuditID  string                           `json:"rollback_routing_audit_id,omitempty"`
	ID                      string                           `json:"id"`
	RuleID                  string                           `json:"rule_id"`
	AccountID               string                           `json:"account_id"`
	AppID                   string                           `json:"app_id"`
	Scope                   string                           `json:"scope"`
	CandidateDeploymentID   string                           `json:"candidate_deployment_id,omitempty"`
	PredecessorDeploymentID string                           `json:"predecessor_deployment_id,omitempty"`
	Status                  string                           `json:"status"`
	Reason                  string                           `json:"reason"`
	Code                    string                           `json:"code,omitempty"`
	Blockers                []BindingCheckFinding            `json:"blockers,omitempty"`
	ObservedValue           float64                          `json:"observed_value"`
	FiredAt                 time.Time                        `json:"fired_at"`
	UpdatedAt               time.Time                        `json:"updated_at"`
	CompletedAt             *time.Time                       `json:"completed_at,omitempty"`
	AuditID                 string                           `json:"audit_id,omitempty"`
	Service                 bool                             `json:"service,omitempty"`
	ServiceRequestID        string                           `json:"service_request_id,omitempty"`
	ServicePhase            string                           `json:"service_phase,omitempty"`
	ServiceRoutingAuditID   string                           `json:"service_routing_audit_id,omitempty"`
}

// AlertRollbackDeploymentEvidence pins a post-release trigger to complete
// telemetry minutes from the selected deployment. Accepted evidence is immutable.
type AlertRollbackDeploymentEvidence struct {
	Version         int        `json:"version"`
	DeploymentID    string     `json:"deployment_id"`
	Metric          string     `json:"metric"`
	Comparison      string     `json:"comparison"`
	Threshold       float64    `json:"threshold"`
	WindowSpec      string     `json:"window_spec"`
	CutoverAt       time.Time  `json:"cutover_at"`
	WindowStart     time.Time  `json:"window_start"`
	WindowEnd       time.Time  `json:"window_end"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	LastSampleAt    *time.Time `json:"last_sample_at,omitempty"`
	Requests        int64      `json:"requests"`
	ServerErrors    int64      `json:"server_errors"`
	MinimumRequests int64      `json:"minimum_requests"`
	ErrorRatePct    float64    `json:"error_rate_pct"`
	Status          string     `json:"status"`
	Code            string     `json:"code,omitempty"`
}

func (c *Client) ListAlertRollbacks(ctx context.Context, slug string) ([]AlertRollback, error) {
	out := []AlertRollback{}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/alert-rollbacks", nil, &out)
	return out, err
}
func (c *Client) GetAlertRollback(ctx context.Context, slug, id string) (AlertRollback, error) {
	var out AlertRollback
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/alert-rollbacks/"+url.PathEscape(id), nil, &out)
	return out, err
}
