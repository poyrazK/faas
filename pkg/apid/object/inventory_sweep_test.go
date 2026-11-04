// adr: 20261023223000

package object

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// listingProvider lists one 7-byte object per bucket and records how many
// listings ran at once.
type listingProvider struct {
	objectstorage.Provider
	mu                  sync.Mutex
	active, maxParallel int
	listings            atomic.Int64
}

func (p *listingProvider) ListObjects(context.Context, string, string, string, int32) (objectstorage.ObjectPage, error) {
	p.mu.Lock()
	p.active++
	p.maxParallel = max(p.maxParallel, p.active)
	p.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	p.listings.Add(1)
	return objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "k", Size: 7}}}, nil
}

// TestInventorySweepDrainsEveryDueBucket pins blockers 2 and 3 of the GA
// review: one sweep used to scan ten buckets, one at a time, and every listing
// was charged to the customer's monthly requests.
func TestInventorySweepDrainsEveryDueBucket(t *testing.T) {
	const buckets = 3*api.ObjectStorageInventorySweepBatch + 7
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
	ids := make([]string, 0, buckets)
	accounts := map[string]string{}
	for i := range buckets {
		account, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "sweep-" + uuid.NewString()[:8], Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical-" + uuid.NewString()[:8], BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
		if err != nil {
			t.Fatal(i, err)
		}
		if _, err = st.ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err = st.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
		accounts[b.ID] = account.ID
	}
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	var successes atomic.Int64
	if err = svc.ReconcileObjectInventories(t.Context(), func(outcome string) {
		if outcome == "success" {
			successes.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if successes.Load() != buckets || provider.listings.Load() != buckets {
		t.Fatalf("one sweep scanned %d of %d buckets (%d listings)", successes.Load(), buckets, provider.listings.Load())
	}
	if provider.maxParallel < 2 || provider.maxParallel > api.ObjectStorageInventorySweepConcurrency {
		t.Fatalf("max parallel listings = %d", provider.maxParallel)
	}
	for _, id := range ids {
		usage, err := st.ObjectUsage(t.Context(), accounts[id], time.Now())
		if err != nil || len(usage.Buckets) != 1 || usage.Buckets[0].ObservedAt.IsZero() || usage.Buckets[0].ObservedBytes != 7 {
			t.Fatal("bucket not observed", id, usage, err)
		}
		if usage.Buckets[0].RequestCount != 0 {
			t.Fatal("maintenance listing charged to the customer", usage.Buckets[0].RequestCount)
		}
	}
	// Nothing is due again until the cadence passes.
	if err = svc.ReconcileObjectInventories(t.Context(), nil); err != nil || provider.listings.Load() != buckets {
		t.Fatal("second sweep rescanned fresh buckets", provider.listings.Load(), err)
	}
}
