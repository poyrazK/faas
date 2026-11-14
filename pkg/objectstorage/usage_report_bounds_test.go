package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestUsageReportSerializedBoundary(t *testing.T) {
	period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	req := UsageReportExportRequest{BackendID: "managed-virginia-v1", BackendFingerprint: strings.Repeat("a", 64), PeriodStart: period, ObservedAt: period.Add(24*time.Hour + 123456*time.Microsecond)}
	rows := make([]api.ObjectStorageUsageReport, api.MaxObjectStorageUsageReportRows)
	for i := range rows {
		rows[i] = api.ObjectStorageUsageReport{AccountID: fmt.Sprintf("%08x-0000-4000-8000-000000000000", i), BackendID: req.BackendID, BackendFingerprint: req.BackendFingerprint, Source: "ovh-public-cloud+server-access-logs", PeriodStart: period, ObservedAt: req.ObservedAt, StoredByteHours: 1000000000000, RequestCount: 100000, EgressBytes: 1000000000000, CostMillicents: 10000000}
	}
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("previous complete report\n"), 0600); err != nil {
		t.Fatal(err)
	}
	exporter := usageReportExporterFunc(func(context.Context, UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
		return rows, nil
	})
	if err := ExportUsageReports(t.Context(), path, exporter, req); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized validated batch published", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "previous complete report\n" {
		t.Fatal("oversized batch destroyed previous report", err)
	}
	// Adjust legal source labels to exercise the exact serialized boundary,
	// including the writer's final newline, without changing any limits.
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	delta := len(encoded) + 1 - api.MaxObjectStorageUsageReportBytes
	for i := range rows {
		if delta <= 0 {
			break
		}
		remove := min(delta, len(rows[i].Source)-1)
		rows[i].Source = rows[i].Source[:len(rows[i].Source)-remove]
		delta -= remove
	}
	if delta != 0 {
		t.Fatal("fixture cannot reach the boundary", delta)
	}
	if err := ExportUsageReports(t.Context(), path, exporter, req); err != nil {
		t.Fatal("exact boundary refused", err)
	}
	previous, err := os.ReadFile(path)
	if err != nil || len(previous) != api.MaxObjectStorageUsageReportBytes {
		t.Fatal("boundary size", len(previous), err)
	}
	got, err := readUsageReports(path)
	if err != nil || len(got) != len(rows) {
		t.Fatal("accepted export not importable", len(got), err)
	}
	rows[0].Source += "x"
	if err := ExportUsageReports(t.Context(), path, exporter, req); !errors.Is(err, ErrInvalid) {
		t.Fatal("one-byte overflow published", err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(previous) {
		t.Fatal("overflow replaced boundary report", err)
	}
	rows = append(rows, rows[0])
	rows[len(rows)-1].AccountID = "ffffffff-0000-4000-8000-000000000000"
	if err := ExportUsageReports(t.Context(), path, exporter, req); !errors.Is(err, ErrInvalid) {
		t.Fatal("row overflow published", err)
	}
}

func TestUsageReportActualObservationValidation(t *testing.T) {
	now := time.Now().UTC()
	period := utcMonth(now)
	observed := period.Add(now.Sub(period) / 2)
	req := UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now}
	base := api.ObjectStorageUsageReport{AccountID: "account", BackendID: req.BackendID, BackendFingerprint: req.BackendFingerprint, Source: "qualified", PeriodStart: period, ObservedAt: observed}
	if err := validateExportedUsageReports([]api.ObjectStorageUsageReport{base}, req); err != nil {
		t.Fatal("native observation cannot be retained", err)
	}
	for _, kind := range []string{"future", "zero", "before month", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			row := base
			switch kind {
			case "future":
				row.ObservedAt = now.Add(time.Hour)
			case "zero":
				row.ObservedAt = time.Time{}
			case "before month":
				row.ObservedAt = period.Add(-time.Second)
			case "mixed":
				row.ObservedAt = observed.Add(time.Second)
			}
			rows := []api.ObjectStorageUsageReport{row}
			if kind == "mixed" {
				row.AccountID = "second"
				rows = []api.ObjectStorageUsageReport{base, row}
			}
			if err := validateExportedUsageReports(rows, req); err == nil {
				t.Fatal("invalid observation published", rows)
			}
		})
	}
}
