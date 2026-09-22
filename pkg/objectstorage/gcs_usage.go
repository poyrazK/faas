package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrGCSUsageIncomplete = errors.New("object storage: incomplete GCS provider usage")

// GCSUsageBucket binds an immutable physical GCS bucket to its Gregale owner.
// The binding must come from the durable object-bucket catalog, not a metric or
// billing row that could be attributed to the wrong tenant.
type GCSUsageBucket struct {
	AccountID    string
	PhysicalName string
}

// GCSBucketUsage is one complete, cumulative UTC-month measurement for a
// physical bucket. Even a zero-usage bucket must have an explicit row. The
// source must include direct signed-URL traffic and all attributable provider
// charges; a gateway request counter or inventory sample cannot fill gaps.
type GCSBucketUsage struct {
	PhysicalName    string
	StoredByteHours int64
	RequestCount    int64
	EgressBytes     int64
	CostMillicents  int64
}

// GCSUsageSnapshot carries the provider's actual coverage watermark. It must
// not be advanced to the exporter's wall clock when Monitoring or Cloud Billing
// is delayed. Costs must already be converted to EUR by an approved, auditable
// operator policy; unsupported currency must cause the source to fail.
type GCSUsageSnapshot struct {
	ProjectID   string
	PeriodStart time.Time
	ObservedAt  time.Time
	Currency    string
	Buckets     []GCSBucketUsage
}

type GCSUsageSource interface {
	UsageForPeriod(context.Context, string, time.Time, time.Time) (GCSUsageSnapshot, error)
}

type GCSUsageBucketCatalog func(context.Context, string, string) ([]GCSUsageBucket, error)

// GCSUsageExporter enforces provider/catalog attribution and completeness
// before the generic atomic report publisher sees any rows. It is deliberately
// not wired into s3-gatewayd until a qualified Monitoring + detailed Cloud
// Billing source and EUR conversion policy are configured.
type GCSUsageExporter struct {
	ProjectID string
	Source    GCSUsageSource
	Catalog   GCSUsageBucketCatalog
}

func (e GCSUsageExporter) ExportUsageReports(ctx context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
	if e.ProjectID == "" || e.Source == nil || e.Catalog == nil {
		return nil, ErrConfiguration
	}
	if err := validateUsageReportExportRequest(req); err != nil {
		return nil, err
	}
	buckets, err := e.Catalog(ctx, req.BackendID, req.BackendFingerprint)
	if err != nil {
		return nil, fmt.Errorf("object storage: list GCS bucket catalog: %w", err)
	}
	owners := make(map[string]string, len(buckets))
	for _, bucket := range buckets {
		if bucket.AccountID == "" || bucket.PhysicalName == "" {
			return nil, ErrGCSUsageIncomplete
		}
		if _, exists := owners[bucket.PhysicalName]; exists {
			return nil, ErrConflict
		}
		owners[bucket.PhysicalName] = bucket.AccountID
	}
	snapshot, err := e.Source.UsageForPeriod(ctx, e.ProjectID, req.PeriodStart, req.ObservedAt)
	if err != nil {
		return nil, fmt.Errorf("object storage: read GCS provider usage: %w", err)
	}
	if snapshot.ProjectID != e.ProjectID || !snapshot.PeriodStart.Equal(req.PeriodStart) || !snapshot.ObservedAt.Equal(req.ObservedAt) || snapshot.Currency != "EUR" {
		return nil, ErrGCSUsageIncomplete
	}
	if len(snapshot.Buckets) != len(owners) {
		return nil, ErrGCSUsageIncomplete
	}
	seen := make(map[string]struct{}, len(snapshot.Buckets))
	reports := make([]api.ObjectStorageUsageReport, 0, len(snapshot.Buckets))
	for _, measurement := range snapshot.Buckets {
		accountID, ok := owners[measurement.PhysicalName]
		if !ok || measurement.PhysicalName == "" {
			return nil, ErrGCSUsageIncomplete
		}
		if _, duplicate := seen[measurement.PhysicalName]; duplicate {
			return nil, ErrConflict
		}
		seen[measurement.PhysicalName] = struct{}{}
		for _, value := range []int64{measurement.StoredByteHours, measurement.RequestCount, measurement.EgressBytes, measurement.CostMillicents} {
			if value < 0 || value > api.MaxObjectStoragePolicyValue {
				return nil, ErrGCSUsageIncomplete
			}
		}
		reports = append(reports, api.ObjectStorageUsageReport{
			AccountID: accountID, BackendID: req.BackendID,
			BackendFingerprint: req.BackendFingerprint,
			Source:             "gcs-monitoring+cloud-billing-detailed",
			PeriodStart:        req.PeriodStart, ObservedAt: req.ObservedAt,
			StoredByteHours: measurement.StoredByteHours, RequestCount: measurement.RequestCount,
			EgressBytes: measurement.EgressBytes, CostMillicents: measurement.CostMillicents,
		})
	}
	// The same account can own multiple physical buckets, but the import format
	// permits exactly one cumulative row per account/backend/period.
	return coalesceUsageReports(reports)
}

var _ UsageReportExporter = GCSUsageExporter{}
