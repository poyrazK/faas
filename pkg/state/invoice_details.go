package state

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// InvoiceDetails contains only invoice facts supplied by a provider. Empty
// fields mean unavailable, never a fabricated date or settlement term. JSON
// keys are omitted so a later sparse webhook cannot erase existing facts.
type InvoiceDetails struct {
	PaymentTerms  string            `json:"payment_terms,omitempty"`
	IssuerName    string            `json:"issuer_name,omitempty"`
	IssuedAt      string            `json:"issued_at,omitempty"`
	DueAt         string            `json:"due_at,omitempty"`
	Lines         *InvoiceLines     `json:"lines,omitempty"`
	LineFirstSeen map[string]string `json:"line_first_seen,omitempty"` // retained across line removal
}

// InvoiceLines is an atomic provider snapshot. Complete describes pagination,
// not reconciliation or classification; exports check those independently.
type InvoiceLines struct {
	Complete bool              `json:"complete"`
	Items    []InvoiceLineItem `json:"items"`
}

type InvoiceLineItem struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	ChargeCategory string `json:"charge_category"` // empty means unknown
	NetCents       int64  `json:"net_cents"`       // after discounts, excluding tax
	TaxCents       int64  `json:"tax_cents"`
	CreatedAt      string `json:"created_at,omitempty"` // local first ingestion
	UpdatedAt      string `json:"updated_at,omitempty"` // local last fact change
}

// ValidateInvoiceDetails bounds storage and rejects invalid facts before an
// upsert. Unknown classification and incomplete lists are valid source gaps.
func ValidateInvoiceDetails(d *InvoiceDetails) error {
	if d == nil {
		return nil
	}
	if len(d.LineFirstSeen) > api.MaxInvoiceSeenLineIDs {
		return fmt.Errorf("state: too many historical invoice line IDs")
	}
	for id, value := range d.LineFirstSeen {
		if id == "" || !validInvoiceText(id, api.MaxFOCUSExportFieldBytes) || strings.ContainsAny(id, "\r\n") {
			return fmt.Errorf("state: invalid historical invoice line ID")
		}
		if parsed, err := time.Parse(time.RFC3339Nano, value); err != nil || parsed.UTC().Year() < 1 || parsed.UTC().Year() > 9999 {
			return fmt.Errorf("state: invalid historical invoice line date")
		}
	}
	for _, value := range []string{d.PaymentTerms, d.IssuerName} {
		if !validInvoiceText(value, api.MaxInvoiceDetailTextBytes) {
			return fmt.Errorf("state: invalid invoice detail text")
		}
	}
	for _, value := range []string{d.IssuedAt, d.DueAt} {
		if value == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, value); err != nil || parsed.UTC().Year() < 1 || parsed.UTC().Year() > 9999 {
			return fmt.Errorf("state: invalid invoice detail date")
		}
	}
	if d.Lines == nil {
		return nil
	}
	if len(d.Lines.Items) > api.MaxInvoiceLineItems {
		return fmt.Errorf("state: too many invoice line items")
	}
	seen := make(map[string]bool)
	for _, item := range d.Lines.Items {
		if item.ID == "" || seen[item.ID] || !validInvoiceText(item.ID, api.MaxFOCUSExportFieldBytes) || strings.ContainsAny(item.ID, "\r\n") {
			return fmt.Errorf("state: invalid or duplicate invoice line ID")
		}
		seen[item.ID] = true
		for _, value := range []string{item.CreatedAt, item.UpdatedAt} {
			if value != "" {
				if parsed, err := time.Parse(time.RFC3339Nano, value); err != nil || parsed.UTC().Year() < 1 || parsed.UTC().Year() > 9999 {
					return fmt.Errorf("state: invalid invoice line date")
				}
			}
		}
		if !validInvoiceText(item.Description, api.MaxInvoiceDetailTextBytes) || (item.ChargeCategory != "" && !InvoiceChargeCategoryValid(item.ChargeCategory)) {
			return fmt.Errorf("state: invalid invoice line description or category")
		}
	}
	return nil
}

func validInvoiceText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func InvoiceChargeCategoryValid(category string) bool {
	switch category {
	case "Usage", "Purchase", "Tax", "Credit", "Adjustment":
		return true
	}
	return false
}

// InvoiceLineGap returns why an invoice cannot safely use its detailed rows.
// Arbitrary precision sums keep overflow from accepting contradictory totals.
func InvoiceLineGap(inv Invoice) string {
	if inv.Details == nil || inv.Details.Lines == nil {
		return "unavailable"
	}
	lines := inv.Details.Lines
	if !lines.Complete {
		return "incomplete"
	}
	if len(lines.Items) == 0 {
		return "empty"
	}
	net, tax := new(big.Int), new(big.Int)
	for _, item := range lines.Items {
		if item.ChargeCategory == "" {
			return "unclassified"
		}
		if item.ChargeCategory == "Tax" {
			return "tax_in_non_tax_lines"
		}
		net.Add(net, big.NewInt(item.NetCents))
		tax.Add(tax, big.NewInt(item.TaxCents))
	}
	expected := new(big.Int).Sub(big.NewInt(inv.TotalCents), big.NewInt(inv.TaxCents))
	if net.Cmp(expected) != 0 || tax.Cmp(big.NewInt(inv.TaxCents)) != 0 {
		return "totals_mismatch"
	}
	return ""
}

func cloneInvoice(inv Invoice) Invoice {
	inv.Details = mergeInvoiceDetails(nil, inv.Details)
	return inv
}

func mergeInvoiceDetails(old, incoming *InvoiceDetails) *InvoiceDetails {
	if old == nil && incoming == nil {
		return nil
	}
	out := new(InvoiceDetails)
	if old != nil {
		*out = *old
	}
	if incoming != nil {
		if incoming.PaymentTerms != "" {
			out.PaymentTerms = incoming.PaymentTerms
		}
		if incoming.IssuerName != "" {
			out.IssuerName = incoming.IssuerName
		}
		if incoming.IssuedAt != "" {
			out.IssuedAt = incoming.IssuedAt
		}
		if incoming.DueAt != "" {
			out.DueAt = incoming.DueAt
		}
		if incoming.Lines != nil {
			out.Lines = incoming.Lines
		}
	}
	if out.Lines != nil {
		lines := *out.Lines
		lines.Items = append([]InvoiceLineItem(nil), lines.Items...)
		out.Lines = &lines
	}
	seen := make(map[string]string)
	if incoming != nil {
		for id, value := range incoming.LineFirstSeen {
			seen[id] = value
		}
	}
	if old != nil {
		for id, value := range old.LineFirstSeen {
			seen[id] = value
		}
	}
	if len(seen) > 0 {
		out.LineFirstSeen = seen
	} else {
		out.LineFirstSeen = nil
	}
	return out
}

func encodeInvoiceDetails(d *InvoiceDetails) ([]byte, error) {
	if err := ValidateInvoiceDetails(d); err != nil {
		return nil, err
	}
	if d == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(stampInvoiceLines(nil, d, time.Now().UTC()))
}

func stampInvoiceLines(old, incoming *InvoiceDetails, now time.Time) *InvoiceDetails {
	out := mergeInvoiceDetails(nil, incoming)
	if out == nil || out.Lines == nil {
		return out
	}
	if out.Lines.Items == nil {
		out.Lines.Items = make([]InvoiceLineItem, 0)
	}
	if out.LineFirstSeen == nil {
		out.LineFirstSeen = make(map[string]string)
	}
	if old != nil {
		for id, value := range old.LineFirstSeen {
			out.LineFirstSeen[id] = value
		}
	}
	existing := make(map[string]InvoiceLineItem)
	if old != nil && old.Lines != nil {
		for _, line := range old.Lines.Items {
			existing[line.ID] = line
		}
	}
	for i := range out.Lines.Items {
		line := &out.Lines.Items[i]
		line.CreatedAt, line.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
		if first, ok := out.LineFirstSeen[line.ID]; ok {
			line.CreatedAt = first
		}
		if previous, ok := existing[line.ID]; ok {
			line.CreatedAt = previous.CreatedAt
			previous.CreatedAt, previous.UpdatedAt = "", ""
			facts := *line
			facts.CreatedAt, facts.UpdatedAt = "", ""
			if facts == previous {
				line.UpdatedAt = existing[line.ID].UpdatedAt
			}
		}
		out.LineFirstSeen[line.ID] = line.CreatedAt
	}
	return out
}
