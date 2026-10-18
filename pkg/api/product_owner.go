package api

import "time"

// Customer changes affect future admissions immediately. Identity mappings are
// preserved for existing assignments; accepted work keeps its original terms.
type ProductCustomerChangePreview struct {
	Before        *ProductCustomer `json:"before,omitempty"`
	After         ProductCustomer  `json:"after"`
	BeforeCatalog *ProductCatalog  `json:"before_catalog,omitempty"`
	Catalog       ProductCatalog   `json:"catalog"`
	HeadRevision  string           `json:"head_revision"`
	Period        string           `json:"period"`
	Hash          string           `json:"hash"`
}

type ProductCustomerChangeRequest struct {
	ID           string          `json:"id"`
	ExpectedHash string          `json:"expected_hash"`
	Customer     ProductCustomer `json:"customer"`
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
