package api

import (
	"encoding/json"
	"time"
)

type ProductStartRequest struct {
	ExpectedQuote string          `json:"expected_quote,omitempty"`
	App           string          `json:"app,omitempty"`
	Feature       string          `json:"feature"`
	DefinitionID  string          `json:"definition_id,omitempty"`
	Input         json.RawMessage `json:"input"`
}

// ProductCatalogSpec is the internal Product Control commercial contract.
// Prices are millicents per verified successful logical operation.
type ProductCatalogSpec struct {
	// AuthApp is an account-owned Application Auth app. The latest catalog
	// selects the portal realm; customer assignments still pin their prices.
	AuthApp     string               `json:"auth_app,omitempty"`
	Access      *ProductAccessPolicy `json:"access,omitempty"`
	SelfService *ProductPlanPolicy   `json:"self_service,omitempty"`
	Team        *ProductTeamPolicy   `json:"team,omitempty"`
	// Signup lets visitors create their own workspace through the portal
	// (ADR-20261022100735); omitted when off.
	Signup   *ProductSignupPolicy      `json:"signup,omitempty"`
	Currency string                    `json:"currency"`
	Features map[string]ProductFeature `json:"features"`
	Plans    map[string]ProductPlan    `json:"plans"`
}

type ProductFeature struct {
	Label        string                   `json:"label"`
	DefinitionID string                   `json:"definition_id,omitempty"`
	Operation    *ProductOperationBinding `json:"operation,omitempty"`
}

// ProductOperationBinding follows the named Operation on the sole fully
// serving deployment in this environment. It never changes a commercial revision.
type ProductOperationBinding struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

type ProductResolvedBinding struct {
	InputSchema        json.RawMessage `json:"input_schema,omitempty"`
	Available          bool            `json:"available"`
	Reason             string          `json:"reason,omitempty"`
	DefinitionID       string          `json:"definition_id,omitempty"`
	DefinitionRevision string          `json:"definition_revision,omitempty"`
	DeploymentID       string          `json:"deployment_id,omitempty"`
}

type ProductPlan struct {
	Label    string                         `json:"label"`
	Features map[string]ProductFeatureGrant `json:"features"`
	// MonthlyFeeMillicents is charged once for each UTC month a customer
	// holds the plan (ADR-20261021154511). Omitted when zero, so existing
	// catalog revisions hash as before.
	MonthlyFeeMillicents int64 `json:"monthly_fee_millicents,omitempty"`
	// TrialDays is the free trial a customer starts when first assigned to
	// the plan without an earlier trial (ADR-20261021195901); omitted when
	// zero.
	TrialDays int64 `json:"trial_days,omitempty"`
}

type ProductFeatureGrant struct {
	MonthlyStarts       int64 `json:"monthly_starts"`
	UnitPriceMillicents int64 `json:"unit_price_millicents"`
	// IncludedStarts are the first starts of a UTC month, admitted at price
	// 0; later starts cost UnitPriceMillicents up to MonthlyStarts
	// (ADR-20261021154511). Omitted when zero, so existing catalog
	// revisions and quotes hash as before.
	IncludedStarts int64 `json:"included_starts,omitempty"`
}

// CodeProductPlanPricingUnavailable refuses a catalog with included starts
// while the product_control_plan_pricing_enabled release gate is off
// (ADR-20261021154511).
const CodeProductPlanPricingUnavailable = "product_plan_pricing_unavailable"

// StartPriceMillicents is the price of a start admitted after startsBefore
// starts of the feature this month.
func (g ProductFeatureGrant) StartPriceMillicents(startsBefore int64) int64 {
	if startsBefore < g.IncludedStarts {
		return 0
	}
	return g.UnitPriceMillicents
}

type ProductCatalog struct {
	Revision string             `json:"revision"`
	Spec     ProductCatalogSpec `json:"spec"`
}

type ProductCustomer struct {
	AuthOrganizationID string `json:"auth_organization_id,omitempty"`
	TenantID           string `json:"tenant_id"`
	Revision           string `json:"revision"`
	Plan               string `json:"plan"`
	Suspended          bool   `json:"suspended"`
	// TrialStartsAt and TrialEndsAt are the customer's one free trial in the
	// app, [starts, ends) (ADR-20261021195901). The store sets them; an
	// assignment request cannot. Omitted without a trial, so earlier
	// previews hash as before.
	TrialStartsAt *time.Time `json:"trial_starts_at,omitempty"`
	TrialEndsAt   *time.Time `json:"trial_ends_at,omitempty"`
}

// InTrial reports whether at falls in the customer's trial.
func (c ProductCustomer) InTrial(at time.Time) bool {
	return c.TrialStartsAt != nil && c.TrialEndsAt != nil && !at.Before(*c.TrialStartsAt) && at.Before(*c.TrialEndsAt)
}

// ProductTrialRequest moves a running trial's end (ADR-20261021195901).
type ProductTrialRequest struct {
	EndsAt time.Time `json:"ends_at"`
}

// CodeProductTrialUnavailable refuses changing a trial that is not running.
const CodeProductTrialUnavailable = "product_trial_unavailable"

type ProductCustomersResponse struct {
	Customers  []ProductCustomer `json:"customers"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type ProductUsageLine struct {
	Feature          string `json:"feature"`
	Revision         string `json:"revision"`
	Plan             string `json:"plan"`
	Starts           int64  `json:"starts"`
	Succeeded        int64  `json:"succeeded"`
	AmountMillicents int64  `json:"amount_millicents"`
}

type ProductUsage struct {
	Period      string             `json:"period"`
	Currency    string             `json:"currency"`
	Lines       []ProductUsageLine `json:"lines"`
	Provisional bool               `json:"provisional"`
	// TrialStarts counts each feature's starts during the customer's trial
	// in the period, which use no included starts (ADR-20261021195901).
	TrialStarts map[string]int64 `json:"trial_starts,omitempty"`
}

type ProductPortal struct {
	Guidance   *ProductUsageGuidance             `json:"guidance,omitempty"`
	Access     *ProductCustomerAccess            `json:"access,omitempty"`
	Allowances map[string]int64                  `json:"remaining_starts"`
	AccountID  string                            `json:"account_id"`
	AppID      string                            `json:"app_id"`
	Quotes     map[string]string                 `json:"quotes"`
	Bindings   map[string]ProductResolvedBinding `json:"bindings"`
	Operations []OperationSummary                `json:"operations"`
	Customer   ProductCustomer                   `json:"customer"`
	Catalog    ProductCatalog                    `json:"catalog"`
	Usage      *ProductUsage                     `json:"usage,omitempty"`
	// OverdueHold explains why new starts are refused while an exported
	// invoice is overdue (ADR-20261018212918); separate from Suspended.
	OverdueHold *ProductCustomerOverdueHold `json:"overdue_hold,omitempty"`
	// IncludedRemaining is each grant's included starts left this month,
	// which starts during the trial do not use (ADR-20261021154511,
	// ADR-20261021195901).
	IncludedRemaining map[string]int64 `json:"included_remaining,omitempty"`
}

// ProductOperationReceipt is immutable admission evidence persisted inside an
// operation and in its independently retained commercial receipt.
type ProductOperationReceipt struct {
	Admission           *ProductAdmissionEvidence `json:"admission,omitempty"`
	Revision            string                    `json:"revision"`
	Feature             string                    `json:"feature"`
	Plan                string                    `json:"plan"`
	Currency            string                    `json:"currency"`
	Period              string                    `json:"period"`
	UnitPriceMillicents int64                     `json:"unit_price_millicents"`
	AdmittedAt          time.Time                 `json:"admitted_at"`
}

// ProductAdmissionEvidence captures the binding and allowance decision in the
// admission transaction. Older receipts can legitimately lack this evidence.
type ProductAdmissionEvidence struct {
	ConfirmedQuote     string `json:"confirmed_quote,omitempty"`
	DefinitionID       string `json:"definition_id"`
	DefinitionRevision string `json:"definition_revision"`
	DeploymentID       string `json:"deployment_id"`
	ReleaseID          string `json:"release_id,omitempty"`
	Scope              string `json:"scope"`
	Name               string `json:"name"`
	MonthlyStarts      int64  `json:"monthly_starts"`
	StartsBefore       int64  `json:"starts_before"`
	// IncludedStarts is the grant's included starts when it had any
	// (ADR-20261021154511); the run was included when StartsBefore was
	// below it.
	IncludedStarts int64 `json:"included_starts,omitempty"`
	// Trial marks a start admitted free during the customer's trial
	// (ADR-20261021195901).
	Trial bool `json:"trial,omitempty"`
	// PaidStartsBefore is the month's starts outside the trial before this
	// one, which decide included pricing when the customer has a trial.
	PaidStartsBefore *int64 `json:"paid_starts_before,omitempty"`
}

type ProductReceiptEvidence struct {
	OperationID   string                  `json:"operation_id"`
	TenantID      string                  `json:"tenant_id"`
	Receipt       ProductOperationReceipt `json:"receipt"`
	SucceededAt   *time.Time              `json:"succeeded_at,omitempty"`
	ResolvedState OperationState          `json:"resolved_state,omitempty"`
	ResolvedAt    *time.Time              `json:"resolved_at,omitempty"`
	WaivedAt      *time.Time              `json:"waived_at,omitempty"`
	WaiverReason  string                  `json:"waiver_reason,omitempty"`
}

// ProductOperationExplanation excludes input, result payloads and execution
// credentials. The independent receipt remains available after result expiry.
type ProductOperationExplanation struct {
	AppID               string                   `json:"app_id"`
	Evidence            ProductReceiptEvidence   `json:"evidence"`
	EntitlementDecision string                   `json:"entitlement_decision"`
	OperationRetained   bool                     `json:"operation_retained"`
	Outcome             *ProductExplainedOutcome `json:"outcome,omitempty"`
	Executions          []OperationExecution     `json:"executions"`
	NextGeneration      int                      `json:"next_generation,omitempty"`
	Charge              ProductExplainedCharge   `json:"charge"`
}

type ProductExplainedOutcome struct {
	State              OperationState `json:"state"`
	Generation         int            `json:"generation"`
	ResultAvailable    bool           `json:"result_available"`
	ArtifactCount      int            `json:"artifact_count"`
	CompletionDelivery string         `json:"completion_delivery"`
}

type ProductExplainedCharge struct {
	State            string `json:"state"`
	AmountMillicents int64  `json:"amount_millicents"`
	Provisional      bool   `json:"provisional"`
}
