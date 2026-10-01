package focus

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// LimitError means the complete artifact cannot fit its documented bound.
type LimitError struct {
	Kind            string
	Limit, Observed int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("FOCUS export exceeds %d %s", e.Limit, e.Kind)
}

func projectInvoiceRows(inv state.Invoice) ([][]string, error) {
	records := state.InvoiceExportRecords(inv)
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		if inv.Lifecycle != nil {
			if history, ok := inv.Lifecycle.Records[record.ID()]; ok {
				if history.Fingerprint != record.Fingerprint() {
					return nil, fmt.Errorf("invoice record lifecycle is stale")
				}
				record = record.WithLifecycle(history.CreatedAt, history.UpdatedAt)
			}
		}
		created, err := time.Parse(time.RFC3339Nano, record.CreatedAt())
		if err != nil || !validDate(created) {
			return nil, fmt.Errorf("invalid invoice record creation date")
		}
		updated, err := time.Parse(time.RFC3339Nano, record.UpdatedAt())
		if err != nil || !validDate(updated) || updated.Before(created) {
			return nil, fmt.Errorf("invalid invoice record update date")
		}
		rows = append(rows, record.WithLifecycle(date(created), date(updated)).Values())
	}
	return rows, nil
}

type SourceCoverage struct {
	IssuedInvoices            int            `json:"IssuedInvoices"`
	DetailedInvoices          int            `json:"DetailedInvoices"`
	AggregateFallbackReasons  map[string]int `json:"AggregateFallbackReasons"`
	MissingPaymentTerms       int            `json:"MissingPaymentTerms"`
	MissingIssuerName         int            `json:"MissingIssuerName"`
	MissingIssueDate          int            `json:"MissingIssueDate"`
	MissingDueDate            int            `json:"MissingDueDate"`
	UntrackedLifecycleRecords int            `json:"UntrackedLifecycleRecords"`
	LegacyLifecycleRecords    int            `json:"LegacyLifecycleRecords"`
}

func invoiceCoverage(invoices []state.Invoice) SourceCoverage {
	c := SourceCoverage{AggregateFallbackReasons: make(map[string]int)}
	for _, inv := range invoices {
		if inv.Status == "draft" || inv.Status == "void" {
			continue
		}
		c.IssuedInvoices++
		for _, record := range state.InvoiceExportRecords(inv) {
			if inv.Lifecycle == nil {
				c.UntrackedLifecycleRecords++
			} else if history, ok := inv.Lifecycle.Records[record.ID()]; !ok {
				c.UntrackedLifecycleRecords++
			} else if history.LegacyCreated {
				c.LegacyLifecycleRecords++
			}
		}
		if reason := state.InvoiceLineGap(inv); reason == "" {
			c.DetailedInvoices++
		} else {
			c.AggregateFallbackReasons[reason]++
		}
		d := inv.Details
		if d == nil {
			d = new(state.InvoiceDetails)
		}
		if d.PaymentTerms == "" {
			c.MissingPaymentTerms++
		}
		if d.IssuerName == "" {
			c.MissingIssuerName++
		}
		if d.IssuedAt == "" {
			c.MissingIssueDate++
		}
		if d.DueAt == "" {
			c.MissingDueDate++
		}
	}
	return c
}

func coverageLimitations(c SourceCoverage) []string {
	limits := []string{
		"Conditional payment-currency and purchase-order data are unavailable; those columns are omitted.",
		"Refunds and credit notes are not separate invoice documents; amounts reconcile to stored invoice totals, which providers may adjust.",
		"Only locally persisted invoices are included; this is not a provider reconciliation or completeness guarantee.",
		"Full FOCUS conformance is not claimed; per-resource cost allocation and correction-document lineage remain unavailable.",
	}
	if c.UntrackedLifecycleRecords > 0 || c.LegacyLifecycleRecords > 0 {
		limits = append(limits, "Historical record creation times cannot be recovered for untracked or legacy records; local observations and lifecycle coverage counts describe this gap.")
	}
	if c.MissingPaymentTerms > 0 {
		limits = append(limits, "PaymentTerms is empty where corresponding invoice terms are unavailable.")
	}
	if c.DetailedInvoices < c.IssuedInvoices {
		limits = append(limits, "Incomplete, unclassified, or unreconciled line snapshots use invoice aggregates; non-tax ChargeCategory defaults to Usage. See AggregateFallbackReasons.")
	}
	if c.MissingIssueDate > 0 || c.MissingDueDate > 0 {
		limits = append(limits, "Issue/due dates are empty where provider invoice dates are unavailable; local creation timestamps never substitute.")
	}
	if c.MissingIssuerName > 0 {
		limits = append(limits, "Issuer names fall back to merchant brands where an invoice issuer name is unavailable; exact legal identity is not guaranteed.")
	}
	return limits
}
