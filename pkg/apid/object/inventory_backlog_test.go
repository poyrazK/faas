// adr: 20261110151628

package object

import (
	"testing"
	"time"

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
	if quiet := testutil.ToFloat64(m.oldest.WithLabelValues("quiet")); quiet != 0 {
		t.Fatalf("quiet age %v s with object_inventory_quiet off", quiet)
	}
}

// TestInventoryDriftCounted: a quiet bucket whose verification listing
// changed is counted by direction under gateway-safety accounting, where
// admission is the only door; anything else is not (ADR-20261110151628 §2).
func TestInventoryDriftCounted(t *testing.T) {
	registry := func(t *testing.T, gatewaySafety bool) *objectstorage.Registry {
		t.Helper()
		since := time.Now().UTC().Add(-time.Hour)
		policy := api.ObjectStoragePolicy{GatewayMeteringSince: &since, MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 10, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 60}
		if gatewaySafety {
			policy.AccountingMode = api.ObjectStorageGatewaySafetyV1
		} else {
			policy.GatewayMeteringSince, policy.MaxMonthlyCostMillicents, policy.MaxReportAgeSeconds = nil, 100, 3600
		}
		r, err := objectstorage.NewRegistry(objectstorage.Config{
			Accounting: &policy, PublicEndpoint: "https://s3.gregale.dev", Transfer: objectstorage.ObjectTransferConfig{Profile: "proxied"},
			DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"},
			Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}},
		}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
			return &listingProvider{}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	b := state.ObjectBucket{ID: uuid.NewString(), BackendID: "external"}
	for _, tc := range []struct {
		name          string
		gatewaySafety bool
		v             state.ObjectInventoryVerification
		up, down      float64
	}{
		{name: "grew around admission", gatewaySafety: true, v: state.ObjectInventoryVerification{Quiet: true, PreviousBytes: 10, PreviousKeys: 1, Bytes: 20, Keys: 2}, up: 1},
		{name: "shrank around admission", gatewaySafety: true, v: state.ObjectInventoryVerification{Quiet: true, PreviousBytes: 10, PreviousKeys: 2, Bytes: 10, Keys: 1}, down: 1},
		{name: "unchanged", gatewaySafety: true, v: state.ObjectInventoryVerification{Quiet: true, PreviousBytes: 10, PreviousKeys: 1, Bytes: 10, Keys: 1}},
		{name: "written since the last listing", gatewaySafety: true, v: state.ObjectInventoryVerification{PreviousBytes: 10, Bytes: 20}},
		{name: "provider-report accounting", v: state.ObjectInventoryVerification{Quiet: true, PreviousBytes: 10, Bytes: 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{Host: receiptRecoveryHost{store: state.NewMemStore(), registry: registry(t, tc.gatewaySafety)}}
			drift := newInventoryDriftCounter()
			svc.inventoryDrift.Store(drift)
			svc.recordInventoryVerification(b, tc.v)
			if up, down := testutil.ToFloat64(drift.WithLabelValues("up")), testutil.ToFloat64(drift.WithLabelValues("down")); up != tc.up || down != tc.down {
				t.Fatalf("up %v down %v, want %v %v", up, down, tc.up, tc.down)
			}
		})
	}
}
