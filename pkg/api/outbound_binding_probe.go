// adr: 430 — outbound canaries use explicit safe-method policy and managed identity.
package api

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

// Valid accepts only safe methods, canonical paths and successful expected statuses.
func (p OutboundBindingProbePolicy) Valid() bool {
	return (p.Method == "GET" || p.Method == "HEAD") && len(p.Path) <= routepolicy.MaxPrefixLength && routepolicy.CanonicalPath(p.Path) &&
		p.ExpectedStatus >= 200 && p.ExpectedStatus <= 299
}

// ValidOutboundProbeGateway accepts an operator-provisioned HTTPS gateway origin.
func ValidOutboundProbeGateway(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		(u.Path == "" || u.Path == "/") && strings.TrimSpace(raw) == raw && !strings.ContainsAny(raw, "?#")
}

// OutboundBindingProbeSpec is selected by apid, never taken from a customer task request.
// Provider credentials stay inside outboundd; this spec contains only routing metadata.
type OutboundBindingProbeSpec struct {
	IntegrationID string                     `json:"integration_id"`
	GatewayURL    string                     `json:"gateway_url"`
	Policy        OutboundBindingProbePolicy `json:"policy"`
}

func (s OutboundBindingProbeSpec) Valid() bool {
	id, err := uuid.Parse(s.IntegrationID)
	return err == nil && id.String() == s.IntegrationID && ValidOutboundProbeGateway(s.GatewayURL) && s.Policy.Valid()
}

type OutboundBindingProbeCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// OutboundBindingProbeReport excludes response bodies/headers, URLs and identity tokens.
// A pass establishes the configured endpoint's response through the outbound gateway.
type OutboundBindingProbeReport struct {
	App           string                    `json:"app,omitempty"`
	IntegrationID string                    `json:"integration_id"`
	TaskID        string                    `json:"task_id,omitempty"`
	DeploymentID  string                    `json:"deployment_id,omitempty"`
	Configuration OutboundBindingProbeCheck `json:"configuration"`
	Identity      OutboundBindingProbeCheck `json:"identity"`
	Gateway       OutboundBindingProbeCheck `json:"gateway"`
	Response      OutboundBindingProbeCheck `json:"response"`
	Error         string                    `json:"error,omitempty"`
}

func (r OutboundBindingProbeReport) Passed() bool {
	return r.Error == "" && r.Configuration.Status == "passed" && r.Identity.Status == "passed" && r.Gateway.Status == "passed" && r.Response.Status == "passed"
}

func (c *Client) GetOutboundBindingProbePolicy(ctx context.Context, integration string) (OutboundBindingProbePolicy, error) {
	var out OutboundBindingProbePolicy
	err := c.do(ctx, "GET", "/v1/outbound/integrations/"+url.PathEscape(integration)+"/probe-policy", nil, &out)
	return out, err
}
func (c *Client) SetOutboundBindingProbePolicy(ctx context.Context, integration string, policy OutboundBindingProbePolicy) (OutboundBindingProbePolicy, error) {
	var out OutboundBindingProbePolicy
	err := c.do(ctx, "PUT", "/v1/outbound/integrations/"+url.PathEscape(integration)+"/probe-policy", policy, &out)
	return out, err
}
func (c *Client) DeleteOutboundBindingProbePolicy(ctx context.Context, integration string) error {
	return c.do(ctx, "DELETE", "/v1/outbound/integrations/"+url.PathEscape(integration)+"/probe-policy", nil, nil)
}
