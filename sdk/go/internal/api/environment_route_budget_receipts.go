package api

import (
	"context"
	"net/url"
)

// EnvironmentRouteBudgetReceipt is original committed policy intent, not current
// configuration or runtime health. RequestHash binds the exact original request.
type EnvironmentRouteBudgetReceipt struct {
	RequestID     string `json:"request_id"`
	RequestHash   string `json:"request_hash"`
	AppID         string `json:"app_id"`
	Scope         string `json:"scope"`
	ProjectID     string `json:"project_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Revision      int64  `json:"revision"`
	AppRevision   int64  `json:"app_revision"`
	BudgetSource  string `json:"budget_source"`
}

// GetEnvironmentRouteBudgetReceipt reads historical intent without replaying a
// mutation. Omit environment for default scope. Missing evidence is not non-commit.
func (c *Client) GetEnvironmentRouteBudgetReceipt(ctx context.Context, slug, environment, requestID string) (EnvironmentRouteBudgetReceipt, error) {
	path := "/v1/apps/" + url.PathEscape(slug) + "/route-budgets/receipts/" + url.PathEscape(requestID)
	if environment == "" {
		path += "?scope=default"
	} else {
		path += "?environment=" + url.QueryEscape(environment)
	}
	var out EnvironmentRouteBudgetReceipt
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
