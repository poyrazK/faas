package api

import (
	"context"
	"net/url"
	"strconv"
)

func (c *Client) GetCanaryRouteGate(ctx context.Context, slug string) (CanaryRouteGate, error) {
	var out CanaryRouteGate
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-requirements/gate", nil, &out)
	return out, err
}

func (c *Client) SetCanaryRouteGate(ctx context.Context, slug string, request SetCanaryRouteGateRequest) (CanaryRouteGate, error) {
	var out CanaryRouteGate
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/route-requirements/gate", request, &out)
	return out, err
}

func (c *Client) GetAutomaticRouteCheck(ctx context.Context, slug, deploymentID string) (AutomaticRouteCheck, error) {
	var out AutomaticRouteCheck
	err := c.do(ctx, "GET", "/v1/apps/"+slug+"/route-requirements/checks/"+deploymentID, nil, &out)
	return out, err
}

func (c *Client) RefreshAutomaticRouteCheck(ctx context.Context, slug, deploymentID string) error {
	return c.do(ctx, "POST", "/v1/apps/"+slug+"/route-requirements/checks/"+deploymentID+"/refresh", nil, nil)
}

func (c *Client) SaveRouteRequirements(ctx context.Context, slug string, request SaveRouteRequirementsRequest) (SavedRouteRequirements, error) {
	var out SavedRouteRequirements
	err := c.do(ctx, "PUT", "/v1/apps/"+slug+"/route-requirements", request, &out)
	return out, err
}

func (c *Client) GetSavedRouteRequirements(ctx context.Context, slug string) (SavedRouteRequirements, error) {
	var out SavedRouteRequirements
	err := c.do(ctx, "GET", "/v1/apps/"+slug+"/route-requirements", nil, &out)
	return out, err
}

func (c *Client) CheckRouteRequirements(ctx context.Context, slug string, request CheckRouteRequirementsRequest) (RouteRequirementsCheck, error) {
	var out RouteRequirementsCheck
	err := c.do(ctx, "POST", "/v1/apps/"+slug+"/route-requirements/check", request, &out)
	return out, err
}

func (c *Client) PlanRoutePolicy(ctx context.Context, slug string, request RoutePolicyPlanRequest) (RoutePolicyPlan, error) {
	var out RoutePolicyPlan
	err := c.do(ctx, "POST", "/v1/apps/"+slug+"/route-policy/plan", request, &out)
	return out, err
}

func (c *Client) ApplyRoutePolicy(ctx context.Context, slug, key string, request RoutePolicyApplyRequest) (RoutePolicyApplyResponse, error) {
	var out RoutePolicyApplyResponse
	err := c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+slug+"/route-policy/apply", request, &out, key)
	return out, err
}

func (c *Client) GetRoutePolicyReceipt(ctx context.Context, slug, id string) (RoutePolicyReceipt, error) {
	var out RoutePolicyReceipt
	err := c.do(ctx, "GET", "/v1/apps/"+slug+"/route-policy/receipts/"+id, nil, &out)
	return out, err
}

func (c *Client) ListRouteCheckHistory(ctx context.Context, slug, deploymentID string, limit int, before string) (RouteCheckHistoryPage, error) {
	var out RouteCheckHistoryPage
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if before != "" {
		query.Set("before", before)
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-requirements/checks/"+url.PathEscape(deploymentID)+"/history?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetRouteCheckHistoryEntry(ctx context.Context, slug, deploymentID, checkID string) (RouteCheckHistoryEntry, error) {
	var out RouteCheckHistoryEntry
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-requirements/checks/"+url.PathEscape(deploymentID)+"/history/"+url.PathEscape(checkID), nil, &out)
	return out, err
}
