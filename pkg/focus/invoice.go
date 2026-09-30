// Package focus projects Gregale's stored invoices into the FOCUS 1.4
// Invoice Detail vocabulary. This is a partial implementation: payment terms
// and provider line-item history are not persisted. Every artifact declares
// these gaps; it must not be advertised as a conformant Cost and Usage dataset.
package focus

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/text/currency"
)

const Version = "1.4"

// Dataset contains CSV and its matching metadata from a single store read.
type Dataset struct {
	CSV      []byte
	Metadata []byte
}

// BuildInvoiceDetail accepts only one account's invoices whose period end
// falls in month, matching GET /v1/invoices. No provider or pricing API is
// called. All validation finishes before an artifact can be downloaded.
func BuildInvoiceDetail(accountID string, month, generatedAt time.Time, invoices []state.Invoice) (Dataset, error) {
	if err := validateWindow(accountID, month, generatedAt, len(invoices)); err != nil {
		return Dataset{}, err
	}
	rows, totals, excluded, err := invoiceRows(accountID, month, invoices)
	if err != nil {
		return Dataset{}, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return Dataset{}, fmt.Errorf("encode FOCUS CSV: %w", err)
	}
	if buf.Len() > api.MaxFOCUSExportBytes {
		return Dataset{}, fmt.Errorf("FOCUS CSV exceeds %d bytes", api.MaxFOCUSExportBytes)
	}
	metadata, err := buildMetadata(accountID, month, generatedAt, buf.Bytes(), len(rows)-1, totals, excluded)
	if err != nil {
		return Dataset{}, err
	}
	return Dataset{CSV: buf.Bytes(), Metadata: metadata}, nil
}

func validateWindow(accountID string, month, generatedAt time.Time, count int) error {
	if !validField(accountID) || accountID == "" {
		return fmt.Errorf("invalid billing account ID")
	}
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	if month.IsZero() || !month.Equal(start) || month.Year() < 1 || month.Year() > 9999 || !validDate(generatedAt) {
		return fmt.Errorf("invalid export month or generation date")
	}
	if count > api.MaxFOCUSExportInvoices {
		return fmt.Errorf("FOCUS export exceeds %d invoices", api.MaxFOCUSExportInvoices)
	}
	return nil
}

func invoiceRows(accountID string, month time.Time, invoices []state.Invoice) ([][]string, map[string]string, map[string]int, error) {
	// Copy before sorting: callers may retain the store's snapshot.
	ordered := append([]state.Invoice(nil), invoices...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	rows := [][]string{headers()}
	totals := make(map[string]*big.Int)
	excluded := make(map[string]int)
	seen := make(map[string]bool)
	for _, inv := range ordered {
		if inv.AccountID != accountID || inv.PeriodEnd.Before(month) || !inv.PeriodEnd.Before(month.AddDate(0, 1, 0)) {
			return nil, nil, nil, fmt.Errorf("invoice outside export account or month")
		}
		if inv.ID == "" || seen[inv.ID] {
			return nil, nil, nil, fmt.Errorf("missing or duplicate invoice ID")
		}
		seen[inv.ID] = true
		if inv.Status == "draft" || inv.Status == "void" {
			excluded[inv.Status]++
			continue
		}
		issuer, code, err := validateInvoice(inv)
		if err != nil {
			return nil, nil, nil, err
		}
		rows = append(rows, invoiceRow(inv, issuer, code, "Usage", inv.TotalCents-inv.TaxCents))
		if inv.TaxCents != 0 {
			rows = append(rows, invoiceRow(inv, issuer, code, "Tax", inv.TaxCents))
		}
		if totals[code] == nil {
			totals[code] = new(big.Int)
		}
		totals[code].Add(totals[code], big.NewInt(inv.TotalCents))
	}
	formatted := make(map[string]string, len(totals))
	for code, cents := range totals {
		formatted[code] = decimalCents(cents)
	}
	return rows, formatted, excluded, nil
}

func validateInvoice(inv state.Invoice) (issuer, code string, err error) {
	if inv.Status != "open" && inv.Status != "paid" && inv.Status != "uncollectible" {
		return "", "", fmt.Errorf("unsupported invoice status")
	}
	for _, value := range []string{inv.ID, inv.ProviderInvoiceID} {
		if !validField(value) || value == "" {
			return "", "", fmt.Errorf("invalid invoice identifier")
		}
	}
	if inv.TotalCents < 0 || inv.TaxCents < 0 || inv.TaxCents > inv.TotalCents {
		return "", "", fmt.Errorf("invoice tax and total cannot be reconciled")
	}
	if !validDate(inv.PeriodStart) || !validDate(inv.PeriodEnd) || !inv.PeriodEnd.After(inv.PeriodStart) ||
		!validDate(inv.CreatedAt) || !validDate(inv.UpdatedAt) || inv.UpdatedAt.Before(inv.CreatedAt) {
		return "", "", fmt.Errorf("invoice dates cannot be represented")
	}
	u, parseErr := currency.ParseISO(inv.Currency)
	if parseErr != nil || u.String() == "XXX" {
		return "", "", fmt.Errorf("invoice currency is not a recognized ISO 4217 currency")
	}
	if scale, _ := currency.Standard.Rounding(u); scale != 2 {
		return "", "", fmt.Errorf("invoice currency requires unsupported minor-unit precision")
	}
	switch inv.Provider {
	case "polar":
		issuer = "Polar"
	case "paddle":
		issuer = "Paddle"
	case "stripe":
		issuer = "Gregale" // Stripe processes payments; Gregale issues the invoice.
	default:
		return "", "", fmt.Errorf("unsupported invoice issuer")
	}
	return issuer, u.String(), nil
}

func invoiceRow(inv state.Invoice, issuer, code, category string, cents int64) []string {
	component, description := "charges", "Aggregated invoice charges excluding tax"
	if category == "Tax" {
		component, description = "tax", "Aggregated invoice tax"
	}
	grain, _ := json.Marshal(map[string]string{"x_GregaleAggregation": component})
	return []string{
		decimalCents(big.NewInt(cents)), inv.AccountID, code,
		date(inv.PeriodEnd), date(inv.PeriodStart), category,
		date(inv.CreatedAt), description, string(grain), inv.ID + ":" + component,
		date(inv.UpdatedAt), inv.ProviderInvoiceID,
		"", issuer, "Issued", // Actual issue date is not stored; never use first-seen time.
		"", "", // Due date and required PaymentTerms are unavailable (metadata declares the gap).
		inv.ProviderInvoiceID, // Original invoices reference themselves, per FOCUS 1.4.
	}
}

func decimalCents(cents *big.Int) string {
	// Arbitrary precision keeps multi-invoice totals exact even past int64.
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(cents, big.NewInt(100), fraction)
	return whole.String() + "." + fmt.Sprintf("%02d", fraction.Int64())
}

func validField(s string) bool {
	return len(s) <= api.MaxFOCUSExportFieldBytes && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func validDate(t time.Time) bool { return !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 }
func date(t time.Time) string    { return t.UTC().Format(time.RFC3339Nano) }

func headers() []string {
	cols := columns()
	out := make([]string, len(cols))
	for i, col := range cols {
		out[i] = col.ColumnName
	}
	return out
}

// Keep the schema and CSV in the same order. Conditional payment-currency
// and purchase-order columns are omitted because their source data is absent.
func columns() []ColumnDefinition {
	names := []string{
		"BilledCost", "BillingAccountId", "BillingCurrency", "BillingPeriodEnd", "BillingPeriodStart", "ChargeCategory",
		"InvoiceDetailCreated", "InvoiceDetailDescription", "InvoiceDetailGrain", "InvoiceDetailId", "InvoiceDetailLastUpdated",
		"InvoiceId", "InvoiceIssueDate", "InvoiceIssuerName", "InvoiceIssueStatus", "PaymentDueDate", "PaymentTerms", "ReferenceInvoiceId",
	}
	cols := make([]ColumnDefinition, len(names))
	for i, name := range names {
		cols[i] = ColumnDefinition{ColumnName: name, DataType: "STRING", StringMaxLength: api.MaxFOCUSExportFieldBytes + len(":charges"), StringEncoding: "UTF-8"}
		switch name {
		case "BilledCost":
			cols[i] = ColumnDefinition{ColumnName: name, DataType: "DECIMAL", NumericPrecision: 19, NumberScale: 2}
		case "BillingPeriodEnd", "BillingPeriodStart", "InvoiceDetailCreated", "InvoiceDetailLastUpdated", "InvoiceIssueDate", "PaymentDueDate":
			cols[i] = ColumnDefinition{ColumnName: name, DataType: "DATETIME"}
		case "InvoiceDetailGrain":
			cols[i] = ColumnDefinition{ColumnName: name, DataType: "JSON"}
		}
	}
	return cols
}

// ParseMonth requires a canonical YYYY-MM value; exports never default to an
// implicit current month, which could change while a script is running.
func ParseMonth(value string) (time.Time, error) {
	m, err := time.Parse("2006-01", value)
	if err != nil || m.Year() < 1 || m.Format("2006-01") != value {
		return time.Time{}, fmt.Errorf("expected month in YYYY-MM format")
	}
	return m, nil
}

// ValidFormat is shared by the API and CLI so both accept identical encodings.
func ValidFormat(format string) bool {
	return format == "zip" || format == "csv" || format == "metadata"
}
