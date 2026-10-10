package api

import (
	"context"
	"errors"
	"net/url"
)

// RetryDurableEntity re-arms metadata only. After a lost response, inspect again;
// replaying the old observation is a conflict rather than a second reset.
func (c *Client) RetryDurableEntity(ctx context.Context, slug string, request DurableEntityRetryRequest) (DurableEntityRetryResponse, error) {
	var out DurableEntityRetryResponse
	if request.Namespace == "" || request.Key == "" || request.ExpectedVersion == 0 || request.ExpectedRecoveryRevision == "" || request.Target != "alarm" && request.Target != "outbox" || request.Target == "alarm" && (request.AlarmAt == nil || request.HeadID != "") || request.Target == "outbox" && (request.HeadID == "" || request.AlarmAt != nil) {
		return out, errors.New("durable entity retry requires a fresh inspection and exactly one recovery target")
	}
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/entities/retry", request, &out)
}
