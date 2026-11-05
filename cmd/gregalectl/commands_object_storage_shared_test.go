// adr: 20261025111015

package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Two backends in one provider account each list the other's buckets: a
// bucket the other backend's catalog holds is not provider_only, while one
// no catalog holds still is.
func TestReconcileObjectStorageSharedNamespace(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	provider := listingProvider{}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	backendConfig := func(id, region string) objectstorage.BackendConfig {
		// S3 ListBuckets covers the credential's whole account, whatever the
		// namespace, so both backends list every bucket.
		return objectstorage.BackendConfig{ID: id, Driver: "fake", Region: region, Namespace: "ns-" + id, Endpoint: "https://s3.example.test",
			S3Region: region, UsageReportsPath: "/var/spool/faas/" + id + "-usage.json"}
	}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1",
		Defaults: map[string]string{"us-east-1": "east", "eu-west-1": "west"},
		Backends: []objectstorage.BackendConfig{backendConfig("east", "us-east-1"), backendConfig("west", "eu-west-1")}},
		func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
			return &provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "shared", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		backend, err := registry.Default(region)
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets-" + backend.ID,
			Scope: "default", PhysicalName: "gregale-" + backend.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: region}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
			t.Fatal(err)
		}
	}
	provider.names = []string{"gregale-east", "gregale-orphan", "gregale-west"}
	report, err := reconcileObjectStorage(ctx, registry, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %+v, want the orphan once per backend", report.Findings)
	}
	for _, f := range report.Findings {
		if f.Kind != objectstorage.ReconcileProviderOnly || f.PhysicalName != "gregale-orphan" {
			t.Fatalf("finding = %+v, want only the orphan", f)
		}
	}
}
