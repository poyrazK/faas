package api

import (
	"context"
	"net/url"
	"strconv"
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

type FinancialBudgetResponse struct {
	ID               string               `json:"id"`
	AccountID        string               `json:"account_id"`
	Revision         int64                `json:"revision"`
	Spec             financial.BudgetSpec `json:"spec"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
	DeletedAt        *time.Time           `json:"deleted_at,omitempty"`
	Status           string               `json:"status"`
	EnforcementReady bool                 `json:"enforcement_ready"`
	Reasons          []string             `json:"reasons"`
}

type FinancialBudgetListResponse struct {
	Budgets []FinancialBudgetResponse `json:"budgets"`
}

type CreateFinancialBudgetRequest struct {
	Spec financial.BudgetSpec `json:"spec"`
}

type UpdateFinancialBudgetRequest struct {
	ExpectedRevision int64                `json:"expected_revision"`
	Spec             financial.BudgetSpec `json:"spec"`
}

type DeleteFinancialBudgetRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type FinancialBudgetRevisionResponse struct {
	PolicyID   string               `json:"policy_id"`
	Revision   int64                `json:"revision"`
	Actor      string               `json:"actor"`
	Mutation   string               `json:"mutation"`
	Spec       financial.BudgetSpec `json:"spec"`
	RecordedAt time.Time            `json:"recorded_at"`
}

type FinancialBudgetHistoryResponse struct {
	Revisions    []FinancialBudgetRevisionResponse `json:"revisions"`
	NextRevision int64                             `json:"next_revision,omitempty"`
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

func (c *Client) ListFinancialBudgets(ctx context.Context) (FinancialBudgetListResponse, error) {
	var out FinancialBudgetListResponse
	err := c.do(ctx, "GET", "/v1/billing/budgets", nil, &out)
	return out, err
}

func (c *Client) GetFinancialBudget(ctx context.Context, id string) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, "GET", "/v1/billing/budgets/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) CreateFinancialBudget(ctx context.Context, req CreateFinancialBudgetRequest) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, "POST", "/v1/billing/budgets", req, &out)
	return out, err
}

func (c *Client) UpdateFinancialBudget(ctx context.Context, id string, req UpdateFinancialBudgetRequest) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, "PUT", "/v1/billing/budgets/"+url.PathEscape(id), req, &out)
	return out, err
}

func (c *Client) DeleteFinancialBudget(ctx context.Context, id string, expectedRevision int64) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, "DELETE", "/v1/billing/budgets/"+url.PathEscape(id), DeleteFinancialBudgetRequest{ExpectedRevision: expectedRevision}, &out)
	return out, err
}

func (c *Client) ListFinancialBudgetRevisions(ctx context.Context, id string, after int64, limit int) (FinancialBudgetHistoryResponse, error) {
	var out FinancialBudgetHistoryResponse
	q := url.Values{}
	q.Set("after_revision", strconv.FormatInt(after, 10))
	q.Set("limit", strconv.Itoa(limit))
	err := c.do(ctx, "GET", "/v1/billing/budgets/"+url.PathEscape(id)+"/revisions?"+q.Encode(), nil, &out)
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
