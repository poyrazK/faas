package billing

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

var ErrFinancialUnavailable = errors.New("retained financial evidence unavailable")

// ReadFinancialCosts is read-only. No current price is substituted for missing
// historical contracts; invoice facts remain a separately identified source.
func ReadFinancialCosts(ctx context.Context, store state.Store, account string, start, now time.Time) (api.FinancialCostsResponse, error) {
	fs, ok := store.(state.FinancialStore)
	if !ok {
		return api.FinancialCostsResponse{}, ErrFinancialUnavailable
	}
	start, now = start.UTC(), now.UTC()
	if account == "" || start.IsZero() || start.Day() != 1 || !start.Equal(start.Truncate(24*time.Hour)) || start.After(now) {
		return api.FinancialCostsResponse{}, state.ErrInvalidArgument
	}
	end := start.AddDate(0, 1, 0)
	asOf := minTime(end, now.Truncate(time.Minute))
	retained, err := fs.FinancialEvidenceCoverage(ctx)
	if err != nil {
		return api.FinancialCostsResponse{}, err
	}
	// Read coverage before fixing the evidence head: each successful window is
	// published only after all of its compute writes committed.
	coverage, err := fs.FinancialSamplingCoverage(ctx, start, asOf)
	if err != nil {
		return api.FinancialCostsResponse{}, err
	}
	head, err := fs.FinancialEvidenceHead(ctx, account, start, end)
	if err != nil {
		return api.FinancialCostsResponse{}, err
	}
	rows, err := fs.AggregateFinancialUsage(ctx, account, start, asOf, head)
	if err != nil {
		return api.FinancialCostsResponse{}, err
	}
	prices, err := fs.ListFinancialPriceSnapshots(ctx, account, start)
	if err != nil {
		return api.FinancialCostsResponse{}, err
	}
	out := api.FinancialCostsResponse{AccountID: account, Currency: "EUR", PeriodStart: start, PeriodEnd: end, AsOf: asOf, RetainedFrom: retained, EvidenceThroughID: head, Scope: "retained_compute_and_interface_egress", Meters: []api.FinancialMeterCosts{}, Invoices: []api.Invoice{}, InvoiceReconciliation: "not_reconciled", MissingBillComponents: []string{"subscription_and_addons", "credits_and_adjustments", "tax", "external_and_managed_resource_meters"}}
	total := new(big.Int)
	for _, meter := range []string{"compute", "egress"} {
		m, err := financialMeterCosts(account, start, end, asOf, now, retained, meter, coverage, rows, prices)
		if err != nil {
			return out, err
		}
		out.Meters = append(out.Meters, m)
		total.Add(total, big.NewInt(m.Accrued.NetMillicents))
	}
	if !total.IsInt64() {
		return out, financial.ErrOverflow
	}
	out.KnownUsageMillicents = total.Int64()
	return out, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func financialMeterCosts(account string, start, end, asOf, now, retained time.Time, meter string, sampling state.FinancialSamplingCoverage, rows []state.FinancialUsageAggregate, snapshots []state.FinancialPriceSnapshot) (api.FinancialMeterCosts, error) {
	expected := int64(asOf.Sub(start) / time.Minute)
	minutes := sampling.ComputeMinutes
	if meter == "egress" {
		minutes = sampling.EgressMinutes
	}
	complete := !start.Before(retained) && expected > 0 && minutes == expected
	fresh := !now.Before(end) || (!sampling.ObservedAt.IsZero() && now.Sub(sampling.ObservedAt) <= api.FinancialEvidenceFreshness && !sampling.ObservedAt.After(now))
	out := api.FinancialMeterCosts{Meter: meter, Coverage: api.FinancialMeterCoverage{Complete: complete, Fresh: fresh, ExpectedMinutes: expected, CompleteMinutes: minutes, Reasons: []string{}}, Accrued: financial.ContractCosts{Meter: meter, Contracts: []financial.MeterCost{}}, Forecast: financial.Forecast{AccountID: account, Meter: meter, Currency: "EUR", PeriodStart: start, PeriodEnd: end, CompleteThrough: asOf, Method: "elapsed_time_run_rate_v1"}}
	if start.Before(retained) {
		out.Coverage.Reasons = append(out.Coverage.Reasons, "retention_started_during_or_after_period")
	}
	if minutes != expected {
		out.Coverage.Reasons = append(out.Coverage.Reasons, "missing_sampling_windows")
	}
	if !fresh {
		out.Coverage.Reasons = append(out.Coverage.Reasons, "stale_evidence")
	}
	byVersion := map[string]state.FinancialPriceSnapshot{}
	var prices []financial.Price
	out.PriceContracts = []api.FinancialPriceContract{}
	for _, p := range snapshots {
		if p.Price.Meter != meter || p.EffectiveFrom.After(asOf) {
			continue
		}
		byVersion[p.Price.Version] = p
		out.PriceContracts = append(out.PriceContracts, api.FinancialPriceContract{Price: p.Price, Plan: p.Plan, EffectiveFrom: p.EffectiveFrom, DeliveryMode: p.DeliveryMode})
		if p.DeliveryMode == "live" {
			prices = append(prices, p.Price)
		}
	}
	var entries []financial.VersionedEvidence
	unpriced, nonBillable := new(big.Int), new(big.Int)
	for i, row := range rows {
		if row.Meter != meter {
			continue
		}
		p, ok := byVersion[row.PriceVersion]
		if !ok {
			unpriced.Add(unpriced, big.NewInt(row.Quantity))
			continue
		}
		if p.Plan != row.Plan || p.Price.Unit != row.Unit {
			return out, fmt.Errorf("financial price identity: %w", state.ErrConflict)
		}
		if p.DeliveryMode != "live" {
			nonBillable.Add(nonBillable, big.NewInt(row.Quantity))
			continue
		}
		entries = append(entries, financial.VersionedEvidence{PriceVersion: row.PriceVersion, Evidence: financial.Evidence{AccountID: account, SourceID: fmt.Sprintf("aggregate:%d", i), Meter: meter, Quantity: row.Quantity, Start: start, End: asOf, Attribution: row.Attribution}})
	}
	if !unpriced.IsInt64() || !nonBillable.IsInt64() {
		return out, financial.ErrOverflow
	}
	out.Coverage.UnpricedQuantity, out.Coverage.NonBillableQuantity = unpriced.Int64(), nonBillable.Int64()
	if unpriced.Sign() != 0 || len(byVersion) == 0 {
		out.Coverage.Complete = false
		out.Coverage.Reasons = append(out.Coverage.Reasons, "missing_historical_prices")
	}
	if len(prices) > 0 {
		cost, err := financial.CostContracts(account, start, end, prices, entries)
		if err != nil {
			return out, err
		}
		out.Accrued = cost
	}
	if !now.Before(end) {
		out.Forecast.Reason = "period_closed"
		return out, nil
	}
	if len(byVersion) == 0 || unpriced.Sign() != 0 {
		out.Forecast.Reason = "missing_historical_prices"
		return out, nil
	}
	if len(prices) == 0 {
		out.Forecast.Reason = "meter_not_billed"
		return out, nil
	}
	price := prices[0]
	for _, p := range prices[1:] {
		a, b := price, p
		a.Version, b.Version = "", ""
		if a != b {
			out.Forecast.Reason = "price_contract_changed"
			return out, nil
		}
	}
	forecast, err := financial.ForecastMeter(account, start, end, asOf, out.Accrued.Quantity, price, out.Coverage.Complete, fresh)
	if err != nil {
		return out, err
	}
	out.Forecast = forecast
	return out, nil
}
