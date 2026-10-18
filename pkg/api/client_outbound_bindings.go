package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
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

// SetOutboundAIContentPolicy replaces an integration's content guardrails
// (ADR-997).
func (c *Client) SetOutboundAIContentPolicy(ctx context.Context, integrationID string, policy OutboundAIContentPolicy) error {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-content-policy"
	return c.do(ctx, "PUT", path, policy, nil)
}

// CreateOutboundAIReplay replays a captured call on targets (ADR-997).
func (c *Client) CreateOutboundAIReplay(ctx context.Context, integrationID, requestID string, req CreateOutboundAIReplayRequest) (OutboundAIReplay, error) {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-requests/" + url.PathEscape(requestID) + "/replays"
	var out OutboundAIReplay
	return out, c.do(ctx, "POST", path, req, &out)
}

// GetOutboundAIReplay reads a replay and its results.
func (c *Client) GetOutboundAIReplay(ctx context.Context, replayID string) (OutboundAIReplay, error) {
	var out OutboundAIReplay
	return out, c.do(ctx, "GET", "/v1/outbound/ai-replays/"+url.PathEscape(replayID), nil, &out)
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

// SetOutboundBindingAIGuardrails replaces one app's AI guardrails (ADR-991).
func (c *Client) SetOutboundBindingAIGuardrails(ctx context.Context, slug, integrationID string, req PutOutboundBindingAIGuardrailsRequest) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/outbound-bindings/" + url.PathEscape(integrationID) + "/ai-guardrails"
	return c.do(ctx, "PUT", path, req, nil)
}

func (c *Client) ListOutboundCredentialKeys(ctx context.Context, integrationID string) (OutboundCredentialKeyList, error) {
	var out OutboundCredentialKeyList
	return out, c.do(ctx, "GET", "/v1/outbound/integrations/"+url.PathEscape(integrationID)+"/credential-keys", nil, &out)
}

func (c *Client) CreateOutboundCredentialKey(ctx context.Context, integrationID string, req CreateOutboundCredentialKeyRequest) (OutboundCredentialKey, error) {
	var out OutboundCredentialKey
	return out, c.do(ctx, "POST", "/v1/outbound/integrations/"+url.PathEscape(integrationID)+"/credential-keys", req, &out)
}

func (c *Client) DeleteOutboundCredentialKey(ctx context.Context, integrationID, keyID string) error {
	return c.do(ctx, "DELETE", "/v1/outbound/integrations/"+url.PathEscape(integrationID)+"/credential-keys/"+url.PathEscape(keyID), nil, nil)
}

func (c *Client) ListOutboundAIBudgetAlerts(ctx context.Context, integrationID string, limit int) (OutboundAIBudgetAlertList, error) {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-alerts"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out OutboundAIBudgetAlertList
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListOutboundAIRequestLog pages the AI request log; pass the previous
// page's NextBefore to continue.
func (c *Client) ListOutboundAIRequestLog(ctx context.Context, integrationID string, limit int, before *time.Time) (OutboundAIRequestLogList, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if before != nil {
		query.Set("before", before.UTC().Format(time.RFC3339Nano))
	}
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-requests"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out OutboundAIRequestLogList
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetOutboundAIRequestContent reads one logged call's captured bodies
// (ADR-996).
func (c *Client) GetOutboundAIRequestContent(ctx context.Context, integrationID, requestID string) (OutboundAIRequestContent, error) {
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-requests/" + url.PathEscape(requestID) + "/content"
	var out OutboundAIRequestContent
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetOutboundAIPriceCatalog lists a provider's built-in prices, or with a
// model the entry that would price it (ADR-994).
func (c *Client) GetOutboundAIPriceCatalog(ctx context.Context, provider, model string) (OutboundAIPriceCatalogResponse, error) {
	query := url.Values{"provider": {provider}}
	if model != "" {
		query.Set("model", model)
	}
	var out OutboundAIPriceCatalogResponse
	return out, c.do(ctx, "GET", "/v1/outbound/ai-price-catalog?"+query.Encode(), nil, &out)
}

// GetOutboundAIUserUsage lists one UTC day's AI usage by end user; an empty
// date is today and an empty endUser lists the top users (ADR-994).
// GetOutboundAITagUsage reads one UTC day's usage by tag, optionally for
// one tag key (ADR-1436).
func (c *Client) GetOutboundAITagUsage(ctx context.Context, integrationID, date, key string, limit int) (OutboundAITagUsageResponse, error) {
	query := url.Values{}
	if date != "" {
		query.Set("date", date)
	}
	if key != "" {
		query.Set("key", key)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-usage/tags"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out OutboundAITagUsageResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ExportOutboundAITagUsage returns a day range of usage by tag as NDJSON
// or CSV, optionally for one key (ADR-1438).
func (c *Client) ExportOutboundAITagUsage(ctx context.Context, integrationID, from, to, key, format string) ([]byte, error) {
	query := url.Values{}
	for name, value := range map[string]string{"from": from, "to": to, "key": key, "format": format} {
		if value != "" {
			query.Set(name, value)
		}
	}
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-usage/tags/export"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []byte
	return out, c.doBytes(ctx, "GET", path, nil, &out)
}

// GetOutboundAITagLimits reads an integration's tag limits (ADR-1436).
func (c *Client) GetOutboundAITagLimits(ctx context.Context, integrationID string) (OutboundAITagLimits, error) {
	var out OutboundAITagLimits
	return out, c.do(ctx, "GET", "/v1/outbound/integrations/"+url.PathEscape(integrationID)+"/ai-tag-limits", nil, &out)
}

// SetOutboundAITagLimits replaces an integration's tag limits; an empty
// list removes them (ADR-1436).
func (c *Client) SetOutboundAITagLimits(ctx context.Context, integrationID string, limits []OutboundAITagLimit) error {
	if limits == nil {
		limits = []OutboundAITagLimit{}
	}
	return c.do(ctx, "PUT", "/v1/outbound/integrations/"+url.PathEscape(integrationID)+"/ai-tag-limits",
		PutOutboundAITagLimitsRequest{Limits: limits}, nil)
}

func (c *Client) GetOutboundAIUserUsage(ctx context.Context, integrationID, date, endUser string, limit int) (OutboundAIUserUsageResponse, error) {
	query := url.Values{}
	if date != "" {
		query.Set("date", date)
	}
	if endUser != "" {
		query.Set("end_user", endUser)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-usage/users"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out OutboundAIUserUsageResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ExportOutboundAIUserUsage downloads the per-end-user usage of the UTC
// days from through to (empty means today) as "csv" or "ndjson" (the
// server default) (ADR-995).
func (c *Client) ExportOutboundAIUserUsage(ctx context.Context, integrationID, from, to, format string) ([]byte, error) {
	query := url.Values{}
	for name, value := range map[string]string{"from": from, "to": to, "format": format} {
		if value != "" {
			query.Set(name, value)
		}
	}
	path := "/v1/outbound/integrations/" + url.PathEscape(integrationID) + "/ai-usage/users/export"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []byte
	return out, c.doBytes(ctx, "GET", path, nil, &out)
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
	return c.SetOutboundAIRouteRouting(ctx, routeID, PutOutboundAIRouteTargetsRequest{Targets: targets})
}

// SetOutboundAIRouteRouting replaces a route's targets and its strategy
// (ADR-1434).
func (c *Client) SetOutboundAIRouteRouting(ctx context.Context, routeID string, req PutOutboundAIRouteTargetsRequest) error {
	return c.do(ctx, "PUT", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/targets", req, nil)
}

func (c *Client) DeleteOutboundAIRoute(ctx context.Context, routeID string) error {
	return c.do(ctx, "DELETE", "/v1/outbound/ai-routes/"+url.PathEscape(routeID), nil, nil)
}

// SetOutboundAIRouteCanary starts or replaces a route's model canary
// (ADR-1340).
func (c *Client) SetOutboundAIRouteCanary(ctx context.Context, routeID string, req PutOutboundAIRouteCanaryRequest) (OutboundAIRouteCanary, error) {
	var out OutboundAIRouteCanary
	return out, c.do(ctx, "PUT", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/canary", req, &out)
}

// GetOutboundAIRouteCanary returns a route's canary with both targets'
// counts over its window.
func (c *Client) GetOutboundAIRouteCanary(ctx context.Context, routeID string) (OutboundAIRouteCanaryStatus, error) {
	var out OutboundAIRouteCanaryStatus
	return out, c.do(ctx, "GET", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/canary", nil, &out)
}

// PromoteOutboundAIRouteCanary makes the canary target the route's first
// target and ends the canary.
func (c *Client) PromoteOutboundAIRouteCanary(ctx context.Context, routeID string) (OutboundAIRoute, error) {
	var out OutboundAIRoute
	return out, c.do(ctx, "POST", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/canary/promote", nil, &out)
}

// CreateOutboundAIEvaluation evaluates a route target against the route's
// first target (ADR-1431). The evaluation runs later; read it back with
// GetOutboundAIEvaluation.
func (c *Client) CreateOutboundAIEvaluation(ctx context.Context, routeID string, req CreateOutboundAIEvaluationRequest) (OutboundAIEvaluation, error) {
	var out OutboundAIEvaluation
	return out, c.do(ctx, "POST", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/evaluations", req, &out)
}

// ListOutboundAIEvaluations lists a route's latest evaluations.
func (c *Client) ListOutboundAIEvaluations(ctx context.Context, routeID string) (OutboundAIEvaluationList, error) {
	var out OutboundAIEvaluationList
	return out, c.do(ctx, "GET", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/evaluations", nil, &out)
}

// GetOutboundAIEvaluation reads an evaluation with its samples.
func (c *Client) GetOutboundAIEvaluation(ctx context.Context, evaluationID string) (OutboundAIEvaluation, error) {
	var out OutboundAIEvaluation
	return out, c.do(ctx, "GET", "/v1/outbound/ai-evaluations/"+url.PathEscape(evaluationID), nil, &out)
}

// CreateOutboundAIAccessToken issues an AI gateway access token for an app
// (ADR-1431). The secret is returned only here.
func (c *Client) CreateOutboundAIAccessToken(ctx context.Context, appSlug string, req CreateOutboundAIAccessTokenRequest) (CreatedOutboundAIAccessToken, error) {
	var out CreatedOutboundAIAccessToken
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(appSlug)+"/ai-access-tokens", req, &out)
}

// ListOutboundAIAccessTokens lists an app's AI gateway access tokens.
func (c *Client) ListOutboundAIAccessTokens(ctx context.Context, appSlug string) (OutboundAIAccessTokenList, error) {
	var out OutboundAIAccessTokenList
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(appSlug)+"/ai-access-tokens", nil, &out)
}

// RevokeOutboundAIAccessToken revokes one of an app's access tokens.
func (c *Client) RevokeOutboundAIAccessToken(ctx context.Context, appSlug, tokenID string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(appSlug)+"/ai-access-tokens/"+url.PathEscape(tokenID), nil, nil)
}

// GetOutboundAIRouteStats returns each route target's settled attempts
// over the window latency and cost routes judge (ADR-1434).
func (c *Client) GetOutboundAIRouteStats(ctx context.Context, routeID string) (OutboundAIRouteStats, error) {
	var out OutboundAIRouteStats
	return out, c.do(ctx, "GET", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/stats", nil, &out)
}

// DeleteOutboundAIRouteCanary ends a route's canary.
func (c *Client) DeleteOutboundAIRouteCanary(ctx context.Context, routeID string) error {
	return c.do(ctx, "DELETE", "/v1/outbound/ai-routes/"+url.PathEscape(routeID)+"/canary", nil, nil)
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
