package api

import (
	"context"
	"net/url"
	"strconv"
)

// AgentActivityEntry is one write request an agent key made
// (ADR-20261107220000).
type AgentActivityEntry struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	// ApprovalID names the approval request the write went through on.
	ApprovalID string `json:"approval_id,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// AgentActivityResponse is one page of an agent's activity, newest first.
type AgentActivityResponse struct {
	Activity []AgentActivityEntry `json:"activity"`
	// NextBefore, passed as ?before, fetches the next older page; empty on
	// the last page.
	NextBefore string `json:"next_before,omitempty"`
}

// AgentActivityQuery pages an activity read; zero values take the defaults.
type AgentActivityQuery struct {
	Before string
	Limit  int
}

func (q AgentActivityQuery) encode(path string) string {
	v := url.Values{}
	if q.Before != "" {
		v.Set("before", q.Before)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// AgentConnectionActivity reads the write requests of one of the caller's
// agent connections; id "self" reads the calling agent's own.
func (c *Client) AgentConnectionActivity(ctx context.Context, id string, q AgentActivityQuery) (AgentActivityResponse, error) {
	var out AgentActivityResponse
	err := c.do(ctx, "GET", q.encode("/v1/agent-connections/"+url.PathEscape(id)+"/activity"), nil, &out)
	return out, err
}

// OrgAgentConnectionActivity reads the write requests of an agent acting in
// the organization; owners and admins only.
func (c *Client) OrgAgentConnectionActivity(ctx context.Context, org, id string, q AgentActivityQuery) (AgentActivityResponse, error) {
	var out AgentActivityResponse
	err := c.do(ctx, "GET", q.encode("/v1/orgs/"+url.PathEscape(org)+"/agent-connections/"+url.PathEscape(id)+"/activity"), nil, &out)
	return out, err
}
