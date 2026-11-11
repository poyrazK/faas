package api

import (
	"context"
	"net/url"
)

// AgentConnectionBudget reads an agent connection's budget and this month's
// spend (ADR-20261111114059); id "self" reads the calling agent's own.
func (c *Client) AgentConnectionBudget(ctx context.Context, id string) (AgentBudgetResponse, error) {
	var out AgentBudgetResponse
	err := c.do(ctx, "GET", "/v1/agent-connections/"+url.PathEscape(id)+"/budget", nil, &out)
	return out, err
}

// AgentConnectionSelfBudget reads the calling agent's own budget.
func (c *Client) AgentConnectionSelfBudget(ctx context.Context) (AgentBudgetResponse, error) {
	return c.AgentConnectionBudget(ctx, "self")
}

// SetAgentConnectionBudget sets an agent connection's budget; a person's
// admin credential only.
func (c *Client) SetAgentConnectionBudget(ctx context.Context, id string, req AgentBudgetRequest) (AgentBudgetResponse, error) {
	var out AgentBudgetResponse
	err := c.do(ctx, "PUT", "/v1/agent-connections/"+url.PathEscape(id)+"/budget", req, &out)
	return out, err
}
