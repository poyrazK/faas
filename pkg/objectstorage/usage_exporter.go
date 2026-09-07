package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	maxUsageReports      = 10000
	maxUsageReportSource = 128
	usageReportFileMode  = 0o600
	usageReportPathLimit = 4096
)

// UsageReportExportRequest identifies one backend and one UTC billing period
// for an operator-owned provider adapter. Provider adapters should fetch their
// authoritative usage data and return one cumulative report per Gregale
// account. They must not infer usage from signed URL counts or inventory
// samples.
type UsageReportExportRequest struct {
	BackendID          string
	BackendFingerprint string
	PeriodStart        time.Time
	ObservedAt         time.Time
}

// UsageReportExporter is the narrow seam for provider-specific billing
// adapters. The generic S3 driver intentionally does not implement it: S3
// protocol operations do not expose portable request, egress, or cost data.
type UsageReportExporter interface {
	ExportUsageReports(context.Context, UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error)
}

// ExportUsageReports runs an adapter and atomically publishes its normalized
// reports to the backend's configured usage_reports_path. The destination is
// replaced only after the complete JSON document has been written and synced,
// so apid never observes a partially written report during its minute sweep.
func ExportUsageReports(ctx context.Context, path string, exporter UsageReportExporter, req UsageReportExportRequest) error {
	if exporter == nil {
		return errors.New("object storage: usage exporter is nil")
	}
	if err := validateUsageReportExportRequest(req); err != nil {
		return err
	}
	reports, err := exporter.ExportUsageReports(ctx, req)
	if err != nil {
		return err
	}
	if err := validateExportedUsageReports(reports, req); err != nil {
		return err
	}
	return WriteUsageReportsAtomic(path, reports)
}

// WriteUsageReportsAtomic writes a complete normalized report array to an
// operator-owned path. Reports are deliberately written with owner-only
// permissions because they contain tenant identifiers and provider costs.
// The caller should use a path that apid can read; it must not be writable by
// customer workloads.
func WriteUsageReportsAtomic(path string, reports []api.ObjectStorageUsageReport) error {
	if !validUsageReportPath(path) || len(reports) > maxUsageReports {
		return ErrInvalid
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return ErrInvalid
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInvalid
	}

	data, err := json.Marshal(reports)
	if err != nil {
		return ErrInvalid
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gregale-object-storage-usage-*")
	if err != nil {
		return ErrUnavailable
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(usageReportFileMode); err != nil {
		return ErrUnavailable
	}
	if _, err := tmp.Write(data); err != nil {
		return ErrUnavailable
	}
	if err := tmp.Sync(); err != nil {
		return ErrUnavailable
	}
	if err := tmp.Close(); err != nil {
		return ErrUnavailable
	}
	if err := os.Rename(tmpName, path); err != nil {
		return ErrUnavailable
	}
	removeTemp = false
	// Syncing the directory makes the rename durable across a host restart on
	// filesystems that support directory fsync. The report is still valid when
	// a platform declines this optional operation.
	//nolint:forbidigo // dir is the parent of the trusted operator-configured export path; syncing it is only for rename durability.
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func validateUsageReportExportRequest(req UsageReportExportRequest) error {
	if req.BackendID == "" || req.BackendFingerprint == "" || len(req.BackendFingerprint) > 128 {
		return ErrInvalid
	}
	period := utcMonth(req.PeriodStart)
	if period.IsZero() || !req.PeriodStart.Equal(period) || req.ObservedAt.IsZero() || req.ObservedAt.After(time.Now().UTC()) {
		return ErrInvalid
	}
	return nil
}

func validateExportedUsageReports(reports []api.ObjectStorageUsageReport, req UsageReportExportRequest) error {
	period := utcMonth(req.PeriodStart)
	seen := make(map[string]struct{}, len(reports))
	for _, report := range reports {
		if report.AccountID == "" || report.BackendID != req.BackendID || report.BackendFingerprint != req.BackendFingerprint || report.Source == "" || len(report.Source) > maxUsageReportSource || !report.PeriodStart.Equal(period) || !report.ObservedAt.Equal(req.ObservedAt) || report.ObservedAt.After(time.Now().UTC()) {
			return ErrInvalid
		}
		for _, value := range []int64{report.StoredByteHours, report.RequestCount, report.EgressBytes, report.CostMillicents} {
			if value < 0 || value > api.MaxObjectStoragePolicyValue {
				return ErrInvalid
			}
		}
		if _, ok := seen[report.AccountID]; ok {
			return ErrConflict
		}
		seen[report.AccountID] = struct{}{}
	}
	return nil
}

func validUsageReportPath(path string) bool {
	return path != "" && len(path) <= usageReportPathLimit && filepath.IsAbs(path) && !strings.HasSuffix(path, string(filepath.Separator))
}

func utcMonth(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}
