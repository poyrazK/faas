package api

import (
	"context"
	"net/url"
)

// EnvironmentRouteBudgets is advisory intent for exactly one registered scope.
// Revision belongs to the override (including explicit inheritance), while
// AppRevision pins the parent policy. Config never includes customer grouping
// or recovery actions, even when inherited from the production monitor.
type EnvironmentRouteBudgets struct {
	AppID         string             `json:"app_id"`
	Scope         string             `json:"scope"`
	ProjectID     string             `json:"project_id,omitempty"`
	EnvironmentID string             `json:"environment_id,omitempty"`
	BudgetSource  string             `json:"budget_source"`
	Revision      int64              `json:"revision"`
	AppRevision   int64              `json:"app_revision"`
	Config        RouteMonitorConfig `json:"config"`
}

type SetEnvironmentRouteBudgetsRequest struct {
	AppID               string              `json:"app_id"`
	ProjectID           string              `json:"project_id,omitempty"`
	EnvironmentID       string              `json:"environment_id,omitempty"`
	RequestID           string              `json:"request_id"`
	ExpectedRevision    *int64              `json:"expected_revision"`
	ExpectedAppRevision *int64              `json:"expected_app_revision"`
	Inherit             bool                `json:"inherit"`
	Enabled             bool                `json:"enabled"`
	Routes              []RouteMonitorRoute `json:"routes"`
}

func environmentRouteBudgetsPath(slug, environment string) string {
	path := "/v1/apps/" + url.PathEscape(slug) + "/route-budgets"
	if environment == "" {
		return path + "?scope=default"
	}
	return path + "?environment=" + url.QueryEscape(environment)
}
func (c *Client) GetEnvironmentRouteBudgets(ctx context.Context, slug, environment string) (EnvironmentRouteBudgets, error) {
	var out EnvironmentRouteBudgets
	err := c.do(ctx, "GET", environmentRouteBudgetsPath(slug, environment), nil, &out)
	return out, err
}
func (c *Client) SetEnvironmentRouteBudgets(ctx context.Context, slug, environment string, req SetEnvironmentRouteBudgetsRequest) (EnvironmentRouteBudgets, error) {
	var out EnvironmentRouteBudgets
	err := c.doWithIdempotencyKey(ctx, "PUT", environmentRouteBudgetsPath(slug, environment), req, &out, req.RequestID)
	return out, err
}
