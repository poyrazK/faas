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
