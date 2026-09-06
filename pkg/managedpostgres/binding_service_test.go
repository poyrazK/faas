package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type bindingProvider struct {
	fakeProvider
	material          CredentialMaterial
	credentialErr     error
	revokeErr         error
	issueCalls        int
	revokeCalls       int
	lastIssueRequest  CredentialRequest
	lastRevokeRequest CredentialRequest
}

func (p *bindingProvider) IssueCredentials(_ context.Context, request CredentialRequest) (CredentialMaterial, error) {
	p.issueCalls++
	p.lastIssueRequest = request
	if p.credentialErr != nil {
		return CredentialMaterial{}, p.credentialErr
	}
	material := p.material
	material.Endpoints = append([]Endpoint(nil), p.material.Endpoints...)
	return material, nil
}

func (p *bindingProvider) RevokeCredentials(_ context.Context, request CredentialRequest) error {
	p.revokeCalls++
	p.lastRevokeRequest = request
	return p.revokeErr
}

type bindingCredentialSink struct {
	putCalls    int
	deleteCalls int
	putErr      error
	deleteErr   error
	references  map[string]Binding
}

func newBindingCredentialSink() *bindingCredentialSink {
	return &bindingCredentialSink{references: make(map[string]Binding)}
}

func (s *bindingCredentialSink) Put(_ context.Context, binding Binding, _ CredentialMaterial) (string, error) {
	s.putCalls++
	if s.putErr != nil {
		return "", s.putErr
	}
	ref := "secret-" + bindingCredentialIdentity(binding)
	s.references[ref] = binding
	return ref, nil
}

func (s *bindingCredentialSink) Delete(_ context.Context, binding Binding) error {
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.references, "secret-"+bindingCredentialIdentity(binding))
	return nil
}

type failFinishBindingStore struct {
	*MemoryStore
	failOnce bool
}

func (s *failFinishBindingStore) FinishBindingProvision(ctx context.Context, bindingID, leaseToken, providerIdentityID, credentialRef string, now time.Time) (Binding, error) {
	if s.failOnce {
		s.failOnce = false
		return Binding{}, ErrConflict
	}
	return s.MemoryStore.FinishBindingProvision(ctx, bindingID, leaseToken, providerIdentityID, credentialRef, now)
}

func bindingTestMaterial() CredentialMaterial {
	return CredentialMaterial{
		ProviderIdentityID: "provider-role-a", Username: "gregale", Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []Endpoint{{Role: EndpointPooled, Host: "pool.example.test", Port: 5432}},
	}
}

func readyBindingFixture(t *testing.T, provider *bindingProvider, bindingStore *failFinishBindingStore, now *time.Time, enabled *bool) (*BindingService, *MemoryStore, Database) {
	t.Helper()
	provider.capabilities = testCapabilities()
	provider.provisionStatus = ProviderStatusReady
	provider.material = bindingTestMaterial()
	registry := testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
	databases := bindingStore.MemoryStore
	databaseService, err := NewService(registry, databases, ServiceOptions{
		LeaseDuration:       time.Minute,
		ProviderTimeout:     time.Second,
		ProvisioningEnabled: func() bool { return true },
		Now:                 func() time.Time { return *now },
		NewID:               func() string { return "database-ready" },
		NewLeaseToken:       func() string { return "database-lease" },
	})
	if err != nil {
		t.Fatal(err)
	}
	database, err := databaseService.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
	if err != nil || database.State != StateReady {
		t.Fatalf("create ready database: %+v %v", database, err)
	}
	sink := newBindingCredentialSink()
	service, err := NewBindingService(registry, databases, bindingStore, sink, BindingServiceOptions{
		LeaseDuration:       time.Minute,
		ProviderTimeout:     time.Second,
		ProvisioningEnabled: func() bool { return *enabled },
		Now:                 func() time.Time { return *now },
		NewID:               func() string { return "binding-ready" },
		NewLeaseToken:       func() string { return "binding-lease-" + now.Format("150405") },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, databases, database
}

func TestBindingServiceCreatesAndDeletesIdempotently(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	enabled := true
	provider := &bindingProvider{}
	store := &failFinishBindingStore{MemoryStore: NewMemoryStore()}
	service, _, database := readyBindingFixture(t, provider, store, &now, &enabled)
	sink := service.sink.(*bindingCredentialSink)
	request := CreateBindingRequest{
		AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite,
	}

	first, err := service.Create(context.Background(), request)
	if err != nil || first.State != BindingStateReady {
		t.Fatalf("create binding: %+v %v", first, err)
	}
	second, err := service.Create(context.Background(), request)
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent create: %+v %v", second, err)
	}
	if provider.issueCalls != 1 || sink.putCalls != 1 || first.ProviderIdentityID != provider.material.ProviderIdentityID {
		t.Fatalf("credential issue: calls=%d puts=%d binding=%+v request=%+v", provider.issueCalls, sink.putCalls, first, provider.lastIssueRequest)
	}
	if provider.lastIssueRequest.IdempotencyKey == "" || provider.lastIssueRequest.Access != CredentialReadWrite {
		t.Fatalf("credential request lost identity: %+v", provider.lastIssueRequest)
	}

	enabled = false
	deleted, err := service.Delete(context.Background(), first.AccountID, first.ID)
	if err != nil || deleted.State != BindingStateDeleted || deleted.DeletedAt == nil {
		t.Fatalf("delete while provisioning disabled: %+v %v", deleted, err)
	}
	if provider.revokeCalls != 1 || sink.deleteCalls != 1 || provider.lastRevokeRequest.IdentityKey != provider.lastIssueRequest.IdentityKey {
		t.Fatalf("credential cleanup: revoke=%d delete=%d issue=%+v revoke_request=%+v", provider.revokeCalls, sink.deleteCalls, provider.lastIssueRequest, provider.lastRevokeRequest)
	}
	if len(sink.references) != 0 {
		t.Fatalf("credential references remain after delete: %+v", sink.references)
	}
	if again, err := service.Delete(context.Background(), first.AccountID, first.ID); err != nil || again.State != BindingStateDeleted || provider.revokeCalls != 1 {
		t.Fatalf("idempotent delete: %+v revoke=%d err=%v", again, provider.revokeCalls, err)
	}
}

func TestBindingServiceRecoversCrashAfterSecretWrite(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 30, 0, 0, time.UTC)
	enabled := true
	provider := &bindingProvider{}
	store := &failFinishBindingStore{MemoryStore: NewMemoryStore(), failOnce: true}
	service, _, database := readyBindingFixture(t, provider, store, &now, &enabled)
	sink := service.sink.(*bindingCredentialSink)
	request := CreateBindingRequest{
		AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a",
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite,
	}

	if _, err := service.Create(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("lost catalog commit = %v, want ErrConflict", err)
	}
	if provider.issueCalls != 1 || sink.putCalls != 1 || len(sink.references) != 1 {
		t.Fatalf("pre-crash side effects: issue=%d put=%d refs=%d", provider.issueCalls, sink.putCalls, len(sink.references))
	}
	now = now.Add(2 * time.Minute)
	recovered, err := service.Reconcile(context.Background(), "account-a", "binding-ready")
	if err != nil || recovered.State != BindingStateReady {
		t.Fatalf("recover binding: %+v %v", recovered, err)
	}
	if provider.issueCalls != 2 || sink.putCalls != 2 || len(sink.references) != 1 {
		t.Fatalf("recovery was not idempotent: issue=%d put=%d refs=%d", provider.issueCalls, sink.putCalls, len(sink.references))
	}
	if recovered.ProviderIdentityID != provider.material.ProviderIdentityID {
		t.Fatalf("provider identity = %q, want %q", recovered.ProviderIdentityID, provider.material.ProviderIdentityID)
	}
}

func TestBindingServiceSanitizesProviderAndSinkFailures(t *testing.T) {
	now := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	enabled := true
	provider := &bindingProvider{credentialErr: errors.New("password=never-return-this")}
	store := &failFinishBindingStore{MemoryStore: NewMemoryStore()}
	service, _, database := readyBindingFixture(t, provider, store, &now, &enabled)
	request := CreateBindingRequest{
		AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a",
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite,
	}
	_, err := service.Create(context.Background(), request)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "never-return-this") {
		t.Fatalf("provider error was not sanitized: %v", err)
	}
	bindings, listErr := service.List(context.Background(), "account-a", database.ID)
	if listErr != nil || len(bindings) != 1 || bindings[0].State != BindingStateFailed || bindings[0].LastErrorCode != "credential_issue_unavailable" {
		t.Fatalf("durable provider failure: %+v %v", bindings, listErr)
	}
	if !bindings[0].RetryAt.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("retry_at = %s", bindings[0].RetryAt)
	}
}

func TestBindingReconcilerRespectsDarkGateButAlwaysCleansUp(t *testing.T) {
	now := time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)
	enabled := true
	provider := &bindingProvider{}
	store := &failFinishBindingStore{MemoryStore: NewMemoryStore()}
	service, _, database := readyBindingFixture(t, provider, store, &now, &enabled)
	sink := service.sink.(*bindingCredentialSink)
	request := CreateBindingRequest{
		AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a",
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite,
	}
	ready, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	sink.deleteErr = ErrUnavailable
	if _, err := service.Delete(context.Background(), ready.AccountID, ready.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first delete = %v", err)
	}
	enabled = false
	sink.deleteErr = nil
	now = now.Add(30 * time.Second)
	reconciler, err := NewBindingReconciler(service, BindingReconcilerOptions{
		Interval:            time.Second,
		Now:                 func() time.Time { return now },
		IncludeProvisioning: func() bool { return enabled },
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.Sweep(context.Background())
	if err != nil || summary.Completed != 1 || summary.Discovered != 1 {
		t.Fatalf("cleanup sweep: %+v %v", summary, err)
	}
	deleted, err := service.Get(context.Background(), ready.AccountID, ready.ID)
	if err != nil || deleted.State != BindingStateDeleted {
		t.Fatalf("deleted binding: %+v %v", deleted, err)
	}
}
