// Package invoicedetail renders the shared FOCUS 1.4 invoice record values.
// It has no store dependency, so ingestion and exports fingerprint identical
// values without introducing a state/focus import cycle.
package invoicedetail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Snapshot struct {
	ID, AccountID, Provider, ProviderInvoiceID, Currency string
	IssuerName, PaymentTerms, IssuedAt, DueAt            string
	PeriodStart, PeriodEnd, CreatedAt, UpdatedAt         time.Time
	TotalCents, TaxCents                                 int64
	Detailed                                             bool
	Lines                                                []Line
}

type Line struct {
	ID, Description, ChargeCategory, CreatedAt, UpdatedAt string
	NetCents, TaxCents                                    int64
}

// Record contains the exact column values, including legacy date observations.
// Lifecycle replaces those dates when reliable per-record tracking exists.
type Record struct{ values [18]string }

func ColumnNames() []string {
	return []string{
		"BilledCost", "BillingAccountId", "BillingCurrency", "BillingPeriodEnd", "BillingPeriodStart", "ChargeCategory",
		"InvoiceDetailCreated", "InvoiceDetailDescription", "InvoiceDetailGrain", "InvoiceDetailId", "InvoiceDetailLastUpdated",
		"InvoiceId", "InvoiceIssueDate", "InvoiceIssuerName", "InvoiceIssueStatus", "PaymentDueDate", "PaymentTerms", "ReferenceInvoiceId",
	}
}

func (r Record) ID() string        { return r.values[9] }
func (r Record) CreatedAt() string { return r.values[6] }
func (r Record) UpdatedAt() string { return r.values[10] }
func (r Record) Values() []string  { return append([]string(nil), r.values[:]...) }

func (r Record) WithLifecycle(created, updated string) Record {
	r.values[6], r.values[10] = created, updated
	return r
}

// Fingerprint excludes lifecycle dates themselves; all other exported column
// values participate, including normalized common invoice facts and row grain.
func (r Record) Fingerprint() string {
	r.values[6], r.values[10] = "", ""
	b, _ := json.Marshal(r.values) // a string array cannot fail JSON encoding
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func Records(s Snapshot) []Record {
	if !s.Detailed {
		out := []Record{aggregateRecord(s, "charges", "Usage", s.TotalCents-s.TaxCents)}
		if s.TaxCents != 0 {
			out = append(out, aggregateRecord(s, "tax", "Tax", s.TaxCents))
		}
		return out
	}
	lines := append([]Line(nil), s.Lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	out := make([]Record, 0, len(lines)*2)
	for _, line := range lines {
		out = append(out, lineRecord(s, line, "charges", line.ChargeCategory, line.NetCents))
		if line.TaxCents != 0 {
			out = append(out, lineRecord(s, line, "tax", "Tax", line.TaxCents))
		}
	}
	return out
}

func commonRecord(s Snapshot, category string, cents int64) Record {
	issuer := s.IssuerName
	if issuer == "" {
		switch s.Provider {
		case "stripe":
			issuer = "Gregale"
		case "paddle":
			issuer = "Paddle"
		case "polar":
			issuer = "Polar"
		}
	}
	return Record{values: [18]string{
		DecimalCents(big.NewInt(cents)), s.AccountID, strings.ToUpper(s.Currency),
		date(s.PeriodEnd), date(s.PeriodStart), category,
		date(s.CreatedAt), "", "", "", date(s.UpdatedAt), s.ProviderInvoiceID,
		canonicalDate(s.IssuedAt), issuer, "Issued", canonicalDate(s.DueAt), s.PaymentTerms, s.ProviderInvoiceID,
	}}
}

func aggregateRecord(s Snapshot, component, category string, cents int64) Record {
	r := commonRecord(s, category, cents)
	r.values[7] = "Aggregated invoice charges excluding tax"
	if component == "tax" {
		r.values[7] = "Aggregated invoice tax"
	}
	grain, _ := json.Marshal(map[string]string{"x_GregaleAggregation": component})
	r.values[8], r.values[9] = string(grain), s.ID+":"+component
	return r
}

func lineRecord(s Snapshot, line Line, component, category string, cents int64) Record {
	r := commonRecord(s, category, cents)
	r.values[6], r.values[10] = canonicalDate(line.CreatedAt), canonicalDate(line.UpdatedAt)
	r.values[7] = line.Description
	if r.values[7] == "" {
		r.values[7] = "Provider invoice line item"
	}
	grain, _ := json.Marshal(map[string]string{"x_GregaleProviderLineId": line.ID, "x_GregaleComponent": component})
	r.values[8] = string(grain)
	r.values[9] = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:invoice-detail:"+s.ID+":"+line.ID+":"+component)).String()
	return r
}

func DecimalCents(cents *big.Int) string {
	if cents.Sign() < 0 {
		return "-" + DecimalCents(new(big.Int).Abs(cents))
	}
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(cents, big.NewInt(100), fraction)
	return whole.String() + "." + fmt.Sprintf("%02d", fraction.Int64())
}

func canonicalDate(value string) string {
	if value == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value // validation must reject malformed observations, not hide them
	}
	return date(t)
}

func date(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
