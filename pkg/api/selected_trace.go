package api

import (
	"context"
	"net/url"
	"strconv"
)

// GetSelectedAppTrace reads only explicitly selected app IDs. It never falls
// back to the account-wide endpoint when the native API is older.
func (c *Client) GetSelectedAppTrace(ctx context.Context, traceID string, appIDs []string, limit int) (SelectedTraceLookupResponse, error) {
	var out SelectedTraceLookupResponse
	q := url.Values{}
	for _, id := range appIDs {
		q.Add("app_id", id)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return out, c.do(ctx, "GET", "/v1/account/traces/"+url.PathEscape(traceID)+"/selected-apps?"+q.Encode(), nil, &out)
}

// ValidTraceID reports whether id is a nonzero canonical W3C trace identifier.
func ValidTraceID(id string) bool {
	if len(id) != 32 {
		return false
	}
	nonzero := false
	for _, c := range id {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
		nonzero = nonzero || c != '0'
	}
	return nonzero
}
