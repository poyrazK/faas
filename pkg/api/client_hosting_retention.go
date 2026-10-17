package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) GetHostingCleanupPlan(ctx context.Context, site string, rules *HostingRetentionRules) (HostingCleanupPlan, error) {
	path := hostingSitePath(site) + "/cleanup"
	if rules != nil {
		q := url.Values{}
		q.Set("pending_age_seconds", strconv.FormatInt(rules.PendingAgeSeconds, 10))
		q.Set("staged_age_seconds", strconv.FormatInt(rules.StagedAgeSeconds, 10))
		path += "?" + q.Encode()
	}
	var out HostingCleanupPlan
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c *Client) SetHostingRetentionPolicy(ctx context.Context, site string, req SetHostingRetentionRequest) (HostingRetentionPolicy, error) {
	var out HostingRetentionPolicy
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, hostingSitePath(site)+"/retention", req, &out)
	return out, err
}
