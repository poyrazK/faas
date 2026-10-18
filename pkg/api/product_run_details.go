package api

import "time"

// ProductRunDetails is a customer projection of retained commercial evidence.
// It contains no input, result, execution diagnostics, owner notes or review.
type ProductRunDetails struct {
	AccountID      string               `json:"account_id"`
	AppID          string               `json:"app_id"`
	TenantID       string               `json:"tenant_id"`
	OperationID    string               `json:"operation_id"`
	Feature        string               `json:"feature"`
	Plan           string               `json:"plan"`
	Period         string               `json:"period"`
	AdmittedAt     time.Time            `json:"admitted_at"`
	ObservedAt     time.Time            `json:"observed_at"`
	StartsConsumed int64                `json:"starts_consumed"`
	Allowance      *ProductRunAllowance `json:"allowance,omitempty"`
	Outcome        ProductRunOutcome    `json:"outcome"`
	Charge         *ProductRunCharge    `json:"charge,omitempty"`
}

// Allowance describes admission, never the workspace's current balance. Older
// receipts legitimately lack this evidence; their allowance is omitted.
type ProductRunAllowance struct {
	MonthlyStarts int64 `json:"monthly_starts"`
	StartsBefore  int64 `json:"starts_before"`
	StartsAfter   int64 `json:"starts_after"`
}

type ProductRunOutcome struct {
	State            string     `json:"state"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	DetailsAvailable bool       `json:"details_available"`
	ResultAvailable  bool       `json:"result_available"`
}

// Charge is absent without current ViewBilling permission. Unknown charges
// omit amounts rather than asserting zero. Credits retain their negative sign.
// Neither finalization nor a credit establishes payment or a cash refund.
type ProductRunCharge struct {
	State               string               `json:"state"`
	Currency            string               `json:"currency"`
	UnitPriceMillicents int64                `json:"unit_price_millicents"`
	AmountMillicents    *int64               `json:"amount_millicents,omitempty"`
	CreditMillicents    int64                `json:"credit_millicents"`
	CreditCount         int64                `json:"credit_count"`
	NetAmountMillicents *int64               `json:"net_amount_millicents,omitempty"`
	Statement           *ProductRunStatement `json:"statement,omitempty"`
}

type ProductRunStatement struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}
