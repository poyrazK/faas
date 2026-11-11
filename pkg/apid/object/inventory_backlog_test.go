// adr: 20261110151628

package object

import (
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestInventoryBacklogGauges: after a sweep apid exports how many buckets are
// due and how old the oldest observation admission needs is.
func TestInventoryBacklogGauges(t *testing.T) {
	st := state.NewMemStore()
	provider := &listingProvider{}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "backlog-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical-backlog", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	m := newInventoryBacklogMetrics()
	svc.observeInventoryBacklog(t.Context(), m)
	if due, age := testutil.ToFloat64(m.due), testutil.ToFloat64(m.oldest.WithLabelValues("active")); due != 1 || age < 0 || age > 60 {
		t.Fatalf("before the sweep: due %v, oldest %v s", due, age)
	}
	if err = svc.ReconcileObjectInventories(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	svc.observeInventoryBacklog(t.Context(), m)
	if due, age := testutil.ToFloat64(m.due), testutil.ToFloat64(m.oldest.WithLabelValues("active")); due != 0 || age < 0 || age > 60 {
		t.Fatalf("after the sweep: due %v, oldest %v s", due, age)
	}
}
