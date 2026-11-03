package api

import (
	"context"
	"net/url"
)

// The ways an agent connects to an account (ADR-20261030133644).
const (
	// AgentConnectionHostedMCP is a browser sign-in through the hosted MCP
	// server, which holds an mcp-login key for it.
	AgentConnectionHostedMCP = "hosted_mcp"
	// AgentConnectionAgentKey is an API key with the agent scope.
	AgentConnectionAgentKey = "agent_key"
	// AgentClientNameMax bounds an agent client's name, in characters.
	AgentClientNameMax = 80
)

// AgentConnectionResponse is one agent connected to the account. ID is the
// API key the agent uses; revoking the connection revokes that key.
type AgentConnectionResponse struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	ClientName string   `json:"client_name,omitempty"`
	ClientSite string   `json:"client_site,omitempty"`
	Label      string   `json:"label,omitempty"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
	ExpiresAt  string   `json:"expires_at,omitempty"`
	// OrgID is the organization the agent acts in (ADR-20261101225122).
	OrgID string `json:"org_id,omitempty"`
	// Member is the email of the person who connected the agent, in an
	// organization's list.
	Member string `json:"member,omitempty"`
}

// AgentConnectionsResponse lists the account's connected agents, newest
// first.
type AgentConnectionsResponse struct {
	Items []AgentConnectionResponse `json:"items"`
}

// AgentConnectionsRevokeAllResponse counts the connections revoked.
type AgentConnectionsRevokeAllResponse struct {
	Revoked int `json:"revoked"`
}

// OrgAgentPolicyRequest sets an organization's agent policy
// (ADR-20261101225122). Both fields are required.
type OrgAgentPolicyRequest struct {
	AgentsAllowed *bool     `json:"agents_allowed"`
	AllowedScopes *[]string `json:"allowed_scopes"`
}

// OrgAgentPolicyResponse is an organization's agent policy. Revoked counts
// the agent connections a change revoked.
type OrgAgentPolicyResponse struct {
	AgentsAllowed bool     `json:"agents_allowed"`
	AllowedScopes []string `json:"allowed_scopes"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
	Revoked       int      `json:"revoked,omitempty"`
}

// AgentClientRequest names the client of the calling hosted MCP key. Site
// is a host name the client was verified at, or empty.
type AgentClientRequest struct {
	ClientName string `json:"client_name"`
	ClientSite string `json:"client_site,omitempty"`
}

// ListAgentConnections returns the agents connected to the account.
func (c *Client) ListAgentConnections(ctx context.Context) (AgentConnectionsResponse, error) {
	var out AgentConnectionsResponse
	return out, c.do(ctx, "GET", "/v1/agent-connections", nil, &out)
}

// RevokeAgentConnection revokes one connected agent's key.
func (c *Client) RevokeAgentConnection(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/agent-connections/"+url.PathEscape(id), nil, nil)
}

// RevokeAllAgentConnections revokes every connected agent's key.
func (c *Client) RevokeAllAgentConnections(ctx context.Context) (AgentConnectionsRevokeAllResponse, error) {
	var out AgentConnectionsRevokeAllResponse
	return out, c.do(ctx, "POST", "/v1/agent-connections/revoke_all", nil, &out)
}

// NameAgentClient names the client of the calling hosted MCP key, once.
func (c *Client) NameAgentClient(ctx context.Context, req AgentClientRequest) error {
	return c.do(ctx, "PUT", "/v1/agent-connections/self", req, nil)
}

// DisconnectAgent revokes the calling hosted MCP key itself.
func (c *Client) DisconnectAgent(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/v1/agent-connections/self", nil, nil)
}

// ListOrgAgentConnections returns the agents acting in an organization.
func (c *Client) ListOrgAgentConnections(ctx context.Context, org string) (AgentConnectionsResponse, error) {
	var out AgentConnectionsResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+url.PathEscape(org)+"/agent-connections", nil, &out)
}

// RevokeOrgAgentConnection revokes one agent acting in an organization.
func (c *Client) RevokeOrgAgentConnection(ctx context.Context, org, id string) error {
	return c.do(ctx, "DELETE", "/v1/orgs/"+url.PathEscape(org)+"/agent-connections/"+url.PathEscape(id), nil, nil)
}

// RevokeAllOrgAgentConnections revokes every agent acting in an organization.
func (c *Client) RevokeAllOrgAgentConnections(ctx context.Context, org string) (AgentConnectionsRevokeAllResponse, error) {
	var out AgentConnectionsRevokeAllResponse
	return out, c.do(ctx, "POST", "/v1/orgs/"+url.PathEscape(org)+"/agent-connections/revoke_all", nil, &out)
}

// GetOrgAgentPolicy returns an organization's agent policy.
func (c *Client) GetOrgAgentPolicy(ctx context.Context, org string) (OrgAgentPolicyResponse, error) {
	var out OrgAgentPolicyResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+url.PathEscape(org)+"/agent-policy", nil, &out)
}

// SetOrgAgentPolicy sets an organization's agent policy.
func (c *Client) SetOrgAgentPolicy(ctx context.Context, org string, req OrgAgentPolicyRequest) (OrgAgentPolicyResponse, error) {
	var out OrgAgentPolicyResponse
	return out, c.do(ctx, "PUT", "/v1/orgs/"+url.PathEscape(org)+"/agent-policy", req, &out)
}
