package object

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func pooledRegistry(t *testing.T, ceiling int64) *objectstorage.Registry {
	t.Helper()
	since := time.Now().UTC().Add(-time.Hour)
	policy := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 10, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 60}
	backend := func(id string) objectstorage.BackendConfig {
		return objectstorage.BackendConfig{ID: id, Driver: "s3", Region: "us-east-1", Namespace: "project-" + id, Endpoint: "https://s3.example.test", S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", MaxBuckets: ceiling}
	}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{
		Accounting: &policy, PublicEndpoint: "https://s3.gregale.dev", Transfer: objectstorage.ObjectTransferConfig{Profile: "proxied"},
		DefaultRegion: "us-east-1", Pools: map[string][]string{"us-east-1": {"ovh-a", "ovh-b"}},
		Backends: []objectstorage.BackendConfig{backend("ovh-a"), backend("ovh-b")},
	}, func(string) string { return "value" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// adr: 20261029174627
func TestCreateBucketPlacesAcrossPool(t *testing.T) {
	st := state.NewMemStore()
	ctx := t.Context()
	acct, err := st.CreateAccount(ctx, "placement-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "placement", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	registry := pooledRegistry(t, 1)
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	placed := map[string]string{}
	for _, name := range []string{"one", "two"} {
		b, err := svc.reserveBucket(ctx, st, acct.ID, app.ID, CreateBucketRequest{Name: name, Scope: api.DefaultEnvScope})
		if err != nil {
			t.Fatal(name, err)
		}
		backend, err := registry.Resolve(b.BackendID, b.BackendFingerprint)
		if err != nil || b.Region != "us-east-1" {
			t.Fatalf("%s placed on an unresolvable backend %q: %v", name, b.BackendID, err)
		}
		placed[name] = backend.ID
	}
	if placed["one"] != "ovh-a" || placed["two"] != "ovh-b" {
		t.Fatalf("placements = %v; want the pool filled in order", placed)
	}
	again, err := svc.reserveBucket(ctx, st, acct.ID, app.ID, CreateBucketRequest{Name: "one", Scope: api.DefaultEnvScope})
	if err != nil || again.BackendID != "ovh-a" {
		t.Fatalf("retried create = %+v, %v", again, err)
	}
	_, err = svc.reserveBucket(ctx, st, acct.ID, app.ID, CreateBucketRequest{Name: "three", Scope: api.DefaultEnvScope})
	if !errors.Is(err, state.ErrObjectStorageRegionFull) {
		t.Fatalf("full region err = %v", err)
	}
	if items, _ := st.ListObjectBuckets(ctx, acct.ID, app.ID); len(items) != 2 {
		t.Fatalf("a refused create left a bucket row: %d rows", len(items))
	}
	w := httptest.NewRecorder()
	BucketProblem(w, err)
	var problem api.Problem
	if json.Unmarshal(w.Body.Bytes(), &problem) != nil || w.Code != 503 || problem.Code != "object_storage_region_full" {
		t.Fatalf("problem = %d %s", w.Code, w.Body.String())
	}
	metrics := newPlacementMetrics()
	if err = svc.observePlacement(ctx, metrics); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.buckets.WithLabelValues("us-east-1", "ovh-b")); got != 1 {
		t.Fatalf("ovh-b buckets gauge = %v", got)
	}
	if got := testutil.ToFloat64(metrics.limits.WithLabelValues("us-east-1", "ovh-a")); got != 1 {
		t.Fatalf("ovh-a limit gauge = %v", got)
	}
	if got := testutil.ToFloat64(metrics.full.WithLabelValues("us-east-1")); got != 1 {
		t.Fatalf("region full gauge = %v", got)
	}
}
