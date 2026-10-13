package api

import (
	"context"
	"net/url"
)

// PutAppMCPTaskCompatibilityReport replaces the bounded handler-compatibility
// snapshot published by an MCP task observer.
func (c *Client) PutAppMCPTaskCompatibilityReport(ctx context.Context, slug string, report MCPTaskCompatibilityReportRequest) error {
	return c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/mcp-task-compatibility", report, nil)
}

// GetAppMCPTaskCompatibilityReport reads the latest handler-compatibility
// snapshot and its server-computed freshness for an app.
func (c *Client) GetAppMCPTaskCompatibilityReport(ctx context.Context, slug string) (MCPTaskCompatibilityReportResponse, error) {
	var out MCPTaskCompatibilityReportResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-task-compatibility", nil, &out)
	return out, err
}
