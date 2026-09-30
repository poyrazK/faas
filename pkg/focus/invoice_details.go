package focus

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
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

func projectInvoiceRows(inv state.Invoice, issuer, code string) ([][]string, error) {
	if state.InvoiceLineGap(inv) != "" {
		rows := [][]string{invoiceRow(inv, issuer, code, "Usage", inv.TotalCents-inv.TaxCents)}
		if inv.TaxCents != 0 {
			rows = append(rows, invoiceRow(inv, issuer, code, "Tax", inv.TaxCents))
		}
		for _, row := range rows {
			applyInvoiceFacts(row, inv.Details)
		}
		return rows, nil
	}
	items := append([]state.InvoiceLineItem(nil), inv.Details.Lines.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	rows := make([][]string, 0, len(items)*2)
	for _, item := range items {
		created, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
		if err != nil || !validDate(created) {
			return nil, fmt.Errorf("invalid invoice line creation date")
		}
		updated, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
		if err != nil || !validDate(updated) || updated.Before(created) {
			return nil, fmt.Errorf("invalid invoice line update date")
		}
		row := providerLineRow(inv, issuer, code, item, "charges", item.ChargeCategory, item.NetCents)
		rows = append(rows, row)
		if item.TaxCents != 0 {
			rows = append(rows, providerLineRow(inv, issuer, code, item, "tax", "Tax", item.TaxCents))
		}
	}
	return rows, nil
}

func providerLineRow(inv state.Invoice, issuer, code string, item state.InvoiceLineItem, component, category string, cents int64) []string {
	row := invoiceRow(inv, issuer, code, category, cents)
	row[6], row[10] = canonicalDate(item.CreatedAt), canonicalDate(item.UpdatedAt)
	row[7] = item.Description
	if row[7] == "" {
		row[7] = "Provider invoice line item"
	}
	grain, _ := json.Marshal(map[string]string{"x_GregaleProviderLineId": item.ID, "x_GregaleComponent": component})
	row[8] = string(grain)
	row[9] = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:invoice-detail:"+inv.ID+":"+item.ID+":"+component)).String()
	applyInvoiceFacts(row, inv.Details)
	return row
}

func applyInvoiceFacts(row []string, d *state.InvoiceDetails) {
	if d == nil {
		return
	}
	row[12], row[15], row[16] = canonicalDate(d.IssuedAt), canonicalDate(d.DueAt), d.PaymentTerms
}

func canonicalDate(value string) string {
	if value == "" {
		return ""
	}
	t, _ := time.Parse(time.RFC3339Nano, value) // state validation precedes projection
	return date(t)
}

type SourceCoverage struct {
	IssuedInvoices           int            `json:"IssuedInvoices"`
	DetailedInvoices         int            `json:"DetailedInvoices"`
	AggregateFallbackReasons map[string]int `json:"AggregateFallbackReasons"`
	MissingPaymentTerms      int            `json:"MissingPaymentTerms"`
	MissingIssuerName        int            `json:"MissingIssuerName"`
	MissingIssueDate         int            `json:"MissingIssueDate"`
	MissingDueDate           int            `json:"MissingDueDate"`
}

func invoiceCoverage(invoices []state.Invoice) SourceCoverage {
	c := SourceCoverage{AggregateFallbackReasons: make(map[string]int)}
	for _, inv := range invoices {
		if inv.Status == "draft" || inv.Status == "void" {
			continue
		}
		c.IssuedInvoices++
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
		"Detail timestamps track local provider-line ingestion and changes; shared invoice-field changes and separate tax-component creation are not independently tracked.",
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
