package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ApproveApplicationStandardReview(ctx context.Context, org, id string, request ApproveApplicationStandardReviewRequest) (ApplicationStandardOperation, error) {
	var result ApplicationStandardOperation
	err := c.do(ctx, http.MethodPost, standardResourcePath(org, "reviews")+"/"+url.PathEscape(id)+"/approve", request, &result)
	return result, err
}

func (c *Client) PauseApplicationStandardOperation(ctx context.Context, org, id string, request ControlApplicationStandardOperationRequest) (ApplicationStandardOperation, error) {
	return c.controlApplicationStandardOperation(ctx, org, id, "pause", request)
}

func (c *Client) ResumeApplicationStandardOperation(ctx context.Context, org, id string, request ControlApplicationStandardOperationRequest) (ApplicationStandardOperation, error) {
	return c.controlApplicationStandardOperation(ctx, org, id, "resume", request)
}

func (c *Client) AbortApplicationStandardOperation(ctx context.Context, org, id string, request ControlApplicationStandardOperationRequest) (ApplicationStandardOperation, error) {
	return c.controlApplicationStandardOperation(ctx, org, id, "abort", request)
}

func (c *Client) controlApplicationStandardOperation(ctx context.Context, org, id, action string, request ControlApplicationStandardOperationRequest) (ApplicationStandardOperation, error) {
	var result ApplicationStandardOperation
	err := c.do(ctx, http.MethodPost, standardResourcePath(org, "operations")+"/"+url.PathEscape(id)+"/"+action, request, &result)
	return result, err
}
