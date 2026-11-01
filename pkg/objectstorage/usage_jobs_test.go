package objectstorage

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func usageJobsRegistry(t *testing.T, backends ...BackendConfig) *Registry {
	t.Helper()
	registry, err := NewRegistry(Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": backends[0].ID}, Backends: backends},
		func(string) string { return "synthetic" }, map[string]Factory{"s3": NewS3})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func ovhUsageBackend(id, keyEnv string) BackendConfig {
	return BackendConfig{ID: id, Driver: "s3", Region: "us-east-1", S3Region: "us-east-va", Namespace: "project-" + id, Endpoint: "https://s3.example.test", PathStyle: true,
		AccessKeyEnv: "ADMIN_KEY", SecretKeyEnv: "ADMIN_SECRET", UsageReportsPath: "/var/spool/faas/object-storage-usage/" + id + ".json",
		Usage: UsageConfig{Driver: "ovh", ApplicationKeyEnv: keyEnv, ApplicationSecretEnv: "OVH_APP_SECRET", ConsumerKeyEnv: "OVH_CONSUMER_KEY", RequestLogBucket: "logs"}}
}

// adr: 20261031122757 — apid builds the provider usage exports; a backend
// whose export cannot be built is named and left out, the others export.
func TestUsageExportJobs(t *testing.T) {
	store := state.NewMemStore()
	getenv := func(name string) string {
		switch name {
		case "OVH_APP_KEY", "OVH_APP_SECRET", "OVH_CONSUMER_KEY":
			return "synthetic"
		}
		return ""
	}
	plain := BackendConfig{ID: "plain", Driver: "s3", Region: "us-east-1", S3Region: "us-east-va", Namespace: "project-plain", Endpoint: "https://s3.example.test", PathStyle: true, AccessKeyEnv: "ADMIN_KEY", SecretKeyEnv: "ADMIN_SECRET"}
	registry := usageJobsRegistry(t, ovhUsageBackend("ovh-a", "OVH_APP_KEY"), ovhUsageBackend("ovh-b", "OVH_MISSING_KEY"), plain)

	jobs, err := UsageExportJobs(registry, store, getenv)
	if err == nil || !strings.Contains(err.Error(), "backend ovh-b has incomplete ovh usage credentials") {
		t.Fatalf("err = %v, want ovh-b named for its missing provider account key", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want only ovh-a's", len(jobs))
	}
	job := jobs[0]
	var backend Backend
	for _, b := range registry.Backends() {
		if b.ID == "ovh-a" {
			backend = b
		}
	}
	if job.BackendID != "ovh-a" || job.BackendFingerprint != backend.Fingerprint || job.ReportPath != "/var/spool/faas/object-storage-usage/ovh-a.json" || job.CoverageLag != time.Hour {
		t.Fatalf("job = %+v", job)
	}
	if _, ok := job.Exporter.(periodUsageExporter); !ok {
		t.Fatalf("exporter %T does not read the catalog with the report month's allocation", job.Exporter)
	}

	if _, err := UsageExportJobs(nil, store, getenv); err == nil {
		t.Fatal("a missing registry was accepted")
	}
	if jobs, err := UsageExportJobs(usageJobsRegistry(t, plain), store, getenv); err != nil || len(jobs) != 0 {
		t.Fatalf("a registry without usage drivers: %d jobs, %v", len(jobs), err)
	}
}
