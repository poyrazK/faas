package objectstorage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type gcsUsageSourceFunc func(context.Context, string, time.Time, time.Time) (GCSUsageSnapshot, error)

func (f gcsUsageSourceFunc) UsageForPeriod(ctx context.Context, project string, from, to time.Time) (GCSUsageSnapshot, error) {
	return f(ctx, project, from, to)
}

func gcsUsageTestRequest() UsageReportExportRequest {
	return UsageReportExportRequest{
		BackendID: "gcs-europe", BackendFingerprint: "fingerprint",
		PeriodStart: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ObservedAt:  time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC),
	}
}

func TestGCSUsageExporterCoalescesCatalogBuckets(t *testing.T) {
	req := gcsUsageTestRequest()
	exporter := GCSUsageExporter{
		ProjectID: "prod-project",
		Catalog: func(_ context.Context, backendID, fingerprint string) ([]GCSUsageBucket, error) {
			if backendID != req.BackendID || fingerprint != req.BackendFingerprint {
				t.Fatalf("catalog placement = %q, %q", backendID, fingerprint)
			}
			return []GCSUsageBucket{
				{AccountID: "account-a", PhysicalName: "bucket-a1"},
				{AccountID: "account-a", PhysicalName: "bucket-a2"},
				{AccountID: "account-b", PhysicalName: "bucket-b"},
			}, nil
		},
		Source: gcsUsageSourceFunc(func(_ context.Context, project string, from, to time.Time) (GCSUsageSnapshot, error) {
			if project != "prod-project" || !from.Equal(req.PeriodStart) || !to.Equal(req.ObservedAt) {
				t.Fatalf("source scope = %q, %s, %s", project, from, to)
			}
			return GCSUsageSnapshot{
				ProjectID: project, PeriodStart: from, ObservedAt: to, Currency: "EUR",
				Buckets: []GCSBucketUsage{
					{PhysicalName: "bucket-b", StoredByteHours: 3, RequestCount: 4, EgressBytes: 5, CostMillicents: 6},
					{PhysicalName: "bucket-a2", StoredByteHours: 7, RequestCount: 8, EgressBytes: 9, CostMillicents: 10},
					{PhysicalName: "bucket-a1", StoredByteHours: 11, RequestCount: 12, EgressBytes: 13, CostMillicents: 14},
				},
			}, nil
		}),
	}
	reports, err := exporter.ExportUsageReports(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %#v, want two account rows", reports)
	}
	got := reports[0]
	if got.AccountID != "account-a" || got.StoredByteHours != 18 || got.RequestCount != 20 || got.EgressBytes != 22 || got.CostMillicents != 24 {
		t.Fatalf("account-a report = %#v", got)
	}
	if got.BackendID != req.BackendID || got.BackendFingerprint != req.BackendFingerprint || got.Source != "gcs-monitoring+cloud-billing-detailed" || !got.PeriodStart.Equal(req.PeriodStart) || !got.ObservedAt.Equal(req.ObservedAt) {
		t.Fatalf("account-a report identity = %#v", got)
	}
	if got := reports[1]; got.AccountID != "account-b" || got.RequestCount != 4 {
		t.Fatalf("account-b report = %#v", got)
	}
}

func TestGCSUsageExporterFailsClosedOnIncompleteMeasurements(t *testing.T) {
	req := gcsUsageTestRequest()
	base := GCSUsageSnapshot{
		ProjectID: "prod-project", PeriodStart: req.PeriodStart,
		ObservedAt: req.ObservedAt, Currency: "EUR",
		Buckets: []GCSBucketUsage{{PhysicalName: "bucket-a", RequestCount: 1}},
	}
	tests := []struct {
		name string
		edit func(*GCSUsageSnapshot)
		want error
	}{
		{name: "wrong project", edit: func(s *GCSUsageSnapshot) { s.ProjectID = "other-project" }, want: ErrGCSUsageIncomplete},
		{name: "wrong period", edit: func(s *GCSUsageSnapshot) { s.PeriodStart = s.PeriodStart.AddDate(0, -1, 0) }, want: ErrGCSUsageIncomplete},
		{name: "stale coverage", edit: func(s *GCSUsageSnapshot) { s.ObservedAt = s.ObservedAt.Add(-time.Hour) }, want: ErrGCSUsageIncomplete},
		{name: "TRY currency", edit: func(s *GCSUsageSnapshot) { s.Currency = "TRY" }, want: ErrGCSUsageIncomplete},
		{name: "missing bucket", edit: func(s *GCSUsageSnapshot) { s.Buckets = nil }, want: ErrGCSUsageIncomplete},
		{name: "unknown bucket", edit: func(s *GCSUsageSnapshot) { s.Buckets[0].PhysicalName = "other-bucket" }, want: ErrGCSUsageIncomplete},
		{name: "negative measurement", edit: func(s *GCSUsageSnapshot) { s.Buckets[0].EgressBytes = -1 }, want: ErrGCSUsageIncomplete},
		{name: "over limit", edit: func(s *GCSUsageSnapshot) { s.Buckets[0].CostMillicents = api.MaxObjectStoragePolicyValue + 1 }, want: ErrGCSUsageIncomplete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			snapshot.Buckets = append([]GCSBucketUsage(nil), base.Buckets...)
			tc.edit(&snapshot)
			exporter := GCSUsageExporter{
				ProjectID: "prod-project",
				Catalog: func(context.Context, string, string) ([]GCSUsageBucket, error) {
					return []GCSUsageBucket{{AccountID: "account-a", PhysicalName: "bucket-a"}}, nil
				},
				Source: gcsUsageSourceFunc(func(context.Context, string, time.Time, time.Time) (GCSUsageSnapshot, error) {
					return snapshot, nil
				}),
			}
			_, err := exporter.ExportUsageReports(context.Background(), req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestGCSUsageExporterRejectsDuplicateBucketAndCatalog(t *testing.T) {
	req := gcsUsageTestRequest()
	source := gcsUsageSourceFunc(func(context.Context, string, time.Time, time.Time) (GCSUsageSnapshot, error) {
		return GCSUsageSnapshot{ProjectID: "prod-project", PeriodStart: req.PeriodStart, ObservedAt: req.ObservedAt, Currency: "EUR", Buckets: []GCSBucketUsage{{PhysicalName: "bucket-a"}, {PhysicalName: "bucket-a"}}}, nil
	})
	for _, tc := range []struct {
		name    string
		catalog []GCSUsageBucket
	}{
		{name: "duplicate provider row", catalog: []GCSUsageBucket{{AccountID: "account-a", PhysicalName: "bucket-a"}, {AccountID: "account-b", PhysicalName: "bucket-b"}}},
		{name: "duplicate catalog binding", catalog: []GCSUsageBucket{{AccountID: "account-a", PhysicalName: "bucket-a"}, {AccountID: "account-b", PhysicalName: "bucket-a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exporter := GCSUsageExporter{
				ProjectID: "prod-project", Source: source,
				Catalog: func(context.Context, string, string) ([]GCSUsageBucket, error) { return tc.catalog, nil },
			}
			_, err := exporter.ExportUsageReports(context.Background(), req)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("error = %v, want ErrConflict", err)
			}
		})
	}
}
