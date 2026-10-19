package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

const (
	MetricMCPOAuthReauthorization    = "mcp_oauth_reauthorization_required"
	MetricMCPOAuthRenewalFailed      = "mcp_oauth_renewal_failed"
	MetricMCPOAuthRetirementExpiring = "mcp_oauth_retirement_expiring"
	MetricMCPOAuthIssuanceUnknown    = "mcp_oauth_issuance_unknown"
)

func MCPConnectionHealthKind(metric string) string {
	switch metric {
	case MetricMCPOAuthReauthorization:
		return "reauthorization_required"
	case MetricMCPOAuthRenewalFailed:
		return "renewal_failed"
	case MetricMCPOAuthRetirementExpiring:
		return "retirement_expiring"
	case MetricMCPOAuthIssuanceUnknown:
		return "issuance_unknown"
	}
	return ""
}
func IsMCPConnectionHealthMetric(metric string) bool { return MCPConnectionHealthKind(metric) != "" }

type MCPConnectionHealthIssue struct {
	Scope          string     `json:"scope,omitempty"`
	ID             string     `json:"id"`
	AppID          string     `json:"app_id"`
	AppSlug        string     `json:"app_slug"`
	Role           string     `json:"role"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	AppDeleted     bool       `json:"app_deleted,omitempty"`
	RetirementID   string     `json:"retirement_id,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RecoveryAction string     `json:"recovery_action"`
	RecoveryPath   string     `json:"recovery_path"`
}
type MCPConnectionHealthSnapshot struct {
	ObservedAt   time.Time                  `json:"observed_at"`
	Complete     bool                       `json:"complete"`
	Counts       map[string]int             `json:"counts"`
	TotalIssues  int                        `json:"total_issues"`
	Items        []MCPConnectionHealthIssue `json:"items"`
	NextOffset   int                        `json:"next_offset,omitempty"`
	Fingerprints map[string]string          `json:"-"`
}

func (c *Client) GetMCPConnectionHealth(ctx context.Context, app string, offset int) (MCPConnectionHealthSnapshot, error) {
	var out MCPConnectionHealthSnapshot
	query := url.Values{"offset": {strconv.Itoa(offset)}}
	if app != "" {
		query.Set("app", app)
	}
	err := c.do(ctx, "GET", "/v1/mcp-connection-health?"+query.Encode(), nil, &out)
	return out, err
}
