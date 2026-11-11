// adr: 20261103211147
package api

import (
	"context"
	"net/url"
	"time"
)

// AppOperationCheck is a policy observation, never an admission reservation.
type AppOperationCheck struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type AppOperationPreview struct {
	SchemaVersion  int                          `json:"schema_version"`
	AppID          string                       `json:"app_id"`
	Scope          string                       `json:"scope"`
	ProjectID      string                       `json:"project_id,omitempty"`
	EnvironmentID  string                       `json:"environment_id,omitempty"`
	Operation      string                       `json:"operation"`
	Authorization  string                       `json:"authorization"`
	Admission      string                       `json:"admission"`
	RequiredScopes []string                     `json:"required_scopes"`
	Checks         map[string]AppOperationCheck `json:"checks"`
	CheckedAt      time.Time                    `json:"checked_at"`
}

func ValidAppOperation(operation string) bool {
	switch operation {
	case "deploy", "restart", "scale", "env_change":
		return true
	}
	return false
}

func (c *Client) GetAppOperationPreview(ctx context.Context, slug, operation, environment string) (AppOperationPreview, error) {
	q := url.Values{"operation": {operation}}
	if environment != "" {
		q.Set("environment", environment)
	}
	var out AppOperationPreview
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/operation-preview?"+q.Encode(), nil, &out)
}

// AppCostCoverage reports source sampling windows without exposing quantities
// attributed to other apps. Quantity/price coverage for one app remains unknown.
type AppCostCoverage struct {
	Complete              bool     `json:"complete"`
	Fresh                 bool     `json:"fresh"`
	ExpectedMinutes       int64    `json:"expected_minutes"`
	CompleteMinutes       int64    `json:"complete_minutes"`
	QuantityCoverageKnown bool     `json:"quantity_coverage_known"`
	Reasons               []string `json:"reasons"`
}

type AppCostMeter struct {
	Meter         string                       `json:"meter"`
	Coverage      AppCostCoverage              `json:"coverage"`
	Unit          string                       `json:"unit,omitempty"`
	Quantity      int64                        `json:"quantity"`
	NetMillicents int64                        `json:"net_millicents"`
	Allocations   []FinancialAppCostAllocation `json:"allocations"`
}

// AppCostsResponse deliberately excludes account totals, forecasts, invoices
// and account-wide unpriced/nonbillable quantities.
type AppCostsResponse struct {
	AppID                 string         `json:"app_id"`
	AppSlug               string         `json:"app_slug"`
	Currency              string         `json:"currency"`
	PeriodStart           time.Time      `json:"period_start"`
	PeriodEnd             time.Time      `json:"period_end"`
	AsOf                  time.Time      `json:"as_of"`
	KnownUsageMillicents  int64          `json:"known_usage_millicents"`
	Meters                []AppCostMeter `json:"meters"`
	Scope                 string         `json:"scope"`
	ScopeDescription      string         `json:"scope_description"`
	BillEstimateAvailable bool           `json:"bill_estimate_available"`
	MissingBillComponents []string       `json:"missing_bill_components"`
}

func (c *Client) GetAppCosts(ctx context.Context, slug, month string) (AppCostsResponse, error) {
	path := "/v1/apps/" + url.PathEscape(slug) + "/costs"
	if month != "" {
		path += "?" + url.Values{"month": {month}}.Encode()
	}
	var out AppCostsResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}
