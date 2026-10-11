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

func (c *Client) SetOutboundIntegrationRequestPolicy(ctx context.Context, integrationID string, policy OutboundRequestPolicy) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/request-policy"
	return c.do(ctx, "PUT", path, PutOutboundRequestPolicyRequest{RequestPolicy: policy}, nil)
}

// SetOutboundIntegrationAIPolicy replaces a customer integration's complete AI
// gateway policy (provider, token and cost limits); an empty provider turns
// it off.
func (c *Client) SetOutboundIntegrationAIPolicy(ctx context.Context, integrationID string, policy PutOutboundAIPolicyRequest) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai"
	return c.do(ctx, "PUT", path, policy, nil)
}

// GetOutboundIntegrationAIPrices returns the price list in effect.
func (c *Client) GetOutboundIntegrationAIPrices(ctx context.Context, integrationID string) (OutboundAIPriceListResponse, error) {
	var out OutboundAIPriceListResponse
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-prices"
	return out, c.do(ctx, "GET", path, nil, &out)
}

// SetOutboundIntegrationAIPrices replaces the price list, effective now.
func (c *Client) SetOutboundIntegrationAIPrices(ctx context.Context, integrationID string, entries []OutboundAIPriceEntry) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-prices"
	if entries == nil {
		entries = []OutboundAIPriceEntry{}
	}
	return c.do(ctx, "PUT", path, PutOutboundAIPricesRequest{Entries: entries}, nil)
}

// SetOutboundBindingAIBudget sets or clears one app's daily AI token limit.
func (c *Client) SetOutboundBindingAIBudget(ctx context.Context, slug, integrationID string, dailyTokenLimit *int64) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID) + "/ai-budget"
	return c.do(ctx, "PUT", path, PutOutboundBindingAIBudgetRequest{DailyTokenLimit: dailyTokenLimit}, nil)
}

func (c *Client) ListOutboundAIRoutes(ctx context.Context) (OutboundAIRouteList, error) {
	var out OutboundAIRouteList
	return out, c.do(ctx, "GET", "/v1/outbound/ai-routes", nil, &out)
}

func (c *Client) CreateOutboundAIRoute(ctx context.Context, req CreateOutboundAIRouteRequest) (OutboundAIRoute, error) {
	var out OutboundAIRoute
	return out, c.do(ctx, "POST", "/v1/outbound/ai-routes", req, &out)
}

func (c *Client) GetOutboundAIRoute(ctx context.Context, routeID string) (OutboundAIRoute, error) {
	var out OutboundAIRoute
	return out, c.do(ctx, "GET", "/v1/outbound/ai-routes/"+url.PathEscape(routeID), nil, &out)
}

func (c *Client) SetOutboundAIRouteTargets(ctx context.Context, routeID string, targets []OutboundAIRouteTarget) error {
	return c.do(ctx, "PUT", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/targets", PutOutboundAIRouteTargetsRequest{Targets: targets}, nil)
}

func (c *Client) DeleteOutboundAIRoute(ctx context.Context, routeID string) error {
	return c.do(ctx, "DELETE", "/v1/outbound/ai-routes/"+url.PathEscape(routeID), nil, nil)
}

// GetOutboundIntegrationAIUsage reads one UTC day (YYYY-MM-DD, empty for
// today) of AI usage by caller and model.
func (c *Client) GetOutboundIntegrationAIUsage(ctx context.Context, integrationID, usageDate string) (OutboundAIUsageResponse, error) {
	var out OutboundAIUsageResponse
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-usage"
	if usageDate != "" {
		path += "?date=" + url.QueryEscape(usageDate)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) SetOutboundIntegrationRunsEnabled(ctx context.Context, integrationID string, enabled bool) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/runs"
	return c.do(ctx, "PUT", path, PutOutboundRunsBindingRequest{Enabled: &enabled}, nil)
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

func (c *Client) GetOutboundBindingUsage(ctx context.Context, slug, integrationID string) (OutboundBindingUsageResponse, error) {
	var out OutboundBindingUsageResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID) + "/usage"
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) SetOutboundBindingDailyBudget(ctx context.Context, slug, integrationID string, limit *int64) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID) + "/budget"
	return c.do(ctx, "PUT", path, PutOutboundBindingDailyRequestBudgetRequest{DailyRequestLimit: limit}, nil)
}

func (c *Client) PutOutboundCredential(ctx context.Context, integrationID, authorization string) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/credential"
	return c.do(ctx, "PUT", path, PutOutboundCredentialRequest{Authorization: authorization}, nil)
}

func (c *Client) DeleteOutboundCredential(ctx context.Context, integrationID string) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/credential"
	return c.do(ctx, "DELETE", path, nil, nil)
}
