package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type parkedErrorHistoryProvider struct{ Provider }

func (parkedErrorHistoryProvider) Capabilities() Capabilities { return testCapabilities() }

func TestErrorHistoryReadsCatalogWithoutContactingParkedDatabase(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	// Embedding only the interface makes a provider runtime call fail the
	// test. Reading or disabling history must only touch the catalog.
	cfg := Config{
		DefaultRegion:          "us-east-1",
		Defaults:               map[string]string{"us-east-1": "primary-a"},
		MaxDatabasesPerAccount: 2,
		Backends:               []BackendConfig{{ID: "primary-a", Driver: "firecracker", Region: "us-east-1", Namespace: "test"}},
	}
	registry, err := NewRegistry(cfg, func(string) string { return "" }, map[string]Factory{
		"firecracker": func(BackendConfig, func(string) string) (Provider, error) { return parkedErrorHistoryProvider{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(registry, store, ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	input := postgresTestDatabase(account, "orders", now)
	input.BackendFingerprint = backend.Fingerprint
	d, _, err := store.Reserve(ctx, input, 2)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, account, d.ID, uuid.NewString(), StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, d.ID, claim.LeaseToken, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
	if d, err = store.FinishProvision(ctx, d.ID, claim.LeaseToken, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetErrorHistory(ctx, account, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO native_postgres_error_events VALUES
		($1, $2, now() - interval '1 minute', 'ERROR', '40P01', NULL),
		($3, $2, now() - interval '1 minute', 'ERROR', '57014', NULL),
		($4, $2, now() - interval '25 hours', 'ERROR', '40P01', NULL)`,
		uuid.New(), d.ID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	report, err := svc.ErrorHistory(ctx, account, d.ID, time.Hour, "40P01", 20)
	if err != nil || !report.Enabled || len(report.Events) != 1 || report.Events[0].SQLState != "40P01" {
		t.Fatalf("parked filtered history: %+v, %v", report, err)
	}
	report, err = svc.ErrorHistory(ctx, account, d.ID, 24*time.Hour, "", 1)
	if err != nil || !report.HasMore || len(report.Events) != 1 {
		t.Fatalf("bounded history: %+v, %v", report, err)
	}
	report, err = svc.SetErrorHistory(ctx, account, d.ID, false)
	if err != nil || report.Enabled || len(report.Events) != 2 {
		t.Fatalf("disabling should retain recent history: %+v, %v", report, err)
	}
	if _, err := svc.ErrorHistory(ctx, uuid.NewString(), d.ID, time.Hour, "", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrelated account read history: %v", err)
	}
}
