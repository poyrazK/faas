package api

import (
	"context"
	"net/url"
)

func (c *Client) ListOutboundIntegrationOffers(ctx context.Context) (OutboundIntegrationOfferList, error) {
	var out OutboundIntegrationOfferList
	return out, c.do(ctx, "GET", "/v1/outbound/integrations", nil, &out)
}

func (c *Client) CreateOutboundIntegration(ctx context.Context, req CreateOutboundIntegrationRequest) (OutboundIntegrationOffer, error) {
	var out OutboundIntegrationOffer
	return out, c.do(ctx, "POST", "/v1/outbound/integrations", req, &out)
}

func (c *Client) DeleteOutboundIntegration(ctx context.Context, integrationID string) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID)
	return c.do(ctx, "DELETE", path, nil, nil)
}

func (c *Client) GetOutboundIntegrationUsage(ctx context.Context, integrationID string) (OutboundIntegrationUsageResponse, error) {
	var out OutboundIntegrationUsageResponse
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/usage"
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) SetOutboundIntegrationDailyBudget(ctx context.Context, integrationID string, limit *int64) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/budget"
	return c.do(ctx, "PUT", path, PutOutboundDailyRequestBudgetRequest{DailyRequestLimit: limit}, nil)
}

func (c *Client) ListOutboundAppBindings(ctx context.Context, slug string) (OutboundAppBindingList, error) {
	var out OutboundAppBindingList
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/outbound-bindings", nil, &out)
}

func (c *Client) BindOutboundIntegration(ctx context.Context, slug, integrationID string) (OutboundAppBinding, error) {
	var out OutboundAppBinding
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID)
	return out, c.do(ctx, "PUT", path, nil, &out)
}

func (c *Client) UnbindOutboundIntegration(ctx context.Context, slug, integrationID string) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID)
	return c.do(ctx, "DELETE", path, nil, nil)
}

func (c *Client) UpdateOutboundBindingPolicy(ctx context.Context, slug, integrationID string, req UpdateOutboundBindingPolicyRequest) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID)
	return c.do(ctx, "PATCH", path, req, nil)
}

func (c *Client) PutOutboundCredential(ctx context.Context, integrationID, authorization string) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/credential"
	return c.do(ctx, "PUT", path, PutOutboundCredentialRequest{Authorization: authorization}, nil)
}

func (c *Client) DeleteOutboundCredential(ctx context.Context, integrationID string) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/credential"
	return c.do(ctx, "DELETE", path, nil, nil)
}
