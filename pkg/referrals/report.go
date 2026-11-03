package referrals

import (
	"context"
	"errors"
	"time"
)

var ErrReportAccess = errors.New("sales manager scope required")

// ReportFilter selects creation cohorts. Outcomes are observed at read time.
type ReportFilter struct {
	From, To, Timezone string // inclusive local calendar dates
	Owner              string
	MemberEmails       []string
}
type LeadMetrics struct {
	Total     int64 `json:"total"`
	New       int64 `json:"new"`
	Contacted int64 `json:"contacted"`
	Qualified int64 `json:"qualified"`
	Demo      int64 `json:"demo"`
	Won       int64 `json:"won"`
	Lost      int64 `json:"lost"`
}
type ReportRep struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	CurrentMember bool   `json:"current_member"`
}
type RepComparison struct {
	ReportRep
	Leads                  LeadMetrics `json:"leads"`
	Signups                int64       `json:"signups"`
	FirstPayments          int64       `json:"first_payments"`
	AvgFirstPaymentSeconds *int64      `json:"avg_first_payment_seconds"`
	MissingInvoices        int64       `json:"missing_invoices"`
}
type FirstInvoiceCollections struct {
	OwnerID            string `json:"owner_id"`
	Currency           string `json:"currency"`
	Invoices           int64  `json:"invoices"`
	PaidCents          int64  `json:"paid_cents,string"`
	RefundedCents      int64  `json:"refunded_cents,string"`
	PendingRefundCents int64  `json:"pending_refund_cents,string"`
	RetainedCents      int64  `json:"retained_cents,string"`
}
type ManagerReport struct {
	Reps        []ReportRep               `json:"reps"`
	Rows        []RepComparison           `json:"rows"`
	Collections []FirstInvoiceCollections `json:"collections"`
	ObservedAt  time.Time                 `json:"observed_at"`
}
type ReportingStore interface {
	ManagerReport(context.Context, Scope, ReportFilter) (ManagerReport, error)
}
