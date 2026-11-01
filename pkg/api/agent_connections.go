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
