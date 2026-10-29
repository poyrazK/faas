package objectstorage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunUsageExportsOncePublishesEachJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	job := UsageExportJob{BackendID: "managed", BackendFingerprint: "fingerprint", ReportPath: path, Exporter: usageReportExporterFunc(func(_ context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		return []api.ObjectStorageUsageReport{{AccountID: "account", BackendID: req.BackendID, BackendFingerprint: req.BackendFingerprint, Source: "fixture", PeriodStart: req.PeriodStart, ObservedAt: req.ObservedAt}}, nil
	})}
	if err := RunUsageExportsOnce(context.Background(), []UsageExportJob{job}, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reports []api.ObjectStorageUsageReport
	if err := json.Unmarshal(data, &reports); err != nil || len(reports) != 1 || !reports[0].ObservedAt.Equal(now) {
		t.Fatalf("reports = %s, err=%v", data, err)
	}
}

func TestRunUsageExportsOnceKeepsPreviousReportOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	job := UsageExportJob{BackendID: "managed", BackendFingerprint: "fingerprint", ReportPath: path, Exporter: usageReportExporterFunc(func(context.Context, UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		return nil, ErrUnavailable
	})}
	if err := RunUsageExportsOnce(context.Background(), []UsageExportJob{job}, time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected export error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous" {
		t.Fatalf("previous report changed: %q err=%v", data, err)
	}
}

func TestRunUsageExportsOnceAppliesCoverageLag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var observed time.Time
	job := UsageExportJob{BackendID: "managed", BackendFingerprint: "fingerprint", ReportPath: path, CoverageLag: time.Hour, Exporter: usageReportExporterFunc(func(_ context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		observed = req.ObservedAt
		return []api.ObjectStorageUsageReport{{AccountID: "account", BackendID: req.BackendID, BackendFingerprint: req.BackendFingerprint, Source: "fixture", PeriodStart: req.PeriodStart, ObservedAt: req.ObservedAt}}, nil
	})}
	if err := RunUsageExportsOnce(context.Background(), []UsageExportJob{job}, now); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-time.Hour); !observed.Equal(want) {
		t.Fatalf("observed_at = %s, want %s", observed, want)
	}
}

// Hunt 19 H19-2: a report counts only toward the month it is observed in, and
// the runner reported only the clock's month with the observation clamped to
// its start, so each month's last CoverageLag of usage was never reported.
// The lagged observation now picks the month, and for a few hours into a new
// month the file also carries the previous month's closing report.
func TestRunUsageExportsReportsTheClosedMonthToItsEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	october := september.AddDate(0, 1, 0)
	job := UsageExportJob{BackendID: "managed", BackendFingerprint: "fingerprint", ReportPath: path, CoverageLag: time.Hour, Exporter: usageReportExporterFunc(func(_ context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		return []api.ObjectStorageUsageReport{{AccountID: "account", BackendID: req.BackendID, BackendFingerprint: req.BackendFingerprint, Source: "fixture", PeriodStart: req.PeriodStart, ObservedAt: req.ObservedAt}}, nil
	})}
	read := func(t *testing.T) []api.ObjectStorageUsageReport {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var reports []api.ObjectStorageUsageReport
		if err := json.Unmarshal(data, &reports); err != nil {
			t.Fatal(err)
		}
		return reports
	}

	// Half an hour into October, the lagged observation is still September's.
	if err := RunUsageExportsOnce(context.Background(), []UsageExportJob{job}, october.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := read(t); len(got) != 1 || !got[0].PeriodStart.Equal(september) || !got[0].ObservedAt.Equal(october.Add(-30*time.Minute)) {
		t.Fatalf("reports before the lag clears = %+v, want September observed at 23:30", got)
	}

	// Once it clears, October is reported and September closes at its last microsecond.
	if err := RunUsageExportsOnce(context.Background(), []UsageExportJob{job}, october.Add(70*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := read(t)
	if len(got) != 2 || !got[0].PeriodStart.Equal(october) || !got[0].ObservedAt.Equal(october.Add(10*time.Minute)) ||
		!got[1].PeriodStart.Equal(september) || !got[1].ObservedAt.Equal(october.Add(-time.Microsecond)) {
		t.Fatalf("reports after the lag clears = %+v, want October plus September's closing report", got)
	}

	// After the closing window only the current month is written.
	if err := RunUsageExportsOnce(context.Background(), []UsageExportJob{job}, october.Add(api.ObjectStorageReportCloseWindow+2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := read(t); len(got) != 1 || !got[0].PeriodStart.Equal(october) {
		t.Fatalf("reports after the closing window = %+v, want October only", got)
	}
}
