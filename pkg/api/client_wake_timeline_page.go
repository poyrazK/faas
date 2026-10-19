package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type WakeTimelinePageOptions struct {
	Since  string
	Cursor string
	Limit  int
}

// ListWakeTimelinePage requests keyset_v1. Older servers may omit Page; callers
// must not treat a legacy timestamp hint as a lossless keyset continuation.
func (c *Client) ListWakeTimelinePage(ctx context.Context, slug, wakeID string, opts WakeTimelinePageOptions) (WakeTimelineResponse, error) {
	q := url.Values{"pagination": {"keyset_v1"}}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out WakeTimelineResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/wakes/" + url.PathEscape(wakeID) + "/timeline?" + q.Encode()
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
