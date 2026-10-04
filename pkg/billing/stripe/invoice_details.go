package stripe

import (
	"encoding/json"
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// InvoiceDetailsFromWebhook reads only the signed invoice object, keeping
// provider-shaped line/pricing JSON inside the provider adapter. Expanded
// price data is optional; an opaque price ID does not prove classification.
func InvoiceDetailsFromWebhook(raw []byte) *state.InvoiceDetails {
	var envelope struct {
		Data struct {
			Object stripeInvoiceFacts `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	facts := envelope.Data.Object
	return invoiceDetailsFromFacts(facts)
}

func invoiceDetailsFromFacts(facts stripeInvoiceFacts) *state.InvoiceDetails {
	d := &state.InvoiceDetails{IssuerName: facts.AccountName, DueAt: stripeFactDate(facts.DueDate), IssuedAt: stripeFactDate(facts.StatusTransitions.FinalizedAt)}
	if facts.EffectiveAt > 0 {
		d.IssuedAt = stripeFactDate(facts.EffectiveAt)
	}
	if d.DueAt != "" {
		d.PaymentTerms = "Due by " + d.DueAt // the invoice's actual settlement deadline
	}
	if facts.Lines == nil {
		return d
	}
	d.Lines = &state.InvoiceLines{Complete: facts.Lines.HasMore != nil && !*facts.Lines.HasMore, Items: make([]state.InvoiceLineItem, 0, len(facts.Lines.Data))}
	seen := make(map[string]bool)
	for _, line := range facts.Lines.Data {
		net, tax, valid := stripeLineAmounts(line)
		if line.ID == "" || seen[line.ID] || !valid || line.Currency == "" || line.Currency != facts.Currency {
			d.Lines.Complete = false
			continue
		}
		seen[line.ID] = true
		price := line.Price
		if len(price) == 0 || string(price) == "null" {
			price = line.Pricing.PriceDetails.Price
		}
		category := stripeInvoiceCategory(price)
		if category == "" && (len(price) == 0 || string(price) == "null") {
			category = stripePlanCategory(line.Plan)
		}
		d.Lines.Items = append(d.Lines.Items, state.InvoiceLineItem{
			ID: line.ID, Description: line.Description, NetCents: net, TaxCents: tax,
			ChargeCategory: category,
		})
	}
	return d
}

type stripeInvoiceFacts struct {
	AccountName       string `json:"account_name"`
	DueDate           int64  `json:"due_date"`
	EffectiveAt       int64  `json:"effective_at"`
	Currency          string `json:"currency"`
	StatusTransitions struct {
		FinalizedAt int64 `json:"finalized_at"`
	} `json:"status_transitions"`
	Lines *stripeInvoiceLines `json:"lines"`
}

type stripeInvoiceLines struct {
	HasMore *bool               `json:"has_more"`
	Data    []stripeInvoiceLine `json:"data"`
}

type stripeInvoiceLine struct {
	Currency        string `json:"currency"`
	ID              string `json:"id"`
	Description     string `json:"description"`
	Amount          *int64 `json:"amount"`
	DiscountAmounts []struct {
		Amount int64 `json:"amount"`
	} `json:"discount_amounts"`
	TaxAmounts []struct {
		Amount    int64 `json:"amount"`
		Inclusive bool  `json:"inclusive"`
	} `json:"tax_amounts"`
	Taxes []struct {
		Amount      int64  `json:"amount"`
		TaxBehavior string `json:"tax_behavior"`
	} `json:"taxes"`
	Price   json.RawMessage `json:"price"`
	Plan    json.RawMessage `json:"plan"`
	Pricing struct {
		PriceDetails struct {
			Price json.RawMessage `json:"price"`
		} `json:"price_details"`
	} `json:"pricing"`
}

func stripeLineAmounts(line stripeInvoiceLine) (int64, int64, bool) {
	if line.Amount == nil {
		return 0, 0, false
	}
	net, tax := big.NewInt(*line.Amount), new(big.Int)
	for _, discount := range line.DiscountAmounts {
		net.Sub(net, big.NewInt(discount.Amount))
	}
	for _, amount := range line.TaxAmounts {
		tax.Add(tax, big.NewInt(amount.Amount))
		if amount.Inclusive {
			net.Sub(net, big.NewInt(amount.Amount))
		}
	}
	// The new taxes shape replaces tax_amounts; never add both shapes.
	if len(line.TaxAmounts) == 0 {
		for _, amount := range line.Taxes {
			tax.Add(tax, big.NewInt(amount.Amount))
			if amount.TaxBehavior == "inclusive" {
				net.Sub(net, big.NewInt(amount.Amount))
			}
		}
	}
	return net.Int64(), tax.Int64(), net.IsInt64() && tax.IsInt64()
}

func stripeInvoiceCategory(raw json.RawMessage) string {
	var price struct {
		Recurring *struct {
			UsageType string `json:"usage_type"`
		} `json:"recurring"`
	}
	if json.Unmarshal(raw, &price) != nil || price.Recurring == nil {
		return ""
	}
	switch price.Recurring.UsageType {
	case "metered":
		return "Usage"
	case "licensed":
		return "Purchase"
	}
	return ""
}

func stripePlanCategory(raw json.RawMessage) string {
	var plan struct {
		UsageType string `json:"usage_type"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return ""
	}
	switch plan.UsageType {
	case "metered":
		return "Usage"
	case "licensed":
		return "Purchase"
	}
	return ""
}

func stripeFactDate(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return billing.InvoiceDate(time.Unix(seconds, 0))
}

// InvoiceTaxCentsFromWebhook handles both tax aggregation shapes. Legacy
// invoice.tax wins when present; overflow is invalid data, never a wrapped sum.
func InvoiceTaxCentsFromWebhook(raw []byte, fallback int64) int64 {
	var envelope struct {
		Data struct {
			Object struct {
				Tax             *int64 `json:"tax"`
				TotalTaxAmounts []struct {
					Amount int64 `json:"amount"`
				} `json:"total_tax_amounts"`
				TotalTaxes []struct {
					Amount int64 `json:"amount"`
				} `json:"total_taxes"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return fallback
	}
	obj := envelope.Data.Object
	if obj.Tax != nil {
		return *obj.Tax
	}
	if obj.TotalTaxAmounts == nil && obj.TotalTaxes == nil {
		return fallback
	}
	tax := new(big.Int)
	if obj.TotalTaxAmounts != nil {
		for _, item := range obj.TotalTaxAmounts {
			tax.Add(tax, big.NewInt(item.Amount))
		}
	} else {
		for _, item := range obj.TotalTaxes {
			tax.Add(tax, big.NewInt(item.Amount))
		}
	}
	if !tax.IsInt64() {
		return -1
	} // rejected by state constraints/export validation
	return tax.Int64()
}
