package api

import (
	"context"
	"net/url"
	"strconv"
)

// ListOutboundAIQualityAlerts lists an integration's drift alerts, newest
// first (ADR 20261021171712). A zero limit is the server's default.
func (c *Client) ListOutboundAIQualityAlerts(ctx context.Context, integrationID string, limit int) (OutboundAIQualityAlertList, error) {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-quality-alerts"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out OutboundAIQualityAlertList
	return out, c.do(ctx, "GET", path, nil, &out)
}

// SetOutboundAIRequestFeedback rates one logged call (ADR 20261021171712).
func (c *Client) SetOutboundAIRequestFeedback(ctx context.Context, integrationID, requestID string, feedback PutOutboundAIFeedbackRequest) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-requests/" + url.PathEscape(requestID) + "/feedback"
	return c.do(ctx, "PUT", path, feedback, nil)
}
