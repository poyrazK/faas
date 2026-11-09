package neon

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type rejectedResizeTransport struct {
	base    http.RoundTripper
	patches int
}

func (t *rejectedResizeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPatch {
		t.patches++
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"invalid resize"}`)), Request: request}, nil
	}
	return t.base.RoundTrip(request)
}

func TestResizeRejectionObservesSourceBeforeFreeingCatalog(t *testing.T) {
	f := &resizeHTTPFixture{}
	p := resizeHTTPProvider(t, f)
	transport := &rejectedResizeTransport{base: p.httpClient.Transport}
	p.httpClient.Transport = transport
	backend := testBackend()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: backend.Region,
		Defaults: map[string]string{backend.Region: backend.ID}, MaxDatabasesPerAccount: 2, Backends: []managedpostgres.BackendConfig{backend}},
		func(string) string { return "fixture" }, map[string]managedpostgres.Factory{"neon": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Default(backend.Region)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := managedpostgres.NewMemoryStore()
	now := time.Now().UTC()
	database, _, err := store.Reserve(ctx, managedpostgres.Database{ID: uuid.NewString(), AccountID: "account", Name: "orders", State: managedpostgres.StateProvisioning,
		Spec: testDatabaseSpec(), BackendID: resolved.ID, BackendFingerprint: resolved.Fingerprint, DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now}, 2)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, database.AccountID, database.ID, "provision", managedpostgres.StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claim.LeaseToken, "quiet-river-12345678", now); err != nil {
		t.Fatal(err)
	}
	claim.ProviderResourceID = "quiet-river-12345678"
	database, err = store.FinishProvisionWithDataResource(ctx, claim, managedpostgres.ObservedDatabase{ProviderResourceID: claim.ProviderResourceID,
		DataResourceID: claim.ProviderResourceID + "/br-main-123", Status: managedpostgres.ProviderStatusReady, Spec: database.Spec}, now)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	request := managedpostgres.ResizeDatabaseRequest{AccountID: database.AccountID, DatabaseID: database.ID, RequestID: uuid.NewString(), TargetClass: managedpostgres.ClassDevelopment}
	if _, err := svc.Resize(ctx, request); err != nil {
		t.Fatal(err)
	}
	ready, err := svc.Reconcile(ctx, database.AccountID, database.ID)
	if err != nil || ready.State != managedpostgres.StateReady || ready.Spec != database.Spec {
		t.Fatalf("provider rejection stranded the database: database=%+v err=%v", ready, err)
	}
	receipt, err := svc.GetResize(ctx, database.AccountID, database.ID, request.RequestID)
	if err != nil || receipt.State != managedpostgres.ResizeFailed || receipt.ErrorCode != "invalid" || transport.patches != 1 || f.patches != 0 {
		t.Fatalf("invalid request was not recorded once: receipt=%+v attempts=%d applied=%d err=%v", receipt, transport.patches, f.patches, err)
	}
}

func TestObserveUpdateDoesNotReplayMutation(t *testing.T) {
	for _, pending := range []bool{false, true} {
		f := &resizeHTTPFixture{pending: pending}
		p := resizeHTTPProvider(t, f)
		request := resizeHTTPrequest()
		observed, err := p.ObserveUpdate(t.Context(), request)
		want := managedpostgres.ProviderStatusReady
		if pending {
			want = managedpostgres.ProviderStatusPending
		}
		if err != nil || observed.Status != want || observed.Spec != request.PreviousSpec || observed.DataResourceID != request.DataResourceID || f.patches != 0 {
			t.Fatalf("update observation mutated or hid pending work: observed=%+v patches=%d err=%v", observed, f.patches, err)
		}
	}
}
