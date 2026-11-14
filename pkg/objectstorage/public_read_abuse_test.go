package objectstorage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Object storage audit OS-5 (2026-11-10): strangers could spend a public
// bucket's anonymous share of the owner's monthly budget at the bucket's whole
// rate, with random paths and cache-busting queries that miss the CDN. A
// query is redirected before anything is counted, a missing key's request is
// refunded, and one client address is limited per bucket.

type publicReadAbuseHarness struct {
	handler  http.Handler
	reads    *ReadAdmitter
	store    *state.MemStore
	backend  Backend
	upstream *int
}

func newPublicReadAbuseHarness(t *testing.T) publicReadAbuseHarness {
	t.Helper()
	upstream := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream++
		if r.Header.Get("X-Test-Missing") != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "3")
		_, _ = w.Write([]byte("png"))
	}))
	t.Cleanup(provider.Close)
	since := time.Now().UTC().Add(-time.Minute)
	registry, err := NewRegistry(Config{
		Accounting: &api.ObjectStoragePolicy{MaxMonthlyCostMillicents: 1 << 30,
			MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000, MaxMonthlyRequests: 1000,
			MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600},
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"},
		Backends: []BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: "https://storage.test", S3Region: "us-east-1"}},
	}, func(string) string { return "" }, map[string]Factory{"s3": func(BackendConfig, func(string) string) (Provider, error) {
		return publicReadMissingProvider{publicReadTestProvider{endpoint: provider.URL}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	// As publicReadServesWithoutNext does: gateway safety on a registry built
	// without a public gateway.
	registry.Accounting.AccountingMode = api.ObjectStorageGatewaySafetyV1
	registry.Accounting.GatewayMeteringSince = &since
	registry.Accounting.MaxMonthlyCostMillicents = 0
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "public-abuse@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "assets-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-assets", PublicRead: true, ServeAt: "/assets",
	}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "token", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(ctx, bucket.ID, "token", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimObjectInventory(ctx, bucket.ID, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectInventory(ctx, bucket.ID, "inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	reads := NewNamedReadAdmitter(store, registry.Accounting.AnonymousReadPolicy(), nil, "test")
	h, err := NewPublicReadHandler(PublicReadConfig{
		Store: store, Registry: registry, RequestMetrics: store, Accounting: refusingAdmission{}, ReadAdmitter: reads, AppsDomain: "apps.example",
		ClientKey: func(r *http.Request) string { return r.Header.Get("X-Test-Client") },
		Next:      http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return publicReadAbuseHarness{handler: h, reads: reads, store: store, backend: backend, upstream: &upstream}
}

// publicReadMissingProvider signs reads of keys starting "missing/" so the
// test server answers 404.
type publicReadMissingProvider struct{ publicReadTestProvider }

func (p publicReadMissingProvider) PresignObjectRead(ctx context.Context, bucket, method, key string, expires int64) (SignedRequest, error) {
	signed, err := p.publicReadTestProvider.PresignObjectRead(ctx, bucket, method, key, expires)
	if err == nil && len(key) > 8 && key[:8] == "missing/" {
		signed.Headers = map[string]string{"X-Test-Missing": "1"}
	}
	return signed, err
}

func (h publicReadAbuseHarness) get(t *testing.T, target, client string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if client != "" {
		req.Header.Set("X-Test-Client", client)
	}
	res := httptest.NewRecorder()
	h.handler.ServeHTTP(res, req)
	return res
}

func (h publicReadAbuseHarness) flushedRequests(t *testing.T) int64 {
	t.Helper()
	if err := h.reads.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	metrics, err := h.store.ListObjectStorageProviderRequestMetrics(context.Background(), h.backend.ID, h.backend.Fingerprint, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, m := range metrics {
		total += m.RequestCount
	}
	return total
}

func TestPublicReadQueryRedirectsBeforeCounting(t *testing.T) {
	h := newPublicReadAbuseHarness(t)
	res := h.get(t, "http://assets-app.apps.example/assets/logo.png?bust=1", "198.51.100.1")
	if res.Code != http.StatusMovedPermanently || res.Header().Get("Location") != "/assets/logo.png" {
		t.Fatalf("query read = %d Location %q", res.Code, res.Header().Get("Location"))
	}
	if res.Header().Get("Cache-Control") != publicQueryRedirectCacheControl {
		t.Fatalf("redirect Cache-Control = %q", res.Header().Get("Cache-Control"))
	}
	if *h.upstream != 0 || h.flushedRequests(t) != 0 {
		t.Fatalf("a query redirect reached the provider (%d) or was counted", *h.upstream)
	}
}

func TestPublicReadMissingKeyIsRefunded(t *testing.T) {
	h := newPublicReadAbuseHarness(t)
	for i := range 3 {
		if res := h.get(t, "http://assets-app.apps.example/assets/missing/"+strconv.Itoa(i), "198.51.100.1"); res.Code != http.StatusNotFound {
			t.Fatalf("missing key = %d", res.Code)
		}
	}
	if res := h.get(t, "http://assets-app.apps.example/assets/logo.png", "198.51.100.1"); res.Code != http.StatusOK {
		t.Fatalf("present key = %d %s", res.Code, res.Body)
	}
	if got := h.flushedRequests(t); got != 1 {
		t.Fatalf("counted requests = %d, want only the found object's 1", got)
	}
}

func TestPublicReadLimitsOneClientPerBucket(t *testing.T) {
	h := newPublicReadAbuseHarness(t)
	limited := 0
	for range api.PublicObjectReadBurstPerClient + 5 {
		if res := h.get(t, "http://assets-app.apps.example/assets/logo.png", "198.51.100.1"); res.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatalf("one client made %d reads past its burst of %d", api.PublicObjectReadBurstPerClient+5, api.PublicObjectReadBurstPerClient)
	}
	if res := h.get(t, "http://assets-app.apps.example/assets/logo.png", "203.0.113.7"); res.Code != http.StatusOK {
		t.Fatalf("another client was limited: %d", res.Code)
	}
}
