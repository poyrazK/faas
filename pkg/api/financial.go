package api

import (
	"context"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
)

type FinancialMeterCoverage struct {
	Complete            bool     `json:"complete"`
	Fresh               bool     `json:"fresh"`
	ExpectedMinutes     int64    `json:"expected_minutes"`
	CompleteMinutes     int64    `json:"complete_minutes"`
	UnpricedQuantity    int64    `json:"unpriced_quantity"`
	NonBillableQuantity int64    `json:"non_billable_quantity"`
	Reasons             []string `json:"reasons"`
}

type FinancialMeterCosts struct {
	Meter          string                   `json:"meter"`
	Coverage       FinancialMeterCoverage   `json:"coverage"`
	Accrued        financial.ContractCosts  `json:"accrued"`
	Forecast       financial.Forecast       `json:"forecast"`
	PriceContracts []FinancialPriceContract `json:"price_contracts"`
}

type FinancialPriceContract struct {
	Price         financial.Price `json:"price"`
	Plan          Plan            `json:"plan"`
	EffectiveFrom time.Time       `json:"effective_from"`
	DeliveryMode  string          `json:"delivery_mode"`
}

type FinancialCostsResponse struct {
	AccountID             string                `json:"account_id"`
	Currency              string                `json:"currency"`
	PeriodStart           time.Time             `json:"period_start"`
	PeriodEnd             time.Time             `json:"period_end"`
	AsOf                  time.Time             `json:"as_of"`
	RetainedFrom          time.Time             `json:"retained_from"`
	EvidenceThroughID     int64                 `json:"evidence_through_id"`
	KnownUsageMillicents  int64                 `json:"known_usage_millicents"`
	Meters                []FinancialMeterCosts `json:"meters"`
	Scope                 string                `json:"scope"`
	Invoices              []Invoice             `json:"invoices"`
	InvoiceReconciliation string                `json:"invoice_reconciliation"`
	MissingBillComponents []string              `json:"missing_bill_components"`
}

type FinancialForecastResponse struct {
	PeriodStart           time.Time             `json:"period_start"`
	PeriodEnd             time.Time             `json:"period_end"`
	AsOf                  time.Time             `json:"as_of"`
	Currency              string                `json:"currency"`
	Meters                []FinancialMeterCosts `json:"meters"`
	BillEstimateAvailable bool                  `json:"bill_estimate_available"`
	MissingBillComponents []string              `json:"missing_bill_components"`
}

type FinancialBudgetPreviewRequest struct {
	Spec financial.BudgetSpec `json:"spec"`
}

type FinancialBudgetTarget struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	EnvironmentID string `json:"environment_id,omitempty"`
	DeploymentID  string `json:"deployment_id,omitempty"`
	Effect        string `json:"effect"`
}

type FinancialBudgetPreviewResponse struct {
	Spec              financial.BudgetSpec    `json:"spec"`
	PeriodStart       time.Time               `json:"period_start"`
	PeriodEnd         time.Time               `json:"period_end"`
	AsOf              time.Time               `json:"as_of"`
	KnownMillicents   int64                   `json:"known_millicents"`
	KnownLimitReached bool                    `json:"known_limit_reached"`
	CoverageComplete  bool                    `json:"coverage_complete"`
	Fresh             bool                    `json:"fresh"`
	Reasons           []string                `json:"reasons"`
	EnforcementReady  bool                    `json:"enforcement_ready"`
	Guarantee         string                  `json:"guarantee"`
	Targets           []FinancialBudgetTarget `json:"targets"`
	ContinuingTargets []FinancialBudgetTarget `json:"continuing_targets"`
}

func (c *Client) PreviewFinancialBudget(ctx context.Context, spec financial.BudgetSpec) (FinancialBudgetPreviewResponse, error) {
	var out FinancialBudgetPreviewResponse
	err := c.do(ctx, "POST", "/v1/billing/budgets/preview", FinancialBudgetPreviewRequest{Spec: spec}, &out)
	return out, err
}

func (c *Client) GetFinancialCosts(ctx context.Context, month string) (FinancialCostsResponse, error) {
	var out FinancialCostsResponse
	path := "/v1/billing/costs"
	if month != "" {
		path += "?month=" + url.QueryEscape(month)
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}

func (c *Client) GetFinancialForecast(ctx context.Context, month string) (FinancialForecastResponse, error) {
	var out FinancialForecastResponse
	path := "/v1/billing/forecast"
	if month != "" {
		path += "?month=" + url.QueryEscape(month)
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
