// adr: 20261025111015

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type listingProvider struct {
	objectstorage.Provider
	names []string
}

func (p listingProvider) ListBucketNames(context.Context, string) ([]string, error) {
	return p.names, nil
}

func TestReconcileObjectStorageReportsRestoreDrift(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	provider := listingProvider{}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return &provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "reconcile", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "gregale-catalogued", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	provider.names = []string{"gregale-catalogued", "gregale-after-restore"}
	report, err := reconcileObjectStorage(ctx, registry, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Backends) != 1 || report.Backends[0].Physical != 2 || report.Backends[0].Catalog != 1 {
		t.Fatalf("backends = %+v", report.Backends)
	}
	if len(report.Findings) != 1 || report.Findings[0].Kind != objectstorage.ReconcileProviderOnly || report.Findings[0].PhysicalName != "gregale-after-restore" {
		t.Fatalf("findings = %+v", report.Findings)
	}
	provider.names = nil
	report, err = reconcileObjectStorage(ctx, registry, st)
	if err != nil || len(report.Findings) != 1 || report.Findings[0].Kind != objectstorage.ReconcileCatalogMissing || report.Findings[0].BucketID != b.ID {
		t.Fatalf("missing physical bucket = %+v, err %v", report.Findings, err)
	}
}

func TestReadObjectStorageProviderEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.env")
	if err := os.WriteFile(path, []byte("# comment\nexport KEY=\"value\"\nSECRET='s3cret'\n\nnot a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := readObjectStorageProviderEnv(path)
	if err != nil || env["KEY"] != "value" || env["SECRET"] != "s3cret" || len(env) != 2 {
		t.Fatalf("env = %v, err %v", env, err)
	}
	if env, err := readObjectStorageProviderEnv(filepath.Join(t.TempDir(), "absent.env")); err != nil || len(env) != 0 {
		t.Fatalf("missing env file = %v, %v; GCS with attached ADC has none", env, err)
	}
}
