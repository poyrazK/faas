package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardCostCSVRecordType = iota
	dashboardCostCSVAppSlug
	dashboardCostCSVCurrency
	dashboardCostCSVPeriodStart
	dashboardCostCSVPeriodEnd
	dashboardCostCSVAsOf
	dashboardCostCSVScope
	dashboardCostCSVScopeDescription
	dashboardCostCSVKnownUsageCost
	dashboardCostCSVMeter
	dashboardCostCSVDeploymentID
	dashboardCostCSVJobID
	dashboardCostCSVProjectID
	dashboardCostCSVEnvironmentID
	dashboardCostCSVWorkloadName
	dashboardCostCSVQuantity
	dashboardCostCSVUnit
	dashboardCostCSVAttributedCost
	dashboardCostCSVCoverageComplete
	dashboardCostCSVCoverageFresh
	dashboardCostCSVCoverageCompleteMinutes
	dashboardCostCSVCoverageExpectedMinutes
	dashboardCostCSVCoverageReasons
	dashboardCostCSVUnpricedQuantity
	dashboardCostCSVMissingBillComponents
	dashboardCostCSVReportStatus
	dashboardCostCSVReportStatusReason
	dashboardCostCSVColumnCount
)

var dashboardAppCostsCSVHeader = []string{
	"record_type", "app_slug", "currency", "period_start", "period_end_exclusive", "as_of",
	"scope", "scope_description", "known_usage_cost", "meter", "deployment_id",
	"job_id", "project_id", "environment_id", "workload_name", "quantity", "unit",
	"attributed_cost", "coverage_complete", "coverage_fresh", "coverage_complete_minutes",
	"coverage_expected_minutes", "coverage_reasons", "unpriced_quantity", "missing_bill_components",
	"report_status", "report_status_reason",
}

func (s *server) renderAppCostsCSV(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start, err := dashboardCostCSVMonth(r.URL.RawQuery, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	accountReport, err := billing.ReadFinancialCosts(ctx, s.store, acct.ID, start, time.Now().UTC())
	if err != nil {
		log.Warn("dashboard app cost CSV unavailable", "account_id", acct.ID, "app_id", app.ID, "month", start.Format("2006-01"), "err", err)
		api.WriteProblem(w, financialProblem(err))
		return
	}
	appReport, err := billing.ScopeFinancialCostsToApp(accountReport, app.ID, app.Slug)
	if err != nil {
		log.Warn("dashboard app cost CSV projection failed", "account_id", acct.ID, "app_id", app.ID, "month", start.Format("2006-01"), "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not prepare app cost export"))
		return
	}
	body, err := dashboardAppCostsCSV(appReport)
	if err != nil {
		log.Warn("dashboard app cost CSV formatting failed", "account_id", acct.ID, "app_id", app.ID, "month", start.Format("2006-01"), "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not format app cost export"))
		return
	}
	filename := fmt.Sprintf("%s-costs-%s.csv", app.Slug, start.Format("2006-01"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) renderAppCostTrendCSV(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now().UTC()
	start, err := dashboardCostCSVMonth(r.URL.RawQuery, now)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	body, err := s.dashboardAppCostTrendCSV(ctx, log, acct, app, start, now)
	if err != nil {
		log.Warn("dashboard app cost trend CSV formatting failed", "account_id", acct.ID, "app_id", app.ID, "month", start.Format("2006-01"), "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not format app cost trend export"))
		return
	}
	filename := fmt.Sprintf("%s-cost-trend-%s.csv", app.Slug, start.Format("2006-01"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) dashboardAppCostTrendCSV(ctx context.Context, log *slog.Logger, acct state.Account, app state.App, selectedStart, now time.Time) ([]byte, error) {
	var body bytes.Buffer
	writer := csv.NewWriter(&body)
	if err := writer.Write(dashboardAppCostsCSVHeader); err != nil {
		return nil, err
	}
	for offset := 5; offset >= 0; offset-- {
		monthStart := selectedStart.AddDate(0, -offset, 0)
		if err := ctx.Err(); err != nil {
			if err := dashboardAppCostCSVWriteUnavailable(writer, app, monthStart); err != nil {
				return nil, err
			}
			continue
		}
		accountReport, err := billing.ReadFinancialCosts(ctx, s.store, acct.ID, monthStart, now)
		if err != nil {
			log.Warn("dashboard app cost trend CSV month unavailable", "account_id", acct.ID, "app_id", app.ID, "month", monthStart.Format("2006-01"), "err", err)
			if err := dashboardAppCostCSVWriteUnavailable(writer, app, monthStart); err != nil {
				return nil, err
			}
			continue
		}
		appReport, err := billing.ScopeFinancialCostsToApp(accountReport, app.ID, app.Slug)
		if err != nil {
			log.Warn("dashboard app cost trend CSV projection failed", "account_id", acct.ID, "app_id", app.ID, "month", monthStart.Format("2006-01"), "err", err)
			if err := dashboardAppCostCSVWriteUnavailable(writer, app, monthStart); err != nil {
				return nil, err
			}
			continue
		}
		if err := dashboardAppCostsCSVWriteReport(writer, appReport); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func dashboardCostCSVMonth(rawQuery string, now time.Time) (time.Time, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid export query encoding")
	}
	for key, values := range query {
		if key != "cost_month" || len(values) != 1 {
			return time.Time{}, fmt.Errorf("expected at most one cost_month parameter")
		}
	}
	month, notice := dashboardCostMonth(query, now)
	if notice != "" {
		return time.Time{}, fmt.Errorf("expected a current or historical month in YYYY-MM format")
	}
	return month, nil
}

func dashboardAppCostsCSV(report api.FinancialAppCostsResponse) ([]byte, error) {
	var body bytes.Buffer
	writer := csv.NewWriter(&body)
	if err := writer.Write(dashboardAppCostsCSVHeader); err != nil {
		return nil, err
	}
	if err := dashboardAppCostsCSVWriteReport(writer, report); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func dashboardAppCostsCSVWriteReport(writer *csv.Writer, report api.FinancialAppCostsResponse) error {
	metadata := dashboardAppCostsCSVRow(report, "report")
	metadata[dashboardCostCSVKnownUsageCost] = dashboardCSVMoney(report.KnownUsageMillicents)
	metadata[dashboardCostCSVMissingBillComponents] = dashboardCSVText(dashboardCSVBillComponents(report.MissingBillComponents))
	if err := writer.Write(metadata); err != nil {
		return err
	}
	for _, meter := range report.Meters {
		meterRow := dashboardAppCostsCSVRow(report, "meter_total")
		dashboardAppCostsCSVSetMeter(meterRow, meter.Meter, meter.Unit, meter.Quantity, meter.NetMillicents, meter.Coverage)
		if err := writer.Write(meterRow); err != nil {
			return err
		}
		for _, allocation := range meter.Allocations {
			row := dashboardAppCostsCSVRow(report, "workload")
			dashboardAppCostsCSVSetMeter(row, meter.Meter, allocation.Unit, allocation.Quantity, allocation.NetMillicents, meter.Coverage)
			row[dashboardCostCSVDeploymentID] = dashboardCSVText(allocation.Attribution.DeploymentID)
			row[dashboardCostCSVJobID] = dashboardCSVText(allocation.Attribution.JobID)
			row[dashboardCostCSVProjectID] = dashboardCSVText(allocation.Attribution.ProjectID)
			row[dashboardCostCSVEnvironmentID] = dashboardCSVText(allocation.Attribution.EnvironmentID)
			name := allocation.Attribution.Name
			if name == "" {
				name = allocation.Attribution.DeploymentID
			}
			if name == "" {
				name = report.AppSlug
			}
			row[dashboardCostCSVWorkloadName] = dashboardCSVText(name)
			if err := writer.Write(row); err != nil {
				return err
			}
		}
	}
	return nil
}

func dashboardAppCostCSVWriteUnavailable(writer *csv.Writer, app state.App, monthStart time.Time) error {
	row := make([]string, dashboardCostCSVColumnCount)
	row[dashboardCostCSVRecordType] = "unavailable"
	row[dashboardCostCSVAppSlug] = dashboardCSVText(app.Slug)
	row[dashboardCostCSVCurrency] = "EUR"
	row[dashboardCostCSVPeriodStart] = dashboardCSVTime(monthStart)
	row[dashboardCostCSVPeriodEnd] = dashboardCSVTime(monthStart.AddDate(0, 1, 0))
	row[dashboardCostCSVScope] = "application_attributed_retained_compute_and_interface_egress"
	row[dashboardCostCSVScopeDescription] = "Retained usage allocations attributed to this application; meter coverage is account-wide."
	row[dashboardCostCSVReportStatus] = "unavailable"
	row[dashboardCostCSVReportStatusReason] = "Retained cost data is temporarily unavailable."
	return writer.Write(row)
}

func dashboardAppCostCSVReportStatus(report api.FinancialAppCostsResponse) (string, string) {
	if !dashboardAppCostCoverageAdequate(report) {
		return "partial", "See per-meter coverage fields."
	}
	seenCompute, seenEgress := false, false
	for _, meter := range report.Meters {
		switch meter.Meter {
		case "compute":
			seenCompute = true
		case "egress":
			seenEgress = true
		}
	}
	if !seenCompute || !seenEgress {
		return "partial", "One or more expected meters are missing."
	}
	return "complete", ""
}

func dashboardAppCostsCSVRow(report api.FinancialAppCostsResponse, recordType string) []string {
	row := make([]string, dashboardCostCSVColumnCount)
	row[dashboardCostCSVRecordType] = recordType
	row[dashboardCostCSVAppSlug] = dashboardCSVText(report.AppSlug)
	row[dashboardCostCSVCurrency] = dashboardCSVText(report.Currency)
	row[dashboardCostCSVPeriodStart] = dashboardCSVTime(report.PeriodStart)
	row[dashboardCostCSVPeriodEnd] = dashboardCSVTime(report.PeriodEnd)
	row[dashboardCostCSVAsOf] = dashboardCSVTime(report.AsOf)
	row[dashboardCostCSVScope] = dashboardCSVText(report.Scope)
	row[dashboardCostCSVScopeDescription] = dashboardCSVText(report.ScopeDescription)
	row[dashboardCostCSVReportStatus], row[dashboardCostCSVReportStatusReason] = dashboardAppCostCSVReportStatus(report)
	return row
}

func dashboardAppCostsCSVSetMeter(row []string, meter, unit string, quantity, attributedCost int64, coverage api.FinancialMeterCoverage) {
	row[dashboardCostCSVMeter] = dashboardCSVText(meter)
	row[dashboardCostCSVQuantity] = strconv.FormatInt(quantity, 10)
	row[dashboardCostCSVUnit] = dashboardCSVText(unit)
	row[dashboardCostCSVAttributedCost] = dashboardCSVMoney(attributedCost)
	row[dashboardCostCSVCoverageComplete] = strconv.FormatBool(coverage.Complete)
	row[dashboardCostCSVCoverageFresh] = strconv.FormatBool(coverage.Fresh)
	row[dashboardCostCSVCoverageCompleteMinutes] = strconv.FormatInt(coverage.CompleteMinutes, 10)
	row[dashboardCostCSVCoverageExpectedMinutes] = strconv.FormatInt(coverage.ExpectedMinutes, 10)
	row[dashboardCostCSVCoverageReasons] = dashboardCSVText(strings.Join(coverage.Reasons, "; "))
	row[dashboardCostCSVUnpricedQuantity] = strconv.FormatInt(coverage.UnpricedQuantity, 10)
}

func dashboardCSVMoney(millicents int64) string {
	return strings.TrimPrefix(dashboardMoney(millicents), "EUR ")
}

func dashboardCSVTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func dashboardCSVBillComponents(components []string) string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, dashboardBillComponent(component))
	}
	return strings.Join(names, "; ")
}

func dashboardCSVText(value string) string {
	first := strings.TrimLeft(value, " \t\r\n\v\f\uFEFF")
	if first != "" && strings.ContainsRune("=+-@", rune(first[0])) {
		return "'" + value
	}
	return value
}
