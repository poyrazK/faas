package object

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// hungBillingHost blocks in BillingProvider, the call month close and
// progress delivery make before reaching the provider.
type hungBillingHost struct {
	receiptRecoveryHost
	entered chan struct{}
	release chan struct{}
}

func (h hungBillingHost) Ops() *wire.OpsMetrics { return nil }
func (h hungBillingHost) BillingProvider() billing.Provider {
	select {
	case h.entered <- struct{}{}:
	default:
	}
	<-h.release
	return nil
}

// TestInventorySweepRunsWhileBillingHangs pins the 11-03 review finding: the
// inventory sweep shared one serial loop with month close and progress
// delivery, so a hung billing step stopped every bucket's inventory and, after
// 900 s, every gateway read and write.
func TestInventorySweepRunsWhileBillingHangs(t *testing.T) {
	st := state.NewMemStore()
	provider := &listingProvider{}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	registry.Pricing = &api.ObjectStoragePricing{}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	readyBucket := func() {
		account, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "split-" + uuid.NewString()[:8], Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical-" + uuid.NewString()[:8], BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err = st.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
			t.Fatal(err)
		}
	}
	waitFor := func(what string, done func() bool) {
		deadline := time.Now().Add(10 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	readyBucket()
	host := hungBillingHost{receiptRecoveryHost: receiptRecoveryHost{store: st, registry: registry}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	svc := &Service{Host: host, InventoryWake: make(chan struct{}, 1)}
	runCtx, stop := context.WithCancel(t.Context())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		svc.RunObjectStorageAccounting(runCtx)
	}()
	defer func() {
		close(host.release)
		stop()
		<-finished
	}()

	waitFor("the first sweep", func() bool { return provider.listings.Load() >= 1 })
	select {
	case <-host.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("billing never reached month close")
	}
	readyBucket()
	svc.wakeObjectInventory()
	waitFor("a sweep while billing hangs", func() bool { return provider.listings.Load() >= 2 })
}
