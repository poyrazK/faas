package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func dashboardCostMonth(query url.Values, now time.Time) (time.Time, string) {
	now = now.UTC()
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	values, supplied := query["cost_month"]
	if !supplied {
		return current, ""
	}
	if len(values) != 1 {
		return current, "Invalid month selection; showing the current month."
	}
	selected, err := time.Parse("2006-01", values[0])
	if err != nil || selected.Format("2006-01") != values[0] || selected.After(current) {
		return current, "Invalid month selection; showing the current month."
	}
	return selected.UTC(), ""
}

func dashboardAppDetailRefreshQuery(month, groupBy, selectedRoute, selectedMethod string) string {
	query := url.Values{}
	query.Set("cost_month", month)
	if groupBy != "route" && groupBy != "" {
		query.Set("analytics_by", groupBy)
	}
	if groupBy == "route" && selectedRoute != "" && selectedMethod != "" {
		query.Set("analytics_route", selectedRoute)
		query.Set("analytics_method", selectedMethod)
	}
	return query.Encode()
}

func parseAppCostTrendPath(path string) (string, bool) {
	const suffix = "/cost-trend"
	if !strings.HasSuffix(path, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(path, suffix)
	return slug, slug != "" && !strings.ContainsRune(slug, '/')
}

func parseAppCostTrendCSVPath(path string) (string, bool) {
	const suffix = "/cost-trend.csv"
	if !strings.HasSuffix(path, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(path, suffix)
	return slug, slug != "" && !strings.ContainsRune(slug, '/')
}

func parseAppCostsCSVPath(path string) (string, bool) {
	const suffix = "/costs.csv"
	if !strings.HasSuffix(path, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(path, suffix)
	return slug, slug != "" && !strings.ContainsRune(slug, '/')
}

func (s *server) dashboardAppCosts(ctx context.Context, log *slog.Logger, acct state.Account, app state.App, start time.Time, selectionNotice string) *dashboard.AppCostsView {
	now := time.Now().UTC()
	if start.IsZero() {
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	view := &dashboard.AppCostsView{
		Month:           start.Format("January 2006"),
		SelectedMonth:   start.Format("2006-01"),
		MaxMonth:        time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01"),
		SelectionNotice: selectionNotice,
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	accountReport, err := billing.ReadFinancialCosts(readCtx, s.store, acct.ID, start, now)
	if err != nil {
		log.Warn("dashboard app costs unavailable", "account_id", acct.ID, "app_id", app.ID, "month", view.SelectedMonth, "err", err)
		view.Unavailable = "Retained cost data is temporarily unavailable."
		return view
	}
	scoped, err := billing.ScopeFinancialCostsToApp(accountReport, app.ID, app.Slug)
	if err != nil {
		log.Warn("dashboard app costs projection failed", "account_id", acct.ID, "app_id", app.ID, "month", view.SelectedMonth, "err", err)
		view.Unavailable = "Retained cost data is temporarily unavailable."
		return view
	}
	view.KnownUsage = dashboardMoney(scoped.KnownUsageMillicents)
	view.AsOf = scoped.AsOf.Format("2006-01-02 15:04 UTC")
	view.MissingBillComponents = make([]string, 0, len(scoped.MissingBillComponents))
	view.Meters = make([]dashboard.AppCostMeterView, 0, len(scoped.Meters))
	for _, component := range scoped.MissingBillComponents {
		view.MissingBillComponents = append(view.MissingBillComponents, dashboardBillComponent(component))
	}
	for _, meter := range scoped.Meters {
		coverage := dashboardAppCostCoverage(meter.Coverage)
		meterView := dashboard.AppCostMeterView{
			Meter: meter.Meter, Quantity: billing.FormatFinancialQuantity(meter.Quantity, meter.Unit),
			NetCost: dashboardMoney(meter.NetMillicents), Coverage: coverage,
		}
		for i, allocation := range meter.Allocations {
			if i == 5 {
				break
			}
			name := allocation.Attribution.Name
			if name == "" {
				name = allocation.Attribution.DeploymentID
			}
			if name == "" {
				name = app.Slug
			}
			meterView.Drivers = append(meterView.Drivers, dashboard.AppCostDriverView{
				Name: name, Quantity: billing.FormatFinancialQuantity(allocation.Quantity, allocation.Unit), NetCost: dashboardMoney(allocation.NetMillicents),
			})
		}
		view.Meters = append(view.Meters, meterView)
	}

	previousStart := start.AddDate(0, -1, 0)
	previousEnd := previousStart.AddDate(0, 1, 0)
	previousAsOf := previousEnd
	if scoped.AsOf.Before(scoped.PeriodEnd) {
		previousAsOf = previousStart.Add(scoped.AsOf.Sub(start))
		if previousAsOf.After(previousEnd) {
			previousAsOf = previousEnd
		}
	}
	previousReport, err := billing.ReadFinancialCosts(readCtx, s.store, acct.ID, previousStart, previousAsOf)
	if err != nil {
		log.Warn("dashboard app cost comparison unavailable", "account_id", acct.ID, "app_id", app.ID, "month", previousStart.Format("2006-01"), "err", err)
		view.ComparisonUnavailable = "The previous period could not be read, so the month comparison is unavailable."
		return view
	}
	previous, err := billing.ScopeFinancialCostsToApp(previousReport, app.ID, app.Slug)
	if err != nil {
		log.Warn("dashboard app cost comparison projection failed", "account_id", acct.ID, "app_id", app.ID, "month", previousStart.Format("2006-01"), "err", err)
		view.ComparisonUnavailable = "The previous period could not be read, so the month comparison is unavailable."
		return view
	}
	view.ComparisonCoverage = dashboardAppCostComparisonCoverage(scoped, previous)
	if !dashboardAppCostCoverageAdequate(scoped) || !dashboardAppCostCoverageAdequate(previous) {
		view.ComparisonUnavailable = "The comparison is hidden because at least one period has incomplete, stale, or unpriced meter data. Expand comparison coverage below for details."
		return view
	}
	view.Comparison = dashboardAppCostComparison(scoped, previous)
	return view
}

func dashboardAppCostCoverage(coverage api.FinancialMeterCoverage) string {
	status := "complete"
	if !coverage.Complete {
		status = "partial"
	}
	if coverage.Fresh {
		status += " and fresh"
	} else {
		status += " and stale"
	}
	status += fmt.Sprintf(" (%d of %d minutes)", coverage.CompleteMinutes, coverage.ExpectedMinutes)
	if len(coverage.Reasons) != 0 {
		status += "; " + strings.Join(coverage.Reasons, ", ")
	}
	if coverage.UnpricedQuantity > 0 {
		status += fmt.Sprintf("; %d usage units lack a recorded price", coverage.UnpricedQuantity)
	}
	return status
}

func dashboardAppCostCoverageAdequate(report api.FinancialAppCostsResponse) bool {
	if len(report.Meters) == 0 {
		return false
	}
	for _, meter := range report.Meters {
		if !meter.Coverage.Complete || !meter.Coverage.Fresh || meter.Coverage.UnpricedQuantity != 0 {
			return false
		}
	}
	return true
}

func dashboardAppCostComparison(selected, previous api.FinancialAppCostsResponse) *dashboard.AppCostComparisonView {
	previousMeters := make(map[string]int64, len(previous.Meters))
	for _, meter := range previous.Meters {
		previousMeters[meter.Meter] = meter.NetMillicents
	}
	comparison := &dashboard.AppCostComparisonView{
		SelectedPeriod: dashboardAppCostPeriod(selected.PeriodStart, selected.AsOf, selected.PeriodEnd),
		PreviousPeriod: dashboardAppCostPeriod(previous.PeriodStart, previous.AsOf, previous.PeriodEnd),
		SelectedTotal:  dashboardMoney(selected.KnownUsageMillicents),
		PreviousTotal:  dashboardMoney(previous.KnownUsageMillicents),
		Meters:         make([]dashboard.AppCostComparisonMeterView, 0, len(selected.Meters)),
	}
	selectedTotal := big.NewInt(selected.KnownUsageMillicents)
	previousTotal := big.NewInt(previous.KnownUsageMillicents)
	comparison.Difference = dashboardMoneyBig(new(big.Int).Sub(new(big.Int).Set(selectedTotal), previousTotal))
	for _, meter := range selected.Meters {
		previousCost := previousMeters[meter.Meter]
		difference := new(big.Int).Sub(big.NewInt(meter.NetMillicents), big.NewInt(previousCost))
		comparison.Meters = append(comparison.Meters, dashboard.AppCostComparisonMeterView{
			Meter: meter.Meter, SelectedCost: dashboardMoney(meter.NetMillicents),
			PreviousCost: dashboardMoney(previousCost), Difference: dashboardMoneyBig(difference),
		})
	}
	return comparison
}

func dashboardAppCostComparisonCoverage(selected, previous api.FinancialAppCostsResponse) *dashboard.AppCostComparisonCoverageView {
	previousMeters := make(map[string]api.FinancialAppMeterCosts, len(previous.Meters))
	for _, meter := range previous.Meters {
		previousMeters[meter.Meter] = meter
	}
	coverage := &dashboard.AppCostComparisonCoverageView{
		SelectedPeriod: dashboardAppCostPeriod(selected.PeriodStart, selected.AsOf, selected.PeriodEnd),
		PreviousPeriod: dashboardAppCostPeriod(previous.PeriodStart, previous.AsOf, previous.PeriodEnd),
		Meters:         make([]dashboard.AppCostComparisonCoverageMeterView, 0, len(selected.Meters)),
	}
	for _, meter := range selected.Meters {
		prior := previousMeters[meter.Meter]
		coverage.Meters = append(coverage.Meters, dashboard.AppCostComparisonCoverageMeterView{
			Meter: meter.Meter, Selected: dashboardAppCostCoverage(meter.Coverage),
			Previous: dashboardAppCostCoverage(prior.Coverage),
		})
	}
	return coverage
}

func dashboardAppCostPeriod(start, asOf, end time.Time) string {
	if !asOf.Before(end) {
		return start.Format("January 2006")
	}
	if !asOf.After(start) {
		return start.Format("January 2006") + " (no elapsed minutes)"
	}
	return fmt.Sprintf("%s (through %s)", start.Format("January 2006"), asOf.Format("Jan 2, 15:04 UTC"))
}

func (s *server) dashboardAppCostTrend(ctx context.Context, log *slog.Logger, acct state.Account, app state.App, selectedStart time.Time, groupBy, selectedRoute, selectedMethod string) dashboard.AppCostTrendData {
	const monthCount = 6
	now := time.Now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type trendRow struct {
		view    dashboard.AppCostTrendMonthView
		compute int64
		egress  int64
	}
	rows := make([]trendRow, monthCount)
	maxMagnitude := float64(0)
	for offset := 0; offset < monthCount; offset++ {
		monthStart := selectedStart.AddDate(0, -offset, 0)
		index := monthCount - offset - 1
		row := trendRow{view: dashboard.AppCostTrendMonthView{
			Month:    monthStart.Format("January 2006"),
			MonthURL: "/dashboard/apps/" + url.PathEscape(app.Slug) + "?" + dashboardAppDetailRefreshQuery(monthStart.Format("2006-01"), groupBy, selectedRoute, selectedMethod),
			Selected: monthStart.Equal(selectedStart), Current: monthStart.Year() == now.Year() && monthStart.Month() == now.Month(),
		}}
		if readCtx.Err() != nil {
			row.view.Unavailable = "Cost history unavailable"
			row.view.CoverageStatus = "Unavailable"
			rows[index] = row
			continue
		}
		accountReport, err := billing.ReadFinancialCosts(readCtx, s.store, acct.ID, monthStart, now)
		if err != nil {
			log.Warn("dashboard app cost trend unavailable", "account_id", acct.ID, "app_id", app.ID, "month", monthStart.Format("2006-01"), "err", err)
			row.view.Unavailable = "Cost history unavailable"
			row.view.CoverageStatus = "Unavailable"
			rows[index] = row
			continue
		}
		scoped, err := billing.ScopeFinancialCostsToApp(accountReport, app.ID, app.Slug)
		if err != nil {
			log.Warn("dashboard app cost trend projection failed", "account_id", acct.ID, "app_id", app.ID, "month", monthStart.Format("2006-01"), "err", err)
			row.view.Unavailable = "Cost history unavailable"
			row.view.CoverageStatus = "Unavailable"
			rows[index] = row
			continue
		}
		ready := dashboardAppCostCoverageAdequate(scoped)
		seenCompute, seenEgress := false, false
		coverage := make([]string, 0, len(scoped.Meters))
		for _, meter := range scoped.Meters {
			coverage = append(coverage, meter.Meter+": "+dashboardAppCostCoverage(meter.Coverage))
			switch meter.Meter {
			case "compute":
				row.compute = meter.NetMillicents
				seenCompute = true
			case "egress":
				row.egress = meter.NetMillicents
				seenEgress = true
			}
		}
		ready = ready && seenCompute && seenEgress
		row.view.CoverageDetails = strings.Join(coverage, "; ")
		row.view.Complete = ready
		if ready {
			row.view.CoverageStatus = "Complete"
			maxMagnitude = math.Max(maxMagnitude, math.Abs(float64(row.compute)))
			maxMagnitude = math.Max(maxMagnitude, math.Abs(float64(row.egress)))
		} else {
			row.view.CoverageStatus = "Partial, stale, or unpriced"
		}
		row.view.ComputeCost = dashboardMoney(row.compute)
		row.view.EgressCost = dashboardMoney(row.egress)
		row.view.TotalCost = dashboardMoney(scoped.KnownUsageMillicents)
		if !ready {
			row.view.ComputeCost += " (partial)"
			row.view.EgressCost += " (partial)"
			row.view.TotalCost += " (partial)"
		}
		row.view.ComputeNegative = row.compute < 0
		row.view.EgressNegative = row.egress < 0
		rows[index] = row
	}
	view := dashboard.AppCostTrendData{Months: make([]dashboard.AppCostTrendMonthView, 0, monthCount)}
	for _, row := range rows {
		if row.view.Complete && maxMagnitude > 0 {
			row.view.ComputeBarWidth = dashboardCostTrendBarWidth(row.compute, maxMagnitude)
			row.view.EgressBarWidth = dashboardCostTrendBarWidth(row.egress, maxMagnitude)
		}
		view.Months = append(view.Months, row.view)
	}
	return view
}

func dashboardCostTrendBarWidth(value int64, maxMagnitude float64) int {
	if value == 0 || maxMagnitude <= 0 {
		return 0
	}
	width := int(math.Round(math.Abs(float64(value)) / maxMagnitude * 100))
	if width < 1 {
		return 1
	}
	return width
}

func dashboardMoney(millicents int64) string {
	return dashboardMoneyBig(big.NewInt(millicents))
}

func dashboardMoneyBig(millicents *big.Int) string {
	const millicentsPerEuro = 100 * api.MillicentsPerCent
	negative := millicents.Sign() < 0
	absolute := new(big.Int).Abs(new(big.Int).Set(millicents))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(absolute, big.NewInt(millicentsPerEuro), remainder)
	sign := ""
	if negative {
		sign = "-"
	}
	return fmt.Sprintf("EUR %s%s.%05d", sign, whole.String(), remainder.Int64())
}

func dashboardBillComponent(component string) string {
	switch component {
	case "subscription_and_addons":
		return "subscription and add-ons"
	case "credits_and_adjustments":
		return "credits and adjustments"
	case "external_and_managed_resource_meters":
		return "external and managed resource usage"
	default:
		return strings.ReplaceAll(component, "_", " ")
	}
}
