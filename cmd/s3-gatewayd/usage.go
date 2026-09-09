package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func usageExportJobs(registry *objectstorage.Registry, store state.ObjectStorageProviderUsageStore, getenv func(string) string) ([]objectstorage.UsageExportJob, error) {
	if registry == nil || store == nil || getenv == nil {
		return nil, errors.New("s3-gatewayd: usage exporter dependencies are missing")
	}
	var jobs []objectstorage.UsageExportJob
	for _, backend := range registry.Backends() {
		if backend.Usage.Driver == "" {
			continue
		}
		switch backend.Usage.Driver {
		case "ovh":
			logStore, logStoreOK := backend.Provider.(objectstorage.AccessLogObjectStore)
			if !logStoreOK || backend.Usage.RequestLogBucket == "" {
				return nil, fmt.Errorf("s3-gatewayd: backend %s requires an S3 access-log bucket", backend.ID)
			}
			serviceName := backend.Namespace
			if backend.Usage.ServiceNameEnv != "" {
				serviceName = getenv(backend.Usage.ServiceNameEnv)
			}
			applicationKey := getenv(backend.Usage.ApplicationKeyEnv)
			applicationSecret := getenv(backend.Usage.ApplicationSecretEnv)
			consumerKey := getenv(backend.Usage.ConsumerKeyEnv)
			if serviceName == "" || applicationKey == "" || applicationSecret == "" || consumerKey == "" {
				return nil, fmt.Errorf("s3-gatewayd: backend %s has incomplete ovh usage credentials", backend.ID)
			}
			client := &objectstorage.OVHUsageClient{
				ServiceName: serviceName, ApplicationKey: applicationKey,
				ApplicationSecret: applicationSecret, ConsumerKey: consumerKey,
			}
			catalog := func(ctx context.Context, backendID, fingerprint string) ([]objectstorage.OVHUsageBucket, error) {
				buckets, err := store.ListObjectStorageProviderBuckets(ctx, backendID, fingerprint)
				if err != nil {
					return nil, err
				}
				out := make([]objectstorage.OVHUsageBucket, 0, len(buckets))
				for _, bucket := range buckets {
					if bucket.AccountID == "" || bucket.PhysicalName == "" {
						return nil, objectstorage.ErrInvalid
					}
					out = append(out, objectstorage.OVHUsageBucket{AccountID: bucket.AccountID, PhysicalName: bucket.PhysicalName})
				}
				return out, nil
			}
			exporter := objectstorage.OVHUsageExporter{
				API:     client,
				Catalog: catalog,
				RequestMetrics: func(ctx context.Context, backendID string, periodStart, observedAt time.Time) ([]objectstorage.OVHRequestMetric, error) {
					buckets, err := catalog(ctx, backendID, backend.Fingerprint)
					if err != nil {
						return nil, err
					}
					return objectstorage.OVHAccessLogRequestMetrics{
						Store: logStore, LogBucket: backend.Usage.RequestLogBucket,
						LogPrefix: backend.Usage.RequestLogPrefix,
					}.Metrics(ctx, periodStart, observedAt, buckets)
				},
				Source: "ovh-public-cloud+server-access-logs",
			}
			jobs = append(jobs, objectstorage.UsageExportJob{
				BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
				ReportPath: backend.UsageReportsPath, Exporter: exporter,
				CoverageLag: time.Hour,
			})
		default:
			return nil, fmt.Errorf("s3-gatewayd: unsupported usage driver %q for backend %s", backend.Usage.Driver, backend.ID)
		}
	}
	return jobs, nil
}
