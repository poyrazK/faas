package billing

import (
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// MaxStatementInvoiceLines bounds one exported invoice (ADR-1502); Stripe
// invoices hold at most 250 line items, one of which may be rounding.
const MaxStatementInvoiceLines = 249

// ErrInvoiceCurrencyUnsupported reports a currency whose minor unit is not
// one hundredth of the main unit. Exports convert millicents to cents.
var ErrInvoiceCurrencyUnsupported = errors.New("statement invoices support two-decimal currencies only")

// ErrStatementNotInvoiceable reports a statement that cannot become an
// invoice: unpriced units, unknown price sources, or too many lines.
var ErrStatementNotInvoiceable = errors.New("statement cannot be exported as an invoice")

// nonTwoDecimalCurrencies are the ISO-4217 codes Stripe prices in zero or
// three decimal places.
var nonTwoDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true, "KRW": true, "MGA": true,
	"PYG": true, "RWF": true, "UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true,
	"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true,
}

// StatementInvoiceLine is one invoice item: units of one price source at
// one unit price. Free allowance units are their own zero-price line, and
// a re-rated adjustment line can be negative (ADR-951, ADR-975).
type StatementInvoiceLine struct {
	RateCardID               string
	PlatformTenantRateCardID string
	Units                    int64
	PriceMillicentsPerUnit   int64
	AmountMillicents         int64
	// AmountMinor is AmountMillicents in the currency's minor unit (cents),
	// rounded half away from zero.
	AmountMinor int64
	// Credit marks the negative line for prepaid credit (ADR-1306).
	Credit bool
	// Fee is the statement fee kind of a base fee or minimum top-up line
	// (ADR-1316); Units is zero.
	Fee string
	// Unit is what the line's units are when not requests (ADR-1317).
	Unit string
	// Label names the units of a line without a rate card, such as a
	// Product Control feature's successful runs (ADR-20261018212918).
	Label string
}

// StatementInvoice is a statement converted to invoice items. RoundingMinor
// is the extra item that makes the items sum to the statement total rounded
// to the minor unit; zero when no rounding item is needed.
type StatementInvoice struct {
	Currency      string
	Lines         []StatementInvoiceLine
	RoundingMinor int64
	TotalMinor    int64
}

type invoiceLineKey struct {
	rateCardID, tenantCardID string
	price                    int64
	fee, unit, label         string
}

func (inv *StatementInvoice) add(key invoiceLineKey, units int64, lines map[invoiceLineKey]*StatementInvoiceLine) error {
	if units == 0 {
		return nil
	}
	amount, err := multiplyMillicents(units, key.price)
	if err != nil {
		return err
	}
	line := lines[key]
	if line == nil {
		line = &StatementInvoiceLine{RateCardID: key.rateCardID, PlatformTenantRateCardID: key.tenantCardID, PriceMillicentsPerUnit: key.price,
			Unit: key.unit, Label: key.label}
		lines[key] = line
	}
	if !addsWithinInt64(line.Units, units) || !addsWithinInt64(line.AmountMillicents, amount) {
		return fmt.Errorf("billing: statement invoice line overflow")
	}
	line.Units += units
	line.AmountMillicents += amount
	return nil
}

// AppStatementInvoice groups an app consumer statement's minute buckets into
// invoice lines by rate card and unit price. cards supplies tier prices.
func AppStatementInvoice(statement state.APIConsumerUsageStatement, cards []state.APIConsumerRateCard) (StatementInvoice, error) {
	inv := StatementInvoice{Currency: statement.Currency}
	tiers := map[string][]state.APIConsumerRateCardTier{}
	for _, card := range cards {
		tiers[card.ID] = card.Tiers
	}
	lines := map[invoiceLineKey]*StatementInvoiceLine{}
	for _, bucket := range statement.Buckets {
		if bucket.RateCardID == "" {
			if bucket.BillableUnits != 0 {
				return inv, fmt.Errorf("%w: unpriced units", ErrStatementNotInvoiceable)
			}
			continue
		}
		if len(bucket.TierUnits) > 0 {
			ladder := tiers[bucket.RateCardID]
			if len(ladder) != len(bucket.TierUnits) {
				return inv, fmt.Errorf("%w: rate card %s ladder changed", ErrStatementNotInvoiceable, bucket.RateCardID)
			}
			for i, units := range bucket.TierUnits {
				if err := inv.add(invoiceLineKey{rateCardID: bucket.RateCardID, price: ladder[i].PriceMillicentsPerUnit, unit: bucket.Unit}, units, lines); err != nil {
					return inv, err
				}
			}
			continue
		}
		charged := bucket.Charged()
		if err := inv.add(invoiceLineKey{rateCardID: bucket.RateCardID, unit: bucket.Unit}, bucket.BillableUnits-charged, lines); err != nil {
			return inv, err
		}
		if err := inv.add(invoiceLineKey{rateCardID: bucket.RateCardID, price: bucket.PriceMillicentsPerUnit, unit: bucket.Unit}, charged, lines); err != nil {
			return inv, err
		}
	}
	for _, fee := range statement.Fees {
		key := invoiceLineKey{rateCardID: fee.RateCardID, fee: fee.Kind}
		line := lines[key]
		if line == nil {
			line = &StatementInvoiceLine{RateCardID: fee.RateCardID, Fee: fee.Kind}
			lines[key] = line
		}
		if !addsWithinInt64(line.AmountMillicents, fee.AmountMillicents) {
			return inv, fmt.Errorf("billing: statement invoice line overflow")
		}
		line.AmountMillicents += fee.AmountMillicents
	}
	inv, err := finishStatementInvoice(inv, lines, statement.AmountMillicents)
	if err != nil || statement.CreditAppliedMillicents == 0 {
		return inv, err
	}
	return withCreditLine(inv, statement.CreditAppliedMillicents, statement.AmountDueMillicents())
}

// withCreditLine appends the prepaid credit a statement drew (ADR-1306) as a
// negative line; the invoice total becomes the amount due rounded to the
// cent.
func withCreditLine(inv StatementInvoice, creditMillicents, dueMillicents int64) (StatementInvoice, error) {
	if len(inv.Lines) >= MaxStatementInvoiceLines {
		return inv, fmt.Errorf("%w: %d invoice lines (1 to %d)", ErrStatementNotInvoiceable, len(inv.Lines)+1, MaxStatementInvoiceLines)
	}
	inv.Lines = append(inv.Lines, StatementInvoiceLine{Credit: true, AmountMillicents: -creditMillicents,
		AmountMinor: -millicentsToCents(creditMillicents)})
	inv.TotalMinor = millicentsToCents(dueMillicents)
	var itemsMinor int64
	for _, line := range inv.Lines {
		itemsMinor += line.AmountMinor
	}
	inv.RoundingMinor = inv.TotalMinor - itemsMinor
	return inv, nil
}

// TenantStatementInvoice groups a platform tenant statement's lines across
// apps and subjects by price source and unit price.
func TenantStatementInvoice(statement state.PlatformTenantStatement) (StatementInvoice, error) {
	inv := StatementInvoice{Currency: statement.Currency}
	lines := map[invoiceLineKey]*StatementInvoiceLine{}
	for _, line := range statement.Lines {
		if line.RateCardID == "" && line.PlatformTenantRateCardID == "" {
			return inv, fmt.Errorf("%w: unpriced units", ErrStatementNotInvoiceable)
		}
		key := invoiceLineKey{rateCardID: line.RateCardID, tenantCardID: line.PlatformTenantRateCardID, price: line.PriceMillicentsPerUnit}
		if err := inv.add(key, line.BillableUnits, lines); err != nil {
			return inv, err
		}
	}
	return finishStatementInvoice(inv, lines, statement.AmountMillicents)
}

// ProductControlStatementInvoice groups a finalized Product Control
// statement's charged successes by feature, plan and admitted unit price
// (ADR-20261018212918). Catalog revisions at the same price share a line;
// waived, failed, cancelled and unresolved runs are never invoiced.
func ProductControlStatementInvoice(doc api.ProductBillingDocument) (StatementInvoice, error) {
	inv := StatementInvoice{Currency: doc.Currency}
	if doc.Kind != "statement" || doc.Snapshot == nil {
		return inv, fmt.Errorf("%w: only finalized Product Control statements export; credit notes keep their recorded handoff", ErrStatementNotInvoiceable)
	}
	lines := map[invoiceLineKey]*StatementInvoiceLine{}
	for _, line := range doc.Snapshot.Lines {
		key := invoiceLineKey{price: line.UnitPriceMillicents, label: fmt.Sprintf("successful %s runs on plan %s", line.Feature, line.Plan)}
		if err := inv.add(key, line.Succeeded, lines); err != nil {
			return inv, err
		}
	}
	return finishStatementInvoice(inv, lines, doc.AmountMillicents)
}

func finishStatementInvoice(inv StatementInvoice, lines map[invoiceLineKey]*StatementInvoiceLine, total int64) (StatementInvoice, error) {
	if nonTwoDecimalCurrencies[inv.Currency] || len(inv.Currency) != 3 {
		return inv, ErrInvoiceCurrencyUnsupported
	}
	var sum int64
	for _, line := range lines {
		if line.Units == 0 && line.AmountMillicents == 0 {
			continue
		}
		line.AmountMinor = millicentsToCents(line.AmountMillicents)
		sum += line.AmountMillicents
		inv.Lines = append(inv.Lines, *line)
	}
	if sum != total {
		return inv, fmt.Errorf("%w: lines total %d millicents, statement %d", ErrStatementNotInvoiceable, sum, total)
	}
	if len(inv.Lines) == 0 || len(inv.Lines) > MaxStatementInvoiceLines {
		return inv, fmt.Errorf("%w: %d invoice lines (1 to %d)", ErrStatementNotInvoiceable, len(inv.Lines), MaxStatementInvoiceLines)
	}
	sort.Slice(inv.Lines, func(i, j int) bool {
		a, b := inv.Lines[i], inv.Lines[j]
		if a.PlatformTenantRateCardID+a.RateCardID != b.PlatformTenantRateCardID+b.RateCardID {
			return a.PlatformTenantRateCardID+a.RateCardID < b.PlatformTenantRateCardID+b.RateCardID
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		if a.Fee != b.Fee {
			return a.Fee < b.Fee
		}
		return a.PriceMillicentsPerUnit < b.PriceMillicentsPerUnit
	})
	inv.TotalMinor = millicentsToCents(total)
	var itemsMinor int64
	for _, line := range inv.Lines {
		itemsMinor += line.AmountMinor
	}
	inv.RoundingMinor = inv.TotalMinor - itemsMinor
	return inv, nil
}

// millicentsToCents converts 1/100000 currency units to 1/100, rounding
// half away from zero.
func millicentsToCents(millicents int64) int64 {
	if millicents < 0 {
		return -((-millicents + 500) / 1000)
	}
	return (millicents + 500) / 1000
}
