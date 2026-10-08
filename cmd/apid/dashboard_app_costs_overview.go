package main

import (
	"context"
	"log/slog"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) renderAppCostsOverview(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now().UTC()
	start, selectionNotice := dashboardCostMonth(r.URL.Query(), now)
	sortBy := dashboardAppCostOverviewSort(r.URL.Query().Get("sort"))
	data := dashboard.AppCostsOverviewData{
		Month: start.Format("January 2006"), SelectedMonth: start.Format("2006-01"),
		MaxMonth: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01"),
		Sort:     sortBy, SelectionNotice: selectionNotice,
		Apps: make([]dashboard.AppCostOverviewRow, 0),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard app costs overview app list unavailable", "account_id", acct.ID, "err", err)
		data.Unavailable = "The app list is temporarily unavailable."
	} else {
		report, err := billing.ReadFinancialCosts(ctx, s.store, acct.ID, start, now)
		if err != nil {
			log.Warn("dashboard app costs overview costs unavailable", "account_id", acct.ID, "month", start.Format("2006-01"), "err", err)
			data.Unavailable = "Retained cost data is temporarily unavailable."
		} else {
			data.AsOf = report.AsOf.Format("2006-01-02 15:04 UTC")
			data.KnownUsageTotal = dashboardMoney(report.KnownUsageMillicents)
			data.Apps, data.AppAttributedTotal = s.dashboardAppCostOverviewRows(ctx, log, acct, apps, report, sortBy)
			for _, component := range report.MissingBillComponents {
				data.MissingBillComponents = append(data.MissingBillComponents, dashboardBillComponent(component))
			}
		}
	}
	accountView, _ := AccountFrom(r.Context())
	page := dashboard.Page{
		Title: "App costs", Body: "app_costs_overview",
		Account: dashboardAccountView(accountView, len(apps)), Data: data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardAppCostOverviewSort(value string) string {
	switch value {
	case "compute", "egress", "app":
		return value
	default:
		return "cost"
	}
}

type dashboardAppCostOverviewRankedRow struct {
	view    dashboard.AppCostOverviewRow
	name    string
	total   *big.Int
	compute *big.Int
	egress  *big.Int
}

func (s *server) dashboardAppCostOverviewRows(ctx context.Context, log *slog.Logger, acct state.Account, currentApps []state.App, report api.FinancialCostsResponse, sortBy string) ([]dashboard.AppCostOverviewRow, string) {
	appsByID := make(map[string]state.App, len(currentApps))
	for _, app := range currentApps {
		appsByID[app.ID] = app
	}
	attributedTotal := new(big.Int)
	allocatedIDs := make(map[string]struct{})
	for _, meter := range report.Meters {
		for _, contract := range meter.Accrued.Contracts {
			for _, allocation := range contract.Allocations {
				appID := allocation.Attribution.AppID
				if appID == "" {
					continue
				}
				allocatedIDs[appID] = struct{}{}
				attributedTotal.Add(attributedTotal, big.NewInt(allocation.NetMillicents))
			}
		}
	}

	unresolvedIDs := make(map[string]struct{})
	for appID := range allocatedIDs {
		if _, ok := appsByID[appID]; ok {
			continue
		}
		app, err := s.store.AppByID(ctx, appID)
		if err != nil || app.AccountID != acct.ID {
			log.Warn("dashboard app costs overview app identity unavailable", "account_id", acct.ID, "app_id", appID, "err", err)
			unresolvedIDs[appID] = struct{}{}
			continue
		}
		appsByID[app.ID] = app
	}

	ranked := make([]dashboardAppCostOverviewRankedRow, 0, len(appsByID)+1)
	appIDs := make([]string, 0, len(appsByID))
	for appID := range appsByID {
		appIDs = append(appIDs, appID)
	}
	sort.Strings(appIDs)
	for _, appID := range appIDs {
		app := appsByID[appID]
		appReport, err := billing.ScopeFinancialCostsToApp(report, app.ID, app.Slug)
		if err != nil {
			log.Warn("dashboard app costs overview app projection failed", "account_id", acct.ID, "app_id", app.ID, "month", report.PeriodStart.Format("2006-01"), "err", err)
			unresolvedIDs[app.ID] = struct{}{}
			continue
		}
		item := s.appListItem(ctx, app, nil, time.Time{})
		row := dashboardAppCostOverviewRow(app.Slug, item.URL, string(app.Status), appReport)
		if app.Status == state.AppDeleted {
			row.URL = ""
		}
		ranked = append(ranked, dashboardAppCostOverviewRankedRow{
			view: row, name: app.Slug, total: big.NewInt(appReport.KnownUsageMillicents),
			compute: dashboardAppCostOverviewMeterAmount(appReport, "compute"),
			egress:  dashboardAppCostOverviewMeterAmount(appReport, "egress"),
		})
	}

	if len(unresolvedIDs) > 0 {
		unknownCompute, unknownEgress, unknownTotal := new(big.Int), new(big.Int), new(big.Int)
		for _, meter := range report.Meters {
			meterAmount := new(big.Int)
			for _, contract := range meter.Accrued.Contracts {
				for _, allocation := range contract.Allocations {
					if _, unresolved := unresolvedIDs[allocation.Attribution.AppID]; unresolved {
						meterAmount.Add(meterAmount, big.NewInt(allocation.NetMillicents))
					}
				}
			}
			unknownTotal.Add(unknownTotal, meterAmount)
			switch meter.Meter {
			case "compute":
				unknownCompute.Set(meterAmount)
			case "egress":
				unknownEgress.Set(meterAmount)
			}
		}
		coverage := dashboardAppCostOverviewReportCoverage(report.Meters)
		row := dashboard.AppCostOverviewRow{
			Slug: "App identity unavailable", Lifecycle: "unavailable",
			ComputeCost: dashboardMoneyBig(unknownCompute), EgressCost: dashboardMoneyBig(unknownEgress),
			TotalCost: dashboardMoneyBig(unknownTotal), CoverageStatus: coverage.status,
			CoverageClass: coverage.class, CoverageDetails: coverage.details,
		}
		ranked = append(ranked, dashboardAppCostOverviewRankedRow{
			view: row, name: row.Slug, total: unknownTotal,
			compute: unknownCompute, egress: unknownEgress,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if sortBy == "app" {
			return strings.ToLower(ranked[i].name) < strings.ToLower(ranked[j].name)
		}
		left, right := ranked[i].total, ranked[j].total
		switch sortBy {
		case "compute":
			left, right = ranked[i].compute, ranked[j].compute
		case "egress":
			left, right = ranked[i].egress, ranked[j].egress
		}
		if cmp := left.Cmp(right); cmp != 0 {
			return cmp > 0
		}
		return strings.ToLower(ranked[i].name) < strings.ToLower(ranked[j].name)
	})
	rows := make([]dashboard.AppCostOverviewRow, 0, len(ranked))
	for _, row := range ranked {
		rows = append(rows, row.view)
	}
	return rows, dashboardMoneyBig(attributedTotal)
}

func dashboardAppCostOverviewRow(slug, url, lifecycle string, report api.FinancialAppCostsResponse) dashboard.AppCostOverviewRow {
	coverage := dashboardAppCostOverviewAppCoverage(report.Meters)
	return dashboard.AppCostOverviewRow{
		Slug: slug, URL: url, Lifecycle: lifecycle,
		ComputeCost:    dashboardMoneyBig(dashboardAppCostOverviewMeterAmount(report, "compute")),
		EgressCost:     dashboardMoneyBig(dashboardAppCostOverviewMeterAmount(report, "egress")),
		TotalCost:      dashboardMoney(report.KnownUsageMillicents),
		CoverageStatus: coverage.status, CoverageClass: coverage.class,
		CoverageDetails: coverage.details,
	}
}

func dashboardAppCostOverviewMeterAmount(report api.FinancialAppCostsResponse, meterName string) *big.Int {
	amount := new(big.Int)
	for _, meter := range report.Meters {
		if meter.Meter == meterName {
			amount.Add(amount, big.NewInt(meter.NetMillicents))
		}
	}
	return amount
}

type dashboardAppCostOverviewCoverage struct {
	status  string
	class   string
	details string
}

type dashboardAppCostOverviewCoverageMeter struct {
	meter    string
	coverage api.FinancialMeterCoverage
}

func dashboardAppCostOverviewAppCoverage(meters []api.FinancialAppMeterCosts) dashboardAppCostOverviewCoverage {
	items := make([]dashboardAppCostOverviewCoverageMeter, 0, len(meters))
	for _, meter := range meters {
		items = append(items, dashboardAppCostOverviewCoverageMeter{meter: meter.Meter, coverage: meter.Coverage})
	}
	return dashboardAppCostOverviewCoverageFor(items)
}

func dashboardAppCostOverviewReportCoverage(meters []api.FinancialMeterCosts) dashboardAppCostOverviewCoverage {
	items := make([]dashboardAppCostOverviewCoverageMeter, 0, len(meters))
	for _, meter := range meters {
		items = append(items, dashboardAppCostOverviewCoverageMeter{meter: meter.Meter, coverage: meter.Coverage})
	}
	return dashboardAppCostOverviewCoverageFor(items)
}

func dashboardAppCostOverviewCoverageFor(meters []dashboardAppCostOverviewCoverageMeter) dashboardAppCostOverviewCoverage {
	complete := len(meters) > 0
	seenCompute, seenEgress := false, false
	details := make([]string, 0, len(meters))
	for _, meter := range meters {
		details = append(details, meter.meter+": "+dashboardAppCostCoverage(meter.coverage))
		if !meter.coverage.Complete || !meter.coverage.Fresh || meter.coverage.UnpricedQuantity != 0 {
			complete = false
		}
		switch meter.meter {
		case "compute":
			seenCompute = true
		case "egress":
			seenEgress = true
		}
	}
	if !seenCompute || !seenEgress {
		complete = false
	}
	if complete {
		return dashboardAppCostOverviewCoverage{status: "Complete, fresh, priced", class: "complete", details: strings.Join(details, "; ")}
	}
	return dashboardAppCostOverviewCoverage{status: "Coverage needs review", class: "partial", details: strings.Join(details, "; ")}
}
