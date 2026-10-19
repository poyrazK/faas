package api

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MCPReadinessTokenHeader = "MCP-Probe-Authorization"
const CodeMCPReadinessRequired = "mcp_readiness_required"

var readinessRolePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var readinessHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type MCPReadinessRole struct {
	Monitor         *MCPReadinessMonitorConfig `json:"monitor,omitempty"`
	Name            string                     `json:"name"`
	ProtocolVersion string                     `json:"protocol_version"`
	CatalogFormat   int                        `json:"catalog_format"`
	CatalogSHA256   string                     `json:"catalog_sha256"`
}
type MCPReadinessPolicy struct {
	RestoredFrom  string             `json:"restored_from,omitempty"`
	Scope         string             `json:"scope,omitempty"`
	Revision      string             `json:"revision"`
	Enabled       bool               `json:"enabled"`
	Endpoint      string             `json:"endpoint"`
	MaxAgeSeconds int                `json:"max_age_seconds"`
	Roles         []MCPReadinessRole `json:"roles"`
	Reason        string             `json:"reason,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at"`
	UpdatedBy     string             `json:"updated_by,omitempty"`
}
type SetMCPReadinessPolicyRequest struct {
	Scope            string             `json:"scope,omitempty"`
	ExpectedRevision string             `json:"expected_revision"`
	Enabled          bool               `json:"enabled"`
	Endpoint         string             `json:"endpoint"`
	MaxAgeSeconds    int                `json:"max_age_seconds"`
	Roles            []MCPReadinessRole `json:"roles"`
	Reason           string             `json:"reason"`
}

func ValidateMCPReadinessPolicy(r SetMCPReadinessPolicyRequest) error {
	if ValidateScope(MCPConnectionScope(r.Scope)) != nil {
		return errors.New("invalid readiness environment scope")
	}
	u, e := url.Parse(r.Endpoint)
	if e != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || u.Path[0] != '/' || len(r.Endpoint) > 256 || len(r.Endpoint) > 1 && r.Endpoint[:2] == "//" || u.RawPath != "" {
		return errors.New("endpoint must be a plain absolute path")
	}
	if r.MaxAgeSeconds < 60 || r.MaxAgeSeconds > 3600 || len(r.Roles) > 32 || (r.Enabled && len(r.Roles) == 0) {
		return errors.New("require 1–32 roles when enabled and max_age_seconds between 60 and 3600")
	}
	if len(r.Reason) < 1 || len(r.Reason) > 512 {
		return errors.New("reason must contain 1–512 bytes")
	}
	seen := map[string]bool{}
	for _, role := range r.Roles {
		if !readinessRolePattern.MatchString(role.Name) || seen[role.Name] || !ValidMCPProbeProtocolVersion(role.ProtocolVersion) || (role.CatalogFormat != 1 && role.CatalogFormat != 2) || !readinessHashPattern.MatchString(role.CatalogSHA256) {
			return errors.New("roles require unique names, a supported protocol, catalog_format 1 or 2 and a SHA-256")
		}
		if role.Monitor != nil {
			if err := ValidateMCPReadinessMonitor(*role.Monitor); err != nil {
				return err
			}
			if r.Enabled && role.Monitor.Enabled && role.Monitor.IntervalSeconds > r.MaxAgeSeconds/2 {
				return errors.New("monitor interval must be at most half the evidence maximum age")
			}
		}
		seen[role.Name] = true
	}
	return nil
}

type MCPReadinessCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}
type MCPReadinessEvidence struct {
	ID              string              `json:"id"`
	DeploymentID    string              `json:"deployment_id"`
	Role            string              `json:"role"`
	PolicyRevision  string              `json:"policy_revision"`
	ArtifactSHA256  string              `json:"artifact_sha256"`
	ProtocolVersion string              `json:"protocol_version"`
	CatalogFormat   int                 `json:"catalog_format"`
	CatalogSHA256   string              `json:"catalog_sha256,omitempty"`
	Passed          bool                `json:"passed"`
	ObservedAt      time.Time           `json:"observed_at"`
	Checks          []MCPReadinessCheck `json:"checks"`
}
type MCPReadinessSnapshot struct {
	Policy     MCPReadinessPolicy          `json:"policy"`
	Evidence   []MCPReadinessEvidence      `json:"evidence"`
	Overrides  []MCPReadinessOverride      `json:"overrides"`
	Monitoring []MCPReadinessMonitorStatus `json:"monitoring"`
}
type MCPReadinessOverride struct {
	ID             string     `json:"id"`
	DeploymentID   string     `json:"deployment_id"`
	PolicyRevision string     `json:"policy_revision"`
	ArtifactSHA256 string     `json:"artifact_sha256"`
	Reason         string     `json:"reason"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	UsedAt         *time.Time `json:"used_at,omitempty"`
}
type MCPReadinessProbeRequest struct {
	Role string `json:"role"`
}
type MCPReadinessOverrideRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func (c *Client) GetMCPReadinessPolicy(ctx context.Context, slug string, scopes ...string) (MCPReadinessPolicy, error) {
	var out MCPReadinessPolicy
	path := "/v1/apps/" + url.PathEscape(slug) + "/mcp-readiness-policy"
	if len(scopes) > 0 {
		if ValidateScope(MCPConnectionScope(scopes[0])) != nil {
			return out, errors.New("invalid readiness scope")
		}
		path += "?scope=" + url.QueryEscape(MCPConnectionScope(scopes[0]))
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
func (c *Client) SetMCPReadinessPolicy(ctx context.Context, slug string, r SetMCPReadinessPolicyRequest) (MCPReadinessPolicy, error) {
	var out MCPReadinessPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/mcp-readiness-policy", r, &out)
	return out, err
}
func (c *Client) GetMCPReadiness(ctx context.Context, id string) (MCPReadinessSnapshot, error) {
	var out MCPReadinessSnapshot
	err := c.do(ctx, "GET", "/v1/deployments/"+url.PathEscape(id)+"/mcp-readiness", nil, &out)
	return out, err
}

// Probe credentials use the existing separately forwarded MCP probe header path.

func (c *Client) ProbeMCPReadiness(ctx context.Context, id, role, token string) (MCPReadinessEvidence, error) {
	var out MCPReadinessEvidence
	if !ValidMCPProbeToken(token) {
		return out, errors.New("invalid MCP probe token")
	}
	headers := http.Header{}
	if token != "" {
		headers.Set(MCPReadinessTokenHeader, token)
	}
	err := c.doWithHeaders(ctx, "POST", "/v1/deployments/"+url.PathEscape(id)+"/mcp-readiness", MCPReadinessProbeRequest{Role: role}, &out, headers)
	return out, err
}
func (c *Client) OverrideMCPReadiness(ctx context.Context, id string, r MCPReadinessOverrideRequest) (MCPReadinessOverride, error) {
	var out MCPReadinessOverride
	err := c.do(ctx, "POST", "/v1/deployments/"+url.PathEscape(id)+"/mcp-readiness-overrides", r, &out)
	return out, err
}

// Monitoring configuration contains references only; tokens never enter policy JSON.
type MCPReadinessMonitorConfig struct {
	OAuthConnections map[string]MCPReadinessOAuthConnection `json:"oauth_connections,omitempty"`
	OAuthConnection  *MCPReadinessOAuthConnection           `json:"oauth_connection,omitempty"`
	Enabled          bool                                   `json:"enabled"`
	IntervalSeconds  int                                    `json:"interval_seconds"`
	FailureThreshold int                                    `json:"failure_threshold"`
	SecretRef        string                                 `json:"secret_ref,omitempty"`
}

type MCPReadinessOAuthConnection struct {
	Scope    string `json:"scope,omitempty"`
	Role     string `json:"role"`
	Revision string `json:"revision"`
}

func ValidateMCPReadinessMonitor(c MCPReadinessMonitorConfig) error {
	if len(c.OAuthConnections) > 32 || c.OAuthConnection != nil && len(c.OAuthConnections) > 0 || c.ManagedOAuth() && c.SecretRef != "" {
		return errors.New("choose one managed OAuth source or secret_ref")
	}
	refs := c.OAuthReferences()
	for _, ref := range refs {
		if !ValidCatalogMonitorRole(ref.Role) || ref.Role == "" || ValidateScope(MCPConnectionScope(ref.Scope)) != nil {
			return errors.New("OAuth binding requires a valid role and environment scope")
		}
		if _, err := uuid.Parse(ref.Revision); err != nil {
			return errors.New("OAuth binding requires a revision UUID")
		}
	}
	for scope, ref := range c.OAuthConnections {
		if ValidateScope(scope) != nil || ref.Scope != "" && ref.Scope != scope {
			return errors.New("OAuth map keys must match connection scopes")
		}
	}

	if c.IntervalSeconds < 60 || c.IntervalSeconds > 3600 || c.FailureThreshold < 1 || c.FailureThreshold > 10 {
		return errors.New("monitor requires interval_seconds 60–3600 and failure_threshold 1–10")
	}
	if c.SecretRef != "" && (!strings.HasPrefix(c.SecretRef, SecretRefPrefix) || ValidateEnvKey(strings.TrimPrefix(c.SecretRef, SecretRefPrefix)) != nil) {
		return errors.New("monitor credential must be an app secret reference")
	}
	return nil
}

type MCPReadinessMonitorObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Passed     bool      `json:"passed"`
	Reason     string    `json:"reason,omitempty"`
}
type MCPReadinessMonitorStatus struct {
	Scope               string                           `json:"scope,omitempty"`
	OAuthConnection     *MCPReadinessOAuthConnection     `json:"oauth_connection,omitempty"`
	RecoveryAction      string                           `json:"recovery_action,omitempty"`
	DeploymentID        string                           `json:"deployment_id"`
	Role                string                           `json:"role"`
	PolicyRevision      string                           `json:"policy_revision"`
	ArtifactSHA256      string                           `json:"artifact_sha256,omitempty"`
	NextProbeAt         time.Time                        `json:"next_probe_at"`
	LastSuccessAt       *time.Time                       `json:"last_success_at,omitempty"`
	ConsecutiveFailures int                              `json:"consecutive_failures"`
	FailureThreshold    int                              `json:"failure_threshold"`
	State               string                           `json:"state"`
	Reason              string                           `json:"reason,omitempty"`
	History             []MCPReadinessMonitorObservation `json:"history"`
}

// ReadinessMonitorState never reports stale observations as healthy.
func ReadinessMonitorState(s MCPReadinessMonitorStatus, at time.Time) string {
	if at.After(s.NextProbeAt.Add(90 * time.Second)) {
		return "stale"
	}
	if s.ConsecutiveFailures >= s.FailureThreshold {
		return "firing"
	}
	if s.ConsecutiveFailures > 0 {
		return "degraded"
	}
	if s.LastSuccessAt == nil {
		return "pending"
	}
	return "healthy"
}

const MetricMCPReadinessUnhealthy = "mcp_readiness_unhealthy"

// ManagedOAuth distinguishes a missing environment binding from a static source.
func (c *MCPReadinessMonitorConfig) ManagedOAuth() bool {
	return c != nil && (c.OAuthConnection != nil || len(c.OAuthConnections) > 0)
}
func (c *MCPReadinessMonitorConfig) OAuthReferences() []MCPReadinessOAuthConnection {
	if c == nil {
		return nil
	}
	var out []MCPReadinessOAuthConnection
	if c.OAuthConnection != nil {
		out = append(out, *c.OAuthConnection)
	}
	for scope, ref := range c.OAuthConnections {
		ref.Scope = scope
		out = append(out, ref)
	}
	return out
}
func (c *MCPReadinessMonitorConfig) OAuthForScope(scope string) *MCPReadinessOAuthConnection {
	if c == nil {
		return nil
	}
	scope = MCPConnectionScope(scope)
	if c.OAuthConnection != nil && MCPConnectionScope(c.OAuthConnection.Scope) == scope {
		ref := *c.OAuthConnection
		return &ref
	}
	if ref, ok := c.OAuthConnections[scope]; ok {
		ref.Scope = scope
		return &ref
	}
	return nil
}
