package object

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type usageExportHost struct {
	receiptRecoveryHost
	ops *wire.OpsMetrics
}

func (h usageExportHost) Ops() *wire.OpsMetrics { return h.ops }

func usageExportRegistry(t *testing.T, usage bool) *objectstorage.Registry {
	t.Helper()
	backend := objectstorage.BackendConfig{ID: "ovh-a", Driver: "s3", Region: "us-east-1", S3Region: "us-east-va", Namespace: "project-a", Endpoint: "https://s3.example.test", PathStyle: true,
		AccessKeyEnv: "ADMIN_KEY", SecretKeyEnv: "ADMIN_SECRET"}
	if usage {
		backend.UsageReportsPath = "/var/spool/faas/object-storage-usage/ovh-a.json"
		backend.Usage = objectstorage.UsageConfig{Driver: "ovh", ApplicationKeyEnv: "OVH_APP_KEY", ApplicationSecretEnv: "OVH_APP_SECRET", ConsumerKeyEnv: "OVH_CONSUMER_KEY", RequestLogBucket: "logs"}
	}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "ovh-a"}, Backends: []objectstorage.BackendConfig{backend}},
		func(string) string { return "synthetic" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func usageExportGauge(t *testing.T, ops *wire.OpsMetrics, backend string) (float64, bool) {
	t.Helper()
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "faas_object_storage_usage_export_last_success_timestamp_seconds" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "backend" && label.GetValue() == backend {
					return metric.GetGauge().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

// adr: 20261031122757 — apid runs the provider usage exports. A backend
// whose export cannot be built never succeeds, so its last-success time is
// 0 from the start and FaasObjectStorageUsageExportStale fires for it.
func TestRunObjectStorageUsageExports(t *testing.T) {
	run := func(t *testing.T, usage bool) *wire.OpsMetrics {
		t.Helper()
		ops := wire.NewOpsMetrics("apid")
		svc := Service{Host: usageExportHost{receiptRecoveryHost: receiptRecoveryHost{store: state.NewMemStore(), registry: usageExportRegistry(t, usage)}, ops: ops}}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// No provider account keys: no job can be built, so the runner
		// returns instead of exporting.
		svc.RunObjectStorageUsageExports(ctx, func(string) string { return "" })
		if ctx.Err() != nil {
			t.Fatal("the runner kept running with nothing to export")
		}
		return ops
	}

	t.Run("unbuildable export is stale at once", func(t *testing.T) {
		value, ok := usageExportGauge(t, run(t, true), "ovh-a")
		if !ok || value != 0 {
			t.Fatalf("last success for ovh-a = %v (present %v), want 0", value, ok)
		}
	})
	t.Run("no usage driver exports nothing", func(t *testing.T) {
		if _, ok := usageExportGauge(t, run(t, false), "ovh-a"); ok {
			t.Fatal("a backend without a usage driver got an export gauge")
		}
	})
}
