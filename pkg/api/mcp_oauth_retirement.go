package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// MCPOAuthRetirement is account-owned metadata that outlives its source app.
type MCPOAuthRetirement struct {
	Scope                      string                     `json:"scope,omitempty"`
	ID                         string                     `json:"id"`
	AppID                      string                     `json:"app_id"`
	AppSlug                    string                     `json:"app_slug"`
	Role                       string                     `json:"role"`
	Reason                     string                     `json:"reason"`
	State                      string                     `json:"state"`
	CreatedAt                  time.Time                  `json:"created_at"`
	RetainUntil                time.Time                  `json:"retain_until"`
	Revocation                 *MCPCatalogOAuthRevocation `json:"revocation"`
	AllowActiveGrantRevocation bool                       `json:"allow_active_grant_revocation"`
	History                    []MCPOAuthRetirementEvent  `json:"history"`
}
type MCPOAuthRetirementEvent struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
}
type MCPOAuthRetirementsResponse struct {
	Items      []MCPOAuthRetirement `json:"items"`
	NextOffset int                  `json:"next_offset,omitempty"`
}
type MCPOAuthRetirementAction struct {
	Action                     string `json:"action"`
	AllowActiveGrantRevocation bool   `json:"allow_active_grant_revocation,omitempty"`
}

func MCPOAuthRetirementPublic(in MCPOAuthRetirement) MCPOAuthRetirement {
	lifecycle := CatalogOAuthLifecyclePublic(&MCPCatalogOAuthLifecycle{Revocation: in.Revocation})
	in.Revocation = lifecycle.Revocation
	in.History = append([]MCPOAuthRetirementEvent{}, in.History...)
	return in
}
func (c *Client) ListMCPOAuthRetirements(ctx context.Context, offset int) (MCPOAuthRetirementsResponse, error) {
	var out MCPOAuthRetirementsResponse
	err := c.do(ctx, "GET", "/v1/mcp-oauth-retirements?offset="+strconv.Itoa(offset), nil, &out)
	return out, err
}
func (c *Client) RecoverMCPOAuthRetirement(ctx context.Context, id string, action MCPOAuthRetirementAction) (MCPOAuthRetirement, error) {
	var out MCPOAuthRetirement
	err := c.do(ctx, "POST", "/v1/mcp-oauth-retirements/"+url.PathEscape(id), action, &out)
	return out, err
}
