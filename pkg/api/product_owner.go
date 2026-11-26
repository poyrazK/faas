package api

import "time"

// Customer changes affect future admissions from their effective time. Existing
// identity mappings are preserved, and accepted work keeps its original terms.
type ProductCustomerChangePreview struct {
	Before            *ProductCustomer    `json:"before,omitempty"`
	After             ProductCustomer     `json:"after"`
	BeforeCatalog     *ProductCatalog     `json:"before_catalog,omitempty"`
	Catalog           ProductCatalog      `json:"catalog"`
	HeadRevision      string              `json:"head_revision"`
	Period            string              `json:"period"`
	Hash              string              `json:"hash"`
	Pricing           *ProductPlanPricing `json:"pricing,omitempty"`
	StartsNextMonthAt *time.Time          `json:"starts_next_month_at,omitempty"`
}

type ProductCustomerChangeRequest struct {
	ID           string          `json:"id"`
	ExpectedHash string          `json:"expected_hash"`
	Customer     ProductCustomer `json:"customer"`
	NextMonth    bool            `json:"next_month,omitempty"`
}

type ProductPlanSchedule struct {
	ProductCustomerChangeReceipt
	Status string `json:"status"`
}

type ProductCustomerChangeReceipt struct {
	ID          string           `json:"id"`
	PreviewHash string           `json:"preview_hash"`
	Before      *ProductCustomer `json:"before,omitempty"`
	After       ProductCustomer  `json:"after"`
	EffectiveAt time.Time        `json:"effective_at"`
}

type ProductOwnerRun struct {
	Evidence           ProductReceiptEvidence `json:"evidence"`
	State              OperationState         `json:"state,omitempty"`
	CreditedMillicents int64                  `json:"credited_millicents"`
}

type ProductOwnerRuns struct {
	Runs       []ProductOwnerRun `json:"runs"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type ProductOwnerAttention struct {
	TenantID    string `json:"tenant_id"`
	Period      string `json:"period"`
	Unresolved  int64  `json:"unresolved"`
	NeedsReview int64  `json:"needs_review"`
}

type ProductOwnerAttentionPage struct {
	Items      []ProductOwnerAttention `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}
