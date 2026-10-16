package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// UpdateEdgeRuleIfMatch updates an existing rule against an exact native
// rule-set version. Reuse the same key, version and body after a lost response.
func (c *Client) UpdateEdgeRuleIfMatch(ctx context.Context, id string, version int, key string, req UpdateEdgeRuleRequest) (EdgeRuleResponse, error) {
	var out EdgeRuleResponse
	if id == "" || version < 1 || key == "" {
		return out, fmt.Errorf("rule ID, positive version and stable idempotency key are required")
	}
	headers := http.Header{"If-Match": {EdgeRuleSetETag(version)}}
	err := c.doWithClientAndHeadersAndIdempotencyKey(ctx, c.http, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(id), req, &out, key, headers)
	return out, err
}
