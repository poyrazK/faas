package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// OutboundAITraceQuery filters a trace listing.
type OutboundAITraceQuery struct {
	AppID   string
	EndUser string
	Before  *time.Time
	Limit   int
}

// ListOutboundAITraces pages an integration's traces newest first
// (ADR 20261020202035).
func (c *Client) ListOutboundAITraces(ctx context.Context, integrationID string, query OutboundAITraceQuery) (OutboundAITraceList, error) {
	values := url.Values{}
	if query.AppID != "" {
		values.Set("app_id", query.AppID)
	}
	if query.EndUser != "" {
		values.Set("end_user", query.EndUser)
	}
	if query.Before != nil {
		values.Set("before", query.Before.UTC().Format(time.RFC3339Nano))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-traces"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out OutboundAITraceList
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetOutboundAITrace reads one trace with its logged calls
// (ADR 20261020202035).
func (c *Client) GetOutboundAITrace(ctx context.Context, integrationID, traceID string) (OutboundAITraceResponse, error) {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-traces/" + url.PathEscape(traceID)
	var out OutboundAITraceResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}
