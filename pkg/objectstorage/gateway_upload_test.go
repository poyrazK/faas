package objectstorage

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 627
func TestGatewaySafetyUploadRoutesBoundConcurrentDispatch(t *testing.T) {
	f := newUploadFixture(t)
	since := time.Now().UTC().Add(-time.Minute)
	p := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 10240, MaxBucketBytes: 10240, MaxAccountKeys: 100, MaxMonthlyRequests: 1, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 60}
	f.registry.Accounting = p
	if err := f.store.ClaimObjectInventory(t.Context(), f.route.BucketID, "gateway-upload"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.FinishObjectInventory(t.Context(), f.route.BucketID, "gateway-upload", 0, 0); err != nil {
		t.Fatal(err)
	}
	c := UploadConfig{Store: f.store, Routes: f.store, Buckets: f.store, Authenticator: f.store, Registry: f.registry, Accounting: f.store, AppsDomain: "apps.test", Next: http.NotFoundHandler()}
	if _, err := NewUploadHandler(c); err == nil {
		t.Fatal("gateway upload accepted missing atomic request meter")
	}
	c.RequestMetrics = f.store
	var err error
	f.handler, err = NewUploadHandler(c)
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := f.request(http.MethodPost, "/uploads/avatar", "avatar", "image/png")
			if response.Code == http.StatusCreated {
				accepted.Add(1)
			} else if response.Code != http.StatusPaymentRequired {
				t.Errorf("upload status=%d", response.Code)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 || f.provider.writes != 1 {
		t.Fatal("concurrent uploads overspent request budget", accepted.Load(), f.provider.writes)
	}
	snapshot, err := f.store.ObjectUsage(t.Context(), f.account.ID, time.Now())
	if u := state.SummarizeObjectUsage(snapshot, p, time.Now()); err != nil || !u.Fresh || u.RequestCount != 1 {
		t.Fatal("upload attempts did not reach admission ledger", u, err)
	}
}
