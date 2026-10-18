package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type AppWakeHistoryPageOptions struct {
	Since, Until, Cursor string
	Limit                int
}

// GetAppWakeHistoryPage requests opt-in keyset_v1 history. Older servers may
// omit Page; callers must not manufacture continuation from their bounded rows.
func (c *Client) GetAppWakeHistoryPage(ctx context.Context, slug string, opts AppWakeHistoryPageOptions) (AppWakeTimelineResponse, error) {
	q := url.Values{"pagination": {"keyset_v1"}}
	for key, value := range map[string]string{"since": opts.Since, "until": opts.Until, "cursor": opts.Cursor} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out AppWakeTimelineResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/wake-timeline?"+q.Encode(), nil, &out)
	return out, err
}
