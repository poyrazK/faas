package api

import (
	"context"
	"net/url"
	"time"
)

func (c *Client) GetAppProfiles(ctx context.Context, slug string, query ProfileQuery) (ProfileResponse, error) {
	var out ProfileResponse
	values := url.Values{"route": {query.Route}, "deployment_id": {query.DeploymentID}, "runtime": {query.Runtime}, "start": {query.Start.Format(time.RFC3339Nano)}, "end": {query.End.Format(time.RFC3339Nano)}}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles?"+values.Encode(), nil, &out)
	return out, err
}

func (c *Client) CompareAppProfiles(ctx context.Context, slug string, query ProfileCompareRequest) (ProfileCompareResponse, error) {
	var out ProfileCompareResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/profiles/compare", query, &out)
	return out, err
}
