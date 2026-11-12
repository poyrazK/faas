// adr: 167, 226
package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tenantRoutingProvider struct{ publicReadTestProvider }

func (p tenantRoutingProvider) PresignObjectRead(_ context.Context, bucket, method, _ string, _ int64) (SignedRequest, error) {
	return SignedRequest{URL: p.endpoint + "/" + bucket, Method: method}, nil
}

type tenantStorageFixture struct {
	store                     *state.MemStore
	registry                  *Registry
	oldApp, tenantApp         state.App
	host, tenantID, surfaceID string
	enabled                   bool
}

func tenantStorageCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newTenantStorageFixture(t *testing.T) *tenantStorageFixture {
	t.Helper()
	ctx := t.Context()
	f := &tenantStorageFixture{store: state.NewMemStore(), host: "tenant-storage.example.com"}
	owner, err := f.store.CreateAccount(ctx, "tenant-storage@example.com", api.PlanPro)
	tenantStorageCheck(t, err)
	f.oldApp, err = f.store.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "old-storage-app", Status: state.AppActive})
	tenantStorageCheck(t, err)
	f.tenantApp, err = f.store.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "tenant-storage-app", Status: state.AppActive})
	tenantStorageCheck(t, err)
	_, err = f.store.CreateCustomDomainIfUnderQuota(state.WithPublicCustomDomainClaim(ctx), f.host, f.oldApp.ID, "proof", 100, 500)
	tenantStorageCheck(t, err)
	verified, err := f.store.MarkDomainVerifiedIfChallenge(ctx, f.host, "proof")
	tenantStorageCheck(t, err)
	if !verified {
		t.Fatal("custom domain did not verify")
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: owner.ID, AppID: f.tenantApp.ID, Name: "storage-tenant"}, limits)
	tenantStorageCheck(t, err)
	f.surfaceID = surface.ID
	_, err = f.store.CreateTenantHostnameIfUnderQuota(state.WithPublicTenantHostnameClaim(ctx), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: f.host, ChallengeToken: "tenant-proof"}, limits)
	tenantStorageCheck(t, err)
	tenantStorageCheck(t, f.store.MarkTenantHostnameVerified(ctx, f.host))
	tenantStorageCheck(t, f.store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive))
	tenant, _, err := f.store.CreatePlatformTenant(ctx, owner.ID, "storage-customer", "Storage customer", 100)
	tenantStorageCheck(t, err)
	f.tenantID = tenant.ID
	_, err = f.store.LinkPlatformTenantSurface(ctx, owner.ID, tenant.ID, surface.ID)
	tenantStorageCheck(t, err)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	t.Cleanup(provider.Close)
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000, MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	f.registry, err = NewRegistry(Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: "https://storage.test", S3Region: "us-east-1"}}}, func(string) string { return "" }, map[string]Factory{"s3": func(BackendConfig, func(string) string) (Provider, error) {
		return tenantRoutingProvider{publicReadTestProvider{endpoint: provider.URL}}, nil
	}})
	tenantStorageCheck(t, err)
	backend, err := f.registry.Default("us-east-1")
	tenantStorageCheck(t, err)
	for _, item := range []struct {
		app  state.App
		name string
	}{{f.oldApp, "old-assets"}, {f.tenantApp, "tenant-assets"}} {
		bucket, err := f.store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: owner.ID, AppID: item.app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: item.name, PublicRead: true, ServeAt: "/assets"}, api.DefaultObjectBucketsPerApp)
		tenantStorageCheck(t, err)
		_, err = f.store.ClaimObjectBucket(ctx, owner.ID, item.app.ID, bucket.ID, "claim", "provisioning")
		tenantStorageCheck(t, err)
		tenantStorageCheck(t, f.store.FinishObjectBucket(ctx, bucket.ID, "claim", "ready"))
		tenantStorageCheck(t, f.store.ClaimObjectInventory(ctx, bucket.ID, "inventory"))
		tenantStorageCheck(t, f.store.FinishObjectInventory(ctx, bucket.ID, "inventory", 0, 0))
	}
	now := time.Now().UTC()
	tenantStorageCheck(t, f.store.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: owner.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "test", PeriodStart: state.ObjectStoragePeriod(now), ObservedAt: now}))
	return f
}

func (f *tenantStorageFixture) tenantSurfacesOn() bool { return f.enabled }

func tenantStorageGet(t *testing.T, handler http.Handler, host string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+"/assets/file.txt", nil))
	return w
}

func TestCustomDomainStorageFollowsTenantRoutingAndWarmSuspension(t *testing.T) {
	f := newTenantStorageFixture(t)
	handler, err := NewPublicReadHandler(PublicReadConfig{Store: f.store, Registry: f.registry, RequestMetrics: f.store, Accounting: f.store, AppsDomain: "apps.gregale.dev", TenantSurfacesEnabled: f.tenantSurfacesOn, Next: http.NotFoundHandler()})
	tenantStorageCheck(t, err)
	uploads := &uploadHandler{store: f.store, appsDomain: "apps.gregale.dev", tenantSurfacesEnabled: f.tenantSurfacesOn}
	check := func(body, appID string) {
		t.Helper()
		response := tenantStorageGet(t, handler, f.host)
		if response.Code != http.StatusOK || response.Body.String() != body {
			t.Fatalf("public assets: %d %s, want %s", response.Code, response.Body, body)
		}
		app, err := uploads.lookupApp(httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.host+"/uploads/assets", nil))
		if err != nil || app.ID != appID {
			t.Fatalf("upload app: %s %v, want %s", app.ID, err, appID)
		}
	}
	check("old-assets", f.oldApp.ID)
	f.enabled = true
	check("tenant-assets", f.tenantApp.ID)
	check("tenant-assets", f.tenantApp.ID)
	canonicalHost := f.host
	f.host = strings.ToUpper(f.host) + ".:443"
	check("tenant-assets", f.tenantApp.ID)
	f.host = canonicalHost
	_, err = f.store.SetPlatformTenantStatus(t.Context(), f.oldApp.AccountID, f.tenantID, state.PlatformTenantSuspended)
	tenantStorageCheck(t, err)
	if response := tenantStorageGet(t, handler, f.host); response.Code != http.StatusNotFound {
		t.Fatalf("suspended tenant received cached public assets: %d %s", response.Code, response.Body)
	}
	if response := tenantStorageGet(t, handler, strings.ToUpper(f.host)+".:443"); response.Code != http.StatusNotFound {
		t.Fatalf("host spelling bypassed tenant suspension: %d %s", response.Code, response.Body)
	}
	_, err = uploads.lookupApp(httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.host+"/uploads/assets", nil))
	if !errors.Is(err, state.ErrPlatformTenantSuspended) {
		t.Fatalf("suspended upload lookup: %v", err)
	}
	_, err = f.store.SetPlatformTenantStatus(t.Context(), f.oldApp.AccountID, f.tenantID, state.PlatformTenantActive)
	tenantStorageCheck(t, err)
	check("tenant-assets", f.tenantApp.ID)
	f.enabled = false
	check("old-assets", f.oldApp.ID)
}

func TestCustomDomainStorageForwarderRechecksWarmTenantSuspension(t *testing.T) {
	f := newTenantStorageFixture(t)
	f.enabled = true
	forwarded := 0
	handler, err := NewPublicReadForwarder(PublicReadForwardConfig{Store: f.store, AppsDomain: "apps.gregale.dev", TenantSurfacesEnabled: f.tenantSurfacesOn, Next: http.NotFoundHandler(), Forward: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded++; w.WriteHeader(http.StatusNoContent) })})
	tenantStorageCheck(t, err)
	if response := tenantStorageGet(t, handler, f.host); response.Code != http.StatusNoContent {
		t.Fatalf("active tenant did not forward: %d", response.Code)
	}
	_, err = f.store.SetPlatformTenantStatus(t.Context(), f.oldApp.AccountID, f.tenantID, state.PlatformTenantSuspended)
	tenantStorageCheck(t, err)
	if response := tenantStorageGet(t, handler, f.host); response.Code != http.StatusNotFound || forwarded != 1 {
		t.Fatalf("suspended cached mount forwarded: status=%d forwards=%d", response.Code, forwarded)
	}
}

type failingStorageTenantReader struct {
	PublicReadStore
	reader state.PlatformTenantHostBindingReader
	fail   error
}

func (s *failingStorageTenantReader) PlatformTenantHostBinding(ctx context.Context, host string) (state.PlatformTenantHostBinding, error) {
	if s.fail != nil {
		return state.PlatformTenantHostBinding{}, s.fail
	}
	return s.reader.PlatformTenantHostBinding(ctx, host)
}

func TestCustomDomainStorageWarmMountFailsClosedWhenTenantReadFails(t *testing.T) {
	f := newTenantStorageFixture(t)
	f.enabled = true
	store := &failingStorageTenantReader{PublicReadStore: f.store, reader: f.store}
	handler, err := NewPublicReadHandler(PublicReadConfig{Store: store, Registry: f.registry, RequestMetrics: f.store, Accounting: f.store, AppsDomain: "apps.gregale.dev", TenantSurfacesEnabled: f.tenantSurfacesOn, Next: http.NotFoundHandler()})
	tenantStorageCheck(t, err)
	if response := tenantStorageGet(t, handler, f.host); response.Code != http.StatusOK || response.Body.String() != "tenant-assets" {
		t.Fatalf("active tenant: %d %s", response.Code, response.Body)
	}
	store.fail = errors.New("tenant binding unavailable")
	if response := tenantStorageGet(t, handler, f.host); response.Code != http.StatusNotFound {
		t.Fatalf("failed tenant check served a cached bucket: %d %s", response.Code, response.Body)
	}
}
