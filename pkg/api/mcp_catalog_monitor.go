package api

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"time"
)

const (
	MetricMCPCatalogDrift              = "mcp_catalog_drift"
	MetricMCPCatalogMonitorUnavailable = "mcp_catalog_monitor_unavailable"
)

func IsMCPCatalogMonitorMetric(metric string) bool {
	return metric == MetricMCPCatalogDrift || metric == MetricMCPCatalogMonitorUnavailable
}

type MCPCatalogMonitorConfigRequest struct {
	Scope            string                    `json:"scope,omitempty"`
	Observer         *MCPCatalogObserverConfig `json:"observer,omitempty"`
	TokenEnv         string                    `json:"token_env,omitempty"`
	Role             string                    `json:"role,omitempty"`
	Enabled          bool                      `json:"enabled"`
	Baseline         json.RawMessage           `json:"baseline"`
	FreshnessSeconds int                       `json:"freshness_seconds"`
}
type MCPCatalogMonitorConfig struct {
	Scope            string                    `json:"scope,omitempty"`
	Observer         *MCPCatalogObserverConfig `json:"observer,omitempty"`
	TokenEnv         string                    `json:"token_env,omitempty"`
	Role             string                    `json:"role,omitempty"`
	Enabled          bool                      `json:"enabled"`
	Revision         string                    `json:"revision"`
	Baseline         json.RawMessage           `json:"baseline"`
	BaselineSHA256   string                    `json:"baseline_sha256"`
	FreshnessSeconds int                       `json:"freshness_seconds"`
}
type MCPCatalogObservationRequest struct {
	Role        string          `json:"role,omitempty"`
	Revision    string          `json:"revision"`
	Observed    json.RawMessage `json:"observed,omitempty"`
	Unavailable bool            `json:"unavailable"`
}
type MCPCatalogReceipt struct {
	Role           string          `json:"role,omitempty"`
	ID             string          `json:"id"`
	Revision       string          `json:"revision"`
	CapturedAt     time.Time       `json:"captured_at"`
	State          string          `json:"state"`
	BaselineSHA256 string          `json:"baseline_sha256"`
	ObservedSHA256 string          `json:"observed_sha256,omitempty"`
	ChangedCount   int             `json:"changed_count"`
	Diff           json.RawMessage `json:"diff,omitempty"`
	Truncated      bool            `json:"truncated"`
}
type MCPCatalogMonitorSnapshot struct {
	OAuthLifecycle *MCPCatalogOAuthLifecycle            `json:"oauth_lifecycle,omitempty"`
	ObserverStatus *MCPCatalogObserverStatus            `json:"observer_status,omitempty"`
	Roles          map[string]MCPCatalogMonitorSnapshot `json:"roles,omitempty"`
	Config         MCPCatalogMonitorConfig              `json:"config"`
	CheckedAt      *time.Time                           `json:"checked_at,omitempty"`
	Latest         *MCPCatalogReceipt                   `json:"latest,omitempty"`
	History        []MCPCatalogReceipt                  `json:"history"`
	State          string                               `json:"state"`
}
type MCPCatalogMonitorSignal struct {
	Role             string                             `json:"role,omitempty"`
	Roles            map[string]MCPCatalogMonitorSignal `json:"roles,omitempty"`
	UnavailableRoles []string                           `json:"unavailable_roles,omitempty"`
	DriftedRoles     []string                           `json:"drifted_roles,omitempty"`
	State            string                             `json:"state"`
	CheckedAt        *time.Time                         `json:"checked_at,omitempty"`
	ReceiptID        string                             `json:"receipt_id,omitempty"`
	ChangedCount     int                                `json:"changed_count"`
	EvidencePath     string                             `json:"evidence_path,omitempty"`
}

func CatalogMonitorSignal(snapshot MCPCatalogMonitorSnapshot, slug string, at time.Time) MCPCatalogMonitorSignal {
	result := MCPCatalogMonitorSignal{State: "disabled", Role: CatalogMonitorRole(snapshot.Config.Role), CheckedAt: snapshot.CheckedAt}
	if !snapshot.Config.Enabled {
		return result
	}
	result.State = "unavailable"
	if snapshot.Latest != nil {
		result.ReceiptID = snapshot.Latest.ID
		result.ChangedCount = snapshot.Latest.ChangedCount
		result.EvidencePath = "/v1/apps/" + url.PathEscape(slug) + "/mcp-catalog-monitor?evidence=" + url.QueryEscape(result.ReceiptID) + "&role=" + url.QueryEscape(result.Role)
	}
	if snapshot.CheckedAt == nil || snapshot.Latest == nil || snapshot.Latest.Revision != snapshot.Config.Revision || snapshot.CheckedAt.After(at) || at.Sub(*snapshot.CheckedAt) > time.Duration(snapshot.Config.FreshnessSeconds)*time.Second {
		return result
	}
	if snapshot.Latest.State == "healthy" || snapshot.Latest.State == "drift" {
		result.State = snapshot.Latest.State
	}
	return result
}
func (c *Client) ConfigureMCPCatalogMonitor(ctx context.Context, slug string, req MCPCatalogMonitorConfigRequest) (MCPCatalogMonitorSnapshot, error) {
	var out MCPCatalogMonitorSnapshot
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/mcp-catalog-monitor", req, &out)
	return out, err
}
func (c *Client) GetMCPCatalogMonitor(ctx context.Context, slug string) (MCPCatalogMonitorSnapshot, error) {
	var out MCPCatalogMonitorSnapshot
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-catalog-monitor", nil, &out)
	return out, err
}
func (c *Client) ReportMCPCatalogObservation(ctx context.Context, slug string, req MCPCatalogObservationRequest) (MCPCatalogReceipt, error) {
	var out MCPCatalogReceipt
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/mcp-catalog-monitor/observations", req, &out)
	return out, err
}

// Empty roles preserve the original single-monitor API as the default role.
func CatalogMonitorRole(role string) string {
	if role == "" {
		return "default"
	}
	return role
}

var catalogRolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var catalogTokenEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func ValidCatalogMonitorRole(role string) bool {
	return catalogRolePattern.MatchString(CatalogMonitorRole(role))
}
func ValidCatalogMonitorTokenEnv(name string) bool {
	return name == "" || catalogTokenEnvPattern.MatchString(name)
}
func CatalogMonitorRoleSnapshot(snapshot MCPCatalogMonitorSnapshot, role string) (MCPCatalogMonitorSnapshot, bool) {
	role = CatalogMonitorRole(role)
	if role == "default" {
		snapshot.Roles = nil
		return snapshot, snapshot.Config.Revision != ""
	}
	out, ok := snapshot.Roles[role]
	out.Roles = nil
	return out, ok
}

type MCPCatalogMonitorSummary struct {
	State  string                               `json:"state"`
	Roles  map[string]MCPCatalogMonitorSnapshot `json:"roles"`
	Signal MCPCatalogMonitorSignal              `json:"signal"`
}

func CatalogMonitorSummary(snapshot MCPCatalogMonitorSnapshot, slug string, at time.Time) MCPCatalogMonitorSummary {
	out := MCPCatalogMonitorSummary{Roles: map[string]MCPCatalogMonitorSnapshot{}}
	signal := MCPCatalogMonitorSignal{State: "disabled", Roles: map[string]MCPCatalogMonitorSignal{}}
	roles := make([]string, 0, len(snapshot.Roles))
	if snapshot.Config.Revision != "" {
		roles = append(roles, "default")
	}
	for role := range snapshot.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		view, _ := CatalogMonitorRoleSnapshot(snapshot, role)
		current := CatalogMonitorSignal(view, slug, at)
		view.State = current.State
		view.ObserverStatus = CatalogObserverStatus(view, at)
		view.OAuthLifecycle = CatalogOAuthLifecyclePublic(view.OAuthLifecycle)
		out.Roles[role] = view
		signal.Roles[role] = current
		switch current.State {
		case "healthy":
			if signal.State == "disabled" {
				signal.State = "healthy"
			}
		case "drift":
			signal.DriftedRoles = append(signal.DriftedRoles, role)
			signal.ChangedCount += current.ChangedCount
			if signal.State != "unavailable" {
				signal.State = "drift"
			}
		case "unavailable":
			signal.UnavailableRoles = append(signal.UnavailableRoles, role)
			signal.State = "unavailable"
		}
	}
	// Preserve the original evidence fields for a single-role installation.
	if len(roles) == 1 {
		current := signal.Roles[roles[0]]
		signal.CheckedAt = current.CheckedAt
		signal.ReceiptID = current.ReceiptID
		signal.EvidencePath = current.EvidencePath
	}
	out.State = signal.State
	out.Signal = signal
	return out
}
func (c *Client) GetMCPCatalogMonitorRole(ctx context.Context, slug, role string) (MCPCatalogMonitorSnapshot, error) {
	var out MCPCatalogMonitorSnapshot
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-catalog-monitor?role="+url.QueryEscape(CatalogMonitorRole(role)), nil, &out)
	return out, err
}
func (c *Client) GetMCPCatalogMonitorSummary(ctx context.Context, slug string) (MCPCatalogMonitorSummary, error) {
	var out MCPCatalogMonitorSummary
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-catalog-monitor?all_roles=true", nil, &out)
	return out, err
}

// MCPConnectionScope preserves legacy connections in the default environment.
func MCPConnectionScope(scope string) string {
	if scope == "" {
		return DefaultEnvScope
	}
	return scope
}
