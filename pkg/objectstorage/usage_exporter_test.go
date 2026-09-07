package objectstorage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type usageReportExporterFunc func(context.Context, UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error)

func (f usageReportExporterFunc) ExportUsageReports(ctx context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
	return f(ctx, req)
}

func TestExportUsageReportsWritesValidatedBatchAtomically(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	req := UsageReportExportRequest{BackendID: "aws-virginia-v1", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now}
	reports := []api.ObjectStorageUsageReport{{
		AccountID:          "account-1",
		BackendID:          req.BackendID,
		BackendFingerprint: req.BackendFingerprint,
		Source:             "aws-s3-usage-report",
		PeriodStart:        period,
		ObservedAt:         now,
		StoredByteHours:    10,
		RequestCount:       20,
		EgressBytes:        30,
		CostMillicents:     40,
	}}

	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := ExportUsageReports(context.Background(), path, usageReportExporterFunc(func(_ context.Context, got UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		called = true
		if got != req {
			t.Fatalf("request = %#v, want %#v", got, req)
		}
		return reports, nil
	}), req)
	if err != nil {
		t.Fatalf("ExportUsageReports() error = %v", err)
	}
	if !called {
		t.Fatal("exporter was not called")
	}
	got, err := readUsageReports(path)
	if err != nil {
		t.Fatalf("readUsageReports() error = %v", err)
	}
	if len(got) != 1 || got[0] != reports[0] {
		t.Fatalf("reports = %#v, want %#v", got, reports)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != usageReportFileMode {
		t.Fatalf("file mode = %o, want %o", got, usageReportFileMode)
	}
}

func TestExportUsageReportsRejectsDuplicateAccountWithoutReplacingFile(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	req := UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now}
	report := api.ObjectStorageUsageReport{AccountID: "account", BackendID: req.BackendID, BackendFingerprint: req.BackendFingerprint, Source: "provider", PeriodStart: period, ObservedAt: now}
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("previous\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ExportUsageReports(context.Background(), path, usageReportExporterFunc(func(context.Context, UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		return []api.ObjectStorageUsageReport{report, report}, nil
	}), req)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "previous\n" {
		t.Fatalf("destination changed after rejected export: %q", got)
	}
}

func TestWriteUsageReportsAtomicRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	path := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(target, []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := WriteUsageReportsAtomic(path, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "target\n" {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestExportUsageReportsRejectsWrongBackend(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	req := UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now}
	err := ExportUsageReports(context.Background(), filepath.Join(t.TempDir(), "usage.json"), usageReportExporterFunc(func(context.Context, UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		return []api.ObjectStorageUsageReport{{AccountID: "account", BackendID: "other", BackendFingerprint: req.BackendFingerprint, Source: "provider", PeriodStart: period, ObservedAt: now}}, nil
	}), req)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}
