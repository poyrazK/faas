package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type lifecycleQualificationProvider struct {
	fakeProvider
	material       CredentialMaterial
	issued         map[string]struct{}
	issueCalls     int
	revokeCalls    int
	lastCredential CredentialRequest
}

type timeoutLifecycleQualificationProvider struct {
	lifecycleQualificationProvider
}

func (p *timeoutLifecycleQualificationProvider) Provision(ctx context.Context, _ ProvisionRequest) (ObservedDatabase, error) {
	<-ctx.Done()
	return ObservedDatabase{}, ctx.Err()
}

func (p *lifecycleQualificationProvider) IssueCredentials(_ context.Context, request CredentialRequest) (CredentialMaterial, error) {
	p.issueCalls++
	p.lastCredential = request
	p.issued[request.IdentityKey] = struct{}{}
	material := p.material
	material.Endpoints = append([]Endpoint(nil), p.material.Endpoints...)
	return material, nil
}

func (p *lifecycleQualificationProvider) RevokeCredentials(_ context.Context, request CredentialRequest) error {
	p.revokeCalls++
	delete(p.issued, request.IdentityKey)
	return nil
}

func (p *lifecycleQualificationProvider) ProbeScaleToZero(context.Context, string, CredentialMaterial) (ScaleToZeroProbeResult, error) {
	return ScaleToZeroProbeResult{Suspended: true, Resumed: true, WakeLatency: 25 * time.Millisecond}, nil
}

type lifecycleQualificationSink struct {
	refs        map[string]Binding
	putCalls    int
	deleteCalls int
	seen        bool
	putErr      error
}

func (s *lifecycleQualificationSink) Put(_ context.Context, binding Binding, material CredentialMaterial) (string, error) {
	s.putCalls++
	if s.putErr != nil {
		return "", s.putErr
	}
	if err := material.Validate(); err != nil {
		return "", err
	}
	s.seen = true
	ref := "secret-qualification-" + binding.ID
	s.refs[ref] = binding
	return ref, nil
}

func (s *lifecycleQualificationSink) Delete(_ context.Context, binding Binding) error {
	s.deleteCalls++
	delete(s.refs, "secret-qualification-"+binding.ID)
	return nil
}

func TestQualifyLifecycleExercisesControlPlaneSagaAndCleanup(t *testing.T) {
	provider := &lifecycleQualificationProvider{
		fakeProvider: fakeProvider{
			capabilities:    testCapabilities(),
			provisionStatus: ProviderStatusPending,
			inspectStatus:   ProviderStatusReady,
			deleteDone:      true,
		},
		material: CredentialMaterial{
			ProviderIdentityID: "role-qualification",
			Username:           "gregale",
			Password:           "not-persisted",
			Database:           "gregale",
			TLSMode:            "require",
			Endpoints:          []Endpoint{{Role: EndpointPooled, Host: "pool.example.test", Port: 5432}},
		},
		issued: make(map[string]struct{}),
	}
	registry := testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
	store := NewMemoryStore()
	service, err := NewService(registry, store, ServiceOptions{
		PollInterval:        time.Second,
		ProvisioningEnabled: func() bool { return true },
		NewID:               func() string { return "database-qualification" },
		NewLeaseToken:       func() string { return "lease-qualification" },
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &lifecycleQualificationSink{refs: make(map[string]Binding)}
	bindingService, err := NewBindingService(registry, store, store, sink, BindingServiceOptions{
		ProvisioningEnabled: func() bool { return true },
		NewID:               func() string { return "binding-qualification" },
		NewLeaseToken:       func() string { return "binding-lease-qualification" },
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := QualifyLifecycle(context.Background(), service, bindingService, LifecycleQualificationOptions{
		AccountID:      "account-qualification",
		DatabaseName:   "qualification-db",
		AppID:          "app-qualification",
		Scope:          "default",
		EnvironmentKey: "DATABASE_URL",
		Access:         CredentialReadWrite,
		Spec:           testSpec(),
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("QualifyLifecycle: %v; report=%+v", err, report)
	}
	for _, check := range report.Checks {
		if !check.Passed {
			t.Fatalf("failed qualification check: %+v", check)
		}
	}
	if len(report.Checks) != 11 {
		t.Fatalf("qualification checks = %d, want 11: %+v", len(report.Checks), report.Checks)
	}
	if provider.issueCalls != 1 || provider.revokeCalls != 1 || len(provider.issued) != 0 {
		t.Fatalf("provider credentials: issue=%d revoke=%d remaining=%d", provider.issueCalls, provider.revokeCalls, len(provider.issued))
	}
	if !sink.seen || sink.putCalls != 1 || sink.deleteCalls != 1 || len(sink.refs) != 0 {
		t.Fatalf("credential sink: seen=%v puts=%d deletes=%d refs=%d", sink.seen, sink.putCalls, sink.deleteCalls, len(sink.refs))
	}
	if provider.provisionCalls != 1 || provider.inspectCalls != 1 || provider.deleteCalls != 1 {
		t.Fatalf("provider lifecycle calls: provision=%d inspect=%d delete=%d", provider.provisionCalls, provider.inspectCalls, provider.deleteCalls)
	}
}

func TestQualifyLifecycleCleansDatabaseWhenBindingFails(t *testing.T) {
	provider := &lifecycleQualificationProvider{
		fakeProvider: fakeProvider{
			capabilities:    testCapabilities(),
			provisionStatus: ProviderStatusReady,
			deleteDone:      true,
		},
		material: CredentialMaterial{
			ProviderIdentityID: "role-qualification",
			Username:           "gregale",
			Password:           "not-persisted",
			Database:           "gregale",
			TLSMode:            "require",
			Endpoints:          []Endpoint{{Role: EndpointDirect, Host: "db.example.test", Port: 5432}},
		},
		issued: make(map[string]struct{}),
	}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	service := testService(t, registry, store)
	sink := &lifecycleQualificationSink{refs: make(map[string]Binding), putErr: errors.New("secret sink unavailable")}
	bindingService, err := NewBindingService(registry, store, store, sink, BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}

	_, err = QualifyLifecycle(context.Background(), service, bindingService, LifecycleQualificationOptions{
		AccountID:      "account-qualification",
		DatabaseName:   "qualification-db",
		AppID:          "app-qualification",
		Scope:          "default",
		EnvironmentKey: "DATABASE_URL",
		Access:         CredentialReadWrite,
		Spec:           testSpec(),
		Timeout:        5 * time.Second,
	})
	if !errors.Is(err, ErrQualificationFailed) {
		t.Fatalf("QualifyLifecycle error = %v, want ErrQualificationFailed", err)
	}
	if provider.deleteCalls != 1 || provider.revokeCalls != 1 {
		t.Fatalf("cleanup calls: database delete=%d credential revoke=%d", provider.deleteCalls, provider.revokeCalls)
	}
	if _, findErr := store.FindByName(context.Background(), "account-qualification", "qualification-db"); !errors.Is(findErr, ErrNotFound) {
		t.Fatalf("database name remained after cleanup: %v", findErr)
	}
}

func TestQualifyLifecycleDiscoversReservedDatabaseAfterCreateError(t *testing.T) {
	provider := &lifecycleQualificationProvider{
		fakeProvider: fakeProvider{
			capabilities: testCapabilities(),
			provisionErr: errors.New("provider create response lost"),
			deleteDone:   true,
		},
		issued: make(map[string]struct{}),
	}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	service := testService(t, registry, store)
	sink := &lifecycleQualificationSink{refs: make(map[string]Binding)}
	bindingService, err := NewBindingService(registry, store, store, sink, BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}

	_, err = QualifyLifecycle(context.Background(), service, bindingService, LifecycleQualificationOptions{
		AccountID:      "account-qualification",
		DatabaseName:   "qualification-db",
		AppID:          "app-qualification",
		Scope:          "default",
		EnvironmentKey: "DATABASE_URL",
		Access:         CredentialReadWrite,
		Spec:           testSpec(),
		Timeout:        5 * time.Second,
	})
	if !errors.Is(err, ErrQualificationFailed) {
		t.Fatalf("QualifyLifecycle error = %v, want ErrQualificationFailed", err)
	}
	if provider.deleteCalls != 1 {
		t.Fatalf("cleanup database delete calls = %d, want 1", provider.deleteCalls)
	}
	if _, findErr := store.FindByName(context.Background(), "account-qualification", "qualification-db"); !errors.Is(findErr, ErrNotFound) {
		t.Fatalf("database name remained after cleanup: %v", findErr)
	}
}

func TestQualifyLifecycleUsesIndependentCleanupAfterTimeout(t *testing.T) {
	provider := &timeoutLifecycleQualificationProvider{
		lifecycleQualificationProvider: lifecycleQualificationProvider{
			fakeProvider: fakeProvider{
				capabilities: testCapabilities(),
				deleteDone:   true,
			},
			issued: make(map[string]struct{}),
		},
	}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	service := testService(t, registry, store)
	sink := &lifecycleQualificationSink{refs: make(map[string]Binding)}
	bindingService, err := NewBindingService(registry, store, store, sink, BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}

	_, err = QualifyLifecycle(context.Background(), service, bindingService, LifecycleQualificationOptions{
		AccountID:      "account-qualification",
		DatabaseName:   "qualification-timeout",
		AppID:          "app-qualification",
		Scope:          "default",
		EnvironmentKey: "DATABASE_URL",
		Access:         CredentialReadWrite,
		Spec:           testSpec(),
		Timeout:        25 * time.Millisecond,
	})
	if !errors.Is(err, ErrQualificationFailed) {
		t.Fatalf("QualifyLifecycle error = %v, want ErrQualificationFailed", err)
	}
	if provider.deleteCalls != 1 {
		t.Fatalf("timeout cleanup database delete calls = %d, want 1", provider.deleteCalls)
	}
	if _, findErr := store.FindByName(context.Background(), "account-qualification", "qualification-timeout"); !errors.Is(findErr, ErrNotFound) {
		t.Fatalf("database name remained after timeout cleanup: %v", findErr)
	}
}
