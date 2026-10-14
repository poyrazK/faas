package api

import (
	"context"
	"net/url"
	"strconv"
)

type MCPAdmissionHistoryResponse struct {
	Items      []MCPAdmissionEvidence `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	Limit      int                    `json:"limit"`
}

func (c *Client) GetMCPAdmissionReceipt(ctx context.Context, id string) (MCPAdmissionEvidence, error) {
	var out MCPAdmissionEvidence
	err := c.do(ctx, "GET", "/v1/mcp/admissions/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ListMCPAdmissionHistory(ctx context.Context, deploymentID, cursor, outcome string, limit int) (MCPAdmissionHistoryResponse, error) {
	var out MCPAdmissionHistoryResponse
	q := url.Values{"limit": {strconv.Itoa(limit)}, "cursor": {cursor}, "outcome": {outcome}}
	err := c.do(ctx, "GET", "/v1/deployments/"+url.PathEscape(deploymentID)+"/mcp-admissions?"+q.Encode(), nil, &out)
	return out, err
}
