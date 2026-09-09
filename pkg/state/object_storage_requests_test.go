package state

import (
	"context"
	"testing"
	"time"
)

func TestMemObjectStorageProviderRequestMetricsAreCumulative(t *testing.T) {
	store := NewMemStore()
	store.objectBuckets = map[string]ObjectBucket{
		"bucket": {ID: "bucket", AccountID: "account", BackendID: "ovh", BackendFingerprint: "fingerprint", PhysicalName: "physical", State: "ready"},
	}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := store.RecordObjectStorageProviderRequest(context.Background(), "bucket", period.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordObjectStorageProviderRequest(context.Background(), "bucket", period.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	metrics, err := store.ListObjectStorageProviderRequestMetrics(context.Background(), "ovh", "fingerprint", period)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].PhysicalName != "physical" || metrics[0].RequestCount != 2 {
		t.Fatalf("metrics = %+v, want one physical bucket with count 2", metrics)
	}
	buckets, err := store.ListObjectStorageProviderBuckets(context.Background(), "ovh", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].AccountID != "account" {
		t.Fatalf("buckets = %+v", buckets)
	}
}

func TestMemObjectStorageProviderRequestMetricsIncludeZeroBuckets(t *testing.T) {
	store := NewMemStore()
	store.objectBuckets = map[string]ObjectBucket{
		"bucket": {ID: "bucket", AccountID: "account", BackendID: "ovh", BackendFingerprint: "fingerprint", PhysicalName: "physical", State: "ready"},
	}
	metrics, err := store.ListObjectStorageProviderRequestMetrics(context.Background(), "ovh", "fingerprint", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].RequestCount != 0 {
		t.Fatalf("metrics = %+v, want explicit zero", metrics)
	}
}
