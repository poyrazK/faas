package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetApplicationStandardAssignment(ctx context.Context, org, id string) (ApplicationStandardAssignment, error) {
	var result ApplicationStandardAssignment
	err := c.do(ctx, http.MethodGet, standardResourcePath(org, "assignments")+"/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *Client) ListApplicationStandardAssignments(ctx context.Context, org, after string, limit int) (ApplicationStandardAssignmentList, error) {
	var result ApplicationStandardAssignmentList
	err := c.do(ctx, http.MethodGet, standardResourcePath(org, "assignments")+standardResourceQuery(after, limit), nil, &result)
	return result, err
}
