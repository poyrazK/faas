package api

import "time"

// Self-service choices are explicit directed transitions from a customer's
// current plan name to a plan in the latest catalog. Omitted/empty is closed.
type ProductPlanPolicy struct {
	Transitions map[string][]string `json:"transitions"`
}

type ProductPlanPolicyConfiguration struct {
	Revision string            `json:"revision"`
	Policy   ProductPlanPolicy `json:"policy"`
}

type ProductPlanPolicyChange struct {
	ExpectedHead     string            `json:"expected_head"`
	ExpectedRevision string            `json:"expected_revision"`
	Policy           ProductPlanPolicy `json:"policy"`
}

type ProductPlanPolicyPreview struct {
	Before ProductPlanPolicyConfiguration `json:"before"`
	After  ProductPlanPolicyConfiguration `json:"after"`
	Change ProductPlanPolicyChange        `json:"change"`
}

// These projections contain commercial terms, never bindings or Auth policy.
type ProductPlanFeatureTerms struct {
	Name                string `json:"name"`
	Label               string `json:"label"`
	MonthlyStarts       int64  `json:"monthly_starts"`
	UnitPriceMillicents int64  `json:"unit_price_millicents"`
	Starts              int64  `json:"starts"`
	RemainingStarts     int64  `json:"remaining_starts"`
	// IncludedStarts come first at no charge (ADR-20261021154511); omitted
	// when zero, so earlier previews hash as before.
	IncludedStarts int64 `json:"included_starts,omitempty"`
}

type ProductPlanTerms struct {
	Plan     string                    `json:"plan"`
	Label    string                    `json:"label"`
	Revision string                    `json:"revision"`
	Currency string                    `json:"currency"`
	Features []ProductPlanFeatureTerms `json:"features"`
}

type ProductPlanOptions struct {
	Current      ProductPlanTerms   `json:"current"`
	Offers       []ProductPlanTerms `json:"offers"`
	Period       string             `json:"period"`
	HeadRevision string             `json:"head_revision"`
	Suspended    bool               `json:"suspended"`
}

type ProductPlanSelection struct {
	ID       string `json:"id"`
	Plan     string `json:"plan"`
	Revision string `json:"revision,omitempty"`
}

type ProductPlanChangePreview struct {
	Request        ProductPlanSelection `json:"request"`
	BeforeCustomer ProductCustomer      `json:"before_customer"`
	AfterCustomer  ProductCustomer      `json:"after_customer"`
	Before         ProductPlanTerms     `json:"before"`
	After          ProductPlanTerms     `json:"after"`
	Period         string               `json:"period"`
	HeadRevision   string               `json:"head_revision"`
	Hash           string               `json:"hash"`
}

type ProductPlanChangeConfirmation struct {
	Request      ProductPlanSelection `json:"request"`
	ExpectedHash string               `json:"expected_hash"`
}

type ProductPlanChangeReceipt struct {
	ID          string                   `json:"id"`
	Preview     ProductPlanChangePreview `json:"preview"`
	ActorRole   string                   `json:"actor_role"`
	EffectiveAt time.Time                `json:"effective_at"`
}

type ProductPlanChanges struct {
	Changes    []ProductPlanChangeReceipt `json:"changes"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}
