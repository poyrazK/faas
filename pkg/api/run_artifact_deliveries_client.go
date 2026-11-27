package api

import (
	"context"
	"net/http"
	"net/url"
)

func runArtifactDeliveryPath(app, route string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/run-artifact-deliveries/" + url.PathEscape(route)
}

func (c *Client) DeliverRunArtifact(ctx context.Context, app, route string, r DeliverRunArtifactRequest) (RunArtifactDelivery, error) {
	var out RunArtifactDelivery
	err := c.doWithClientAndHeadersAndIdempotencyKeyPolicy(ctx, c.uploadHTTP(), http.MethodPost, runArtifactDeliveryPath(app, route), r, &out, "", nil, false)
	return out, err
}

func (c *Client) GetRunArtifactDelivery(ctx context.Context, app, route, id string) (RunArtifactDelivery, error) {
	var out RunArtifactDelivery
	err := c.do(ctx, http.MethodGet, runArtifactDeliveryPath(app, route)+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) SignRunArtifactDelivery(ctx context.Context, app, route, id string) (ObjectSignedRequest, error) {
	var out ObjectSignedRequest
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, runArtifactDeliveryPath(app, route)+"/"+url.PathEscape(id)+"/download-url", nil, &out)
	return out, err
}
