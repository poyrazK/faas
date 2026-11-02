package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// UsageExportJobs builds the provider usage export of every backend whose
// registry entry names a usage driver. apid runs them with its bucket-admin
// credential, which can read the operator's access-log bucket, and the
// provider account keys from its environment (ADR-20261031122757). A backend
// whose export cannot be built is left out and named in the returned error;
// the others still export.
func UsageExportJobs(registry *Registry, store state.ObjectStorageProviderUsageStore, getenv func(string) string) ([]UsageExportJob, error) {
	if registry == nil || store == nil || getenv == nil {
		return nil, errors.New("object storage: usage exporter dependencies are missing")
	}
	var jobs []UsageExportJob
	var failures []error
	for _, backend := range registry.Backends() {
		if backend.Usage.Driver == "" {
			continue
		}
		job, err := usageExportJob(backend, store, getenv)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, errors.Join(failures...)
}

func usageExportJob(backend Backend, store state.ObjectStorageProviderUsageStore, getenv func(string) string) (UsageExportJob, error) {
	if backend.Usage.Driver != "ovh" {
		return UsageExportJob{}, fmt.Errorf("object storage: unsupported usage driver %q for backend %s", backend.Usage.Driver, backend.ID)
	}
	logStore, logStoreOK := backend.Provider.(AccessLogObjectStore)
	if !logStoreOK || backend.Usage.RequestLogBucket == "" {
		return UsageExportJob{}, fmt.Errorf("object storage: backend %s requires an S3 access-log bucket", backend.ID)
	}
	serviceName := backend.Namespace
	if backend.Usage.ServiceNameEnv != "" {
		serviceName = getenv(backend.Usage.ServiceNameEnv)
	}
	applicationKey := getenv(backend.Usage.ApplicationKeyEnv)
	applicationSecret := getenv(backend.Usage.ApplicationSecretEnv)
	consumerKey := getenv(backend.Usage.ConsumerKeyEnv)
	if serviceName == "" || applicationKey == "" || applicationSecret == "" || consumerKey == "" {
		return UsageExportJob{}, fmt.Errorf("object storage: backend %s has incomplete ovh usage credentials", backend.ID)
	}
	client := &OVHUsageClient{
		ServiceName: serviceName, ApplicationKey: applicationKey,
		ApplicationSecret: applicationSecret, ConsumerKey: consumerKey,
	}
	catalog := func(ctx context.Context, backendID, fingerprint string) ([]OVHUsageBucket, error) {
		buckets, err := store.ListObjectStorageProviderBuckets(ctx, backendID, fingerprint)
		if err != nil {
			return nil, err
		}
		out := make([]OVHUsageBucket, 0, len(buckets))
		for _, bucket := range buckets {
			if bucket.AccountID == "" || bucket.PhysicalName == "" {
				return nil, ErrInvalid
			}
			subject := bucket.UsageSubjectID
			if subject == "" {
				subject = bucket.AccountID
			}
			out = append(out, OVHUsageBucket{AccountID: subject, PhysicalName: bucket.PhysicalName})
		}
		return out, nil
	}
	exporter := OVHUsageExporter{
		API:     client,
		Catalog: catalog,
		RequestMetrics: func(ctx context.Context, backendID string, periodStart, observedAt time.Time) ([]OVHRequestMetric, error) {
			buckets, err := catalog(ctx, backendID, backend.Fingerprint)
			if err != nil {
				return nil, err
			}
			return OVHAccessLogRequestMetrics{
				Store: logStore, LogBucket: backend.Usage.RequestLogBucket,
				LogPrefix: backend.Usage.RequestLogPrefix,
			}.Metrics(ctx, periodStart, observedAt, buckets)
		},
		Source: "ovh-public-cloud+server-access-logs",
	}
	return UsageExportJob{
		BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		ReportPath: backend.UsageReportsPath, Exporter: periodUsageExporter{exporter: exporter},
		CoverageLag: time.Hour,
	}, nil
}

// periodUsageExporter reads the catalog with the report month's allocation
// (state.WithObjectUsagePeriod), so a month's usage goes to the subject the
// bucket belonged to then.
type periodUsageExporter struct {
	exporter UsageReportExporter
}

func (e periodUsageExporter) ExportUsageReports(ctx context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
	return e.exporter.ExportUsageReports(state.WithObjectUsagePeriod(ctx, req.PeriodStart), req)
}
