package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	capabilities    Capabilities
	provisionStatus ProviderStatus
	inspectStatus   ProviderStatus
	deleteDone      bool
	provisionErr    error
	provisionCalls  int
	inspectCalls    int
	deleteCalls     int
}

func (p *fakeProvider) Capabilities() Capabilities { return p.capabilities }

func (p *fakeProvider) Provision(_ context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	p.provisionCalls++
	if p.provisionErr != nil {
		return ObservedDatabase{}, p.provisionErr
	}
	return ObservedDatabase{
		ProviderResourceID: "upstream-" + request.ResourceID,
		Status:             p.provisionStatus,
		Spec:               request.Spec,
	}, nil
}

func (p *fakeProvider) Inspect(_ context.Context, providerResourceID string) (ObservedDatabase, error) {
	p.inspectCalls++
	return ObservedDatabase{ProviderResourceID: providerResourceID, Status: p.inspectStatus, Spec: testSpec()}, nil
}

func (*fakeProvider) Update(_ context.Context, _ UpdateRequest) (ObservedDatabase, error) {
	return ObservedDatabase{}, ErrUnsupported
}

func (p *fakeProvider) Delete(_ context.Context, _ DeleteRequest) (DeleteResult, error) {
	p.deleteCalls++
	return DeleteResult{Done: p.deleteDone}, nil
}

func (*fakeProvider) IssueCredentials(_ context.Context, _ CredentialRequest) (CredentialMaterial, error) {
	return CredentialMaterial{}, ErrUnsupported
}

func (*fakeProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	return Usage{Window: window}, nil
}

func testCapabilities() Capabilities {
	return Capabilities{
		PostgresMajors:          []int{16, 17},
		ServiceClasses:          []ServiceClass{ClassDevelopment, ClassBurstable},
		Availability:            []Availability{AvailabilitySingleZone},
		ScaleToZero:             true,
		PooledConnections:       true,
		PointInTimeRestore:      true,
		MaxRestoreWindowSeconds: 7 * 24 * 60 * 60,
		MaxStorageBytes:         100 << 30,
		UsageMeters:             []Meter{MeterActiveSeconds, MeterStorageByteSeconds, MeterEgressBytes},
	}
}

func testSpec() Spec {
	return Spec{
		Region:               "us-east-1",
		PostgresMajor:        17,
		Class:                ClassDevelopment,
		Availability:         AvailabilitySingleZone,
		ScaleToZero:          true,
		StorageLimitBytes:    10 << 30,
		RestoreWindowSeconds: 24 * 60 * 60,
	}
}

func testRegistry(t *testing.T, provider *fakeProvider, mutate func(*Config)) *Registry {
	t.Helper()
	config := Config{
		DefaultRegion:          "us-east-1",
		Defaults:               map[string]string{"us-east-1": "primary-a"},
		MaxDatabasesPerAccount: 2,
		Backends: []BackendConfig{{
			ID:        "primary-a",
			Driver:    "fake",
			Region:    "us-east-1",
			Namespace: "provider-account-a",
			Settings:  map[string]string{"endpoint": "https://db.example.test"},
			SecretEnv: map[string]string{"api-key": "TEST_DATABASE_API_KEY"},
		}},
	}
	if mutate != nil {
		mutate(&config)
	}
	registry, err := NewRegistry(config, func(string) string { return "secret" }, map[string]Factory{
		"fake": func(BackendConfig, func(string) string) (Provider, error) { return provider, nil },
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func testService(t *testing.T, registry *Registry, store Store) *Service {
	t.Helper()
	sequence := 0
	service, err := NewService(registry, store, ServiceOptions{
		Now: func() time.Time { return time.Date(2026, 9, 5, 12, 0, sequence, 0, time.UTC) },
		NewID: func() string {
			sequence++
			return fmt.Sprintf("database-%d", sequence)
		},
		NewLeaseToken: func() string {
			sequence++
			return fmt.Sprintf("lease-%d", sequence)
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func TestCreateIsIdempotentAndPersistsPlacement(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	registry := testRegistry(t, provider, nil)
	service := testService(t, registry, NewMemoryStore())
	request := CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()}

	first, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if first.State != StateReady || second.ID != first.ID || provider.provisionCalls != 1 {
		t.Fatalf("idempotency = first state %q, IDs %q/%q, calls %d", first.State, first.ID, second.ID, provider.provisionCalls)
	}
	backend, err := registry.Default(request.Spec.Region)
	if err != nil {
		t.Fatal(err)
	}
	if first.BackendID != backend.ID || first.BackendFingerprint != backend.Fingerprint || first.ProviderResourceID == "" {
		t.Fatalf("placement was not persisted: %+v", first)
	}
}

func TestReconcileResumesAsynchronousProvisioning(t *testing.T) {
	provider := &fakeProvider{
		capabilities:    testCapabilities(),
		provisionStatus: ProviderStatusPending,
		inspectStatus:   ProviderStatusReady,
	}
	service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
	created, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "events", Spec: testSpec()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.State != StateProvisioning || created.ProviderResourceID == "" {
		t.Fatalf("pending database = %+v", created)
	}
	ready, err := service.Reconcile(context.Background(), "account-a", created.ID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if ready.State != StateReady || provider.provisionCalls != 1 || provider.inspectCalls != 1 {
		t.Fatalf("ready = %+v; provision=%d inspect=%d", ready, provider.provisionCalls, provider.inspectCalls)
	}
}

func TestCreateRejectsUnsupportedSpecBeforeReservation(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	provider.capabilities.ScaleToZero = false
	store := NewMemoryStore()
	service := testService(t, testRegistry(t, provider, nil), store)
	_, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "jobs", Spec: testSpec()})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Create error = %v, want ErrUnsupported", err)
	}
	if provider.provisionCalls != 0 {
		t.Fatalf("provider called %d times", provider.provisionCalls)
	}
	if _, err := store.FindByName(context.Background(), "account-a", "jobs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsupported reservation exists: %v", err)
	}
}

func TestCreateEnforcesAccountDatabaseQuota(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	registry := testRegistry(t, provider, func(config *Config) {
		config.MaxDatabasesPerAccount = 1
	})
	service := testService(t, registry, NewMemoryStore())
	if _, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "first", Spec: testSpec()}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "second", Spec: testSpec()}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second Create error = %v, want ErrQuotaExceeded", err)
	}
	if provider.provisionCalls != 1 {
		t.Fatalf("provider called %d times after quota rejection", provider.provisionCalls)
	}
}

func TestProviderErrorIsNormalizedAndSanitized(t *testing.T) {
	provider := &fakeProvider{
		capabilities:    testCapabilities(),
		provisionStatus: ProviderStatusReady,
		provisionErr:    errors.New("upstream password=do-not-log"),
	}
	store := NewMemoryStore()
	service := testService(t, testRegistry(t, provider, nil), store)
	_, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "private", Spec: testSpec()})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "do-not-log") {
		t.Fatalf("Create error was not sanitized: %v", err)
	}
	database, findErr := store.FindByName(context.Background(), "account-a", "private")
	if findErr != nil || database.LastErrorCode != "unavailable" || database.LeaseToken != "" {
		t.Fatalf("durable failure = %+v, %v", database, findErr)
	}
}

func TestDeleteSupportsAsynchronousProviders(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
	database, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "sessions", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	deleting, err := service.Delete(context.Background(), "account-a", database.ID)
	if err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if deleting.State != StateDeleting {
		t.Fatalf("first Delete state = %q", deleting.State)
	}
	provider.deleteDone = true
	deleted, err := service.Delete(context.Background(), "account-a", database.ID)
	if err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if deleted.State != StateDeleted || deleted.DeletedAt == nil || provider.deleteCalls != 2 {
		t.Fatalf("deleted = %+v; calls=%d", deleted, provider.deleteCalls)
	}
}

func TestPlacementFingerprintFencesRepurposedBackend(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities()}
	first := testRegistry(t, provider, nil)
	oldBackend, err := first.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	second := testRegistry(t, provider, func(config *Config) {
		config.Backends[0].Namespace = "different-provider-account"
	})
	if _, err := second.Resolve(oldBackend.ID, oldBackend.Fingerprint); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Resolve after repurpose = %v, want ErrUnavailable", err)
	}
	rotatedSecret := testRegistry(t, provider, func(config *Config) {
		config.Backends[0].SecretEnv["api-key"] = "ROTATED_DATABASE_API_KEY"
	})
	resolved, err := rotatedSecret.Resolve(oldBackend.ID, oldBackend.Fingerprint)
	if err != nil || resolved.Fingerprint != oldBackend.Fingerprint {
		t.Fatalf("credential rotation changed placement: %+v, %v", resolved, err)
	}
}

func TestCredentialMaterialRedactsFormatting(t *testing.T) {
	material := CredentialMaterial{
		Username:  "user",
		Password:  "very-secret",
		Database:  "app",
		TLSMode:   "verify-full",
		Endpoints: []Endpoint{{Role: EndpointPooled, Host: "db.example.test", Port: 5432}},
	}
	if err := material.Validate(); err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v %#v", material, material)
	if strings.Contains(formatted, material.Password) || !strings.Contains(formatted, "REDACTED") {
		t.Fatalf("credential formatting leaked: %s", formatted)
	}
}

func TestUsageRejectsDuplicateOrNegativeMeters(t *testing.T) {
	window := UsageWindow{From: time.Unix(0, 0), To: time.Unix(60, 0)}
	valid := Usage{Window: window, Readings: []MeterReading{{Meter: MeterOperations, Quantity: 5}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid usage: %v", err)
	}
	invalid := Usage{Window: window, Readings: []MeterReading{{Meter: MeterOperations, Quantity: 5}, {Meter: MeterOperations, Quantity: -1}}}
	if !errors.Is(invalid.Validate(), ErrInvalid) {
		t.Fatalf("invalid usage accepted")
	}
}
