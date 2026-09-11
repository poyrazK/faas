package api

import "context"

// CreatePrewarm schedules temporary capacity restoration ahead of a known
// demand window. The scheduler starts the restore during its lead time.
func (c *Client) CreatePrewarm(ctx context.Context, slug string, req PrewarmRequest) (PrewarmIntentResponse, error) {
	var out PrewarmIntentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/prewarm", req, &out)
}

// ListPrewarms returns scheduled and completed prewarm intents for an app.
func (c *Client) ListPrewarms(ctx context.Context, slug string) ([]PrewarmIntentResponse, error) {
	var out []PrewarmIntentResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/prewarms", nil, &out)
}

// CancelPrewarm cancels a pending prewarm intent. Claimed intents cannot be
// cancelled because their restore may already be in flight.
func (c *Client) CancelPrewarm(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/prewarms/"+id, nil, nil)
}
