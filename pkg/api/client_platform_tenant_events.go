package api

import (
	"context"
	"net/url"
	"strconv"
)

// PublishPlatformTenantSelfEvent publishes an event as the tenant bound to
// the client's token. The caller's id is scoped by tenant, app, and source.
func (c *Client) PublishPlatformTenantSelfEvent(ctx context.Context, slug string, req PublishEventRequest) (PlatformTenantPublishEventResponse, error) {
	var out PlatformTenantPublishEventResponse
	path := "/v1/platform-tenant-self/apps/" + url.PathEscape(slug) + "/events:publish"
	return out, c.do(ctx, "POST", path, req, &out)
}

// GetPlatformTenantSelfEventReceipt reads the tenant-owned fanout and workflow
// start evidence for one published event.
func (c *Client) GetPlatformTenantSelfEventReceipt(ctx context.Context, slug, eventID, source string, limit int, after string) (EventReceiptResponse, error) {
	var out EventReceiptResponse
	query := url.Values{"source": {source}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if after != "" {
		query.Set("after", after)
	}
	path := "/v1/platform-tenant-self/apps/" + url.PathEscape(slug) + "/events/receipts/" + url.PathEscape(eventID) + "?" + query.Encode()
	return out, c.do(ctx, "GET", path, nil, &out)
}
