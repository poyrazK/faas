package managedpostgres

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// ADR-20261110120000 binding review fixes: per-app binding cap, binding env
// keys a workload can receive, release-pinned refusal and session
// qualification evidence.

func bindingReviewService(t *testing.T, options BindingServiceOptions) (*BindingService, Database) {
	t.Helper()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	provider := &bindingProvider{}
	provider.capabilities = testCapabilities()
	provider.provisionStatus = ProviderStatusReady
	provider.material = bindingTestMaterial()
	registry := testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
	store := NewMemoryStore()
	databases, err := NewService(registry, store, ServiceOptions{
		LeaseDuration: time.Minute, ProviderTimeout: time.Second,
		ProvisioningEnabled: func() bool { return true },
		Now:                 func() time.Time { return now },
		NewID:               func() string { return "database-review" },
		NewLeaseToken:       func() string { return "database-lease" },
	})
	if err != nil {
		t.Fatal(err)
	}
	database, err := databases.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
	if err != nil || database.State != StateReady {
		t.Fatalf("create database: %+v %v", database, err)
	}
	next := 0
	options.LeaseDuration, options.ProviderTimeout = time.Minute, time.Second
	options.ProvisioningEnabled = func() bool { return true }
	options.Now = func() time.Time { return now }
	options.NewID = func() string { next++; return "binding-" + strconv.Itoa(next) }
	options.NewLeaseToken = func() string { return "binding-lease-" + strconv.Itoa(next) }
	service, err := NewBindingService(registry, store, store, newBindingCredentialSink(), options)
	if err != nil {
		t.Fatal(err)
	}
	return service, database
}

func TestBindingServiceCapsLiveBindingsPerApp(t *testing.T) {
	service, database := bindingReviewService(t, BindingServiceOptions{
		MaxBindingsPerApp: func(context.Context, string) (int, error) { return 2, nil },
	})
	request := func(key string) CreateBindingRequest {
		return CreateBindingRequest{AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a", Scope: "production", EnvironmentKey: key, Access: CredentialReadWrite}
	}
	for _, key := range []string{"DATABASE_URL", "REPORTS_DATABASE_URL"} {
		if _, err := service.Create(context.Background(), request(key)); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}
	// The same target is idempotent at the cap.
	if _, err := service.Create(context.Background(), request("DATABASE_URL")); err != nil {
		t.Fatalf("idempotent create at cap: %v", err)
	}
	_, err := service.Create(context.Background(), request("THIRD_DATABASE_URL"))
	var limit *BindingLimitError
	if !errors.As(err, &limit) || limit.Limit != 2 || limit.Observed != 2 || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("third binding = %v, want BindingLimitError{2, 2}", err)
	}
	// Another app has its own allowance.
	other := request("DATABASE_URL")
	other.AppID = "app-b"
	if _, err := service.Create(context.Background(), other); err != nil {
		t.Fatalf("other app: %v", err)
	}
}

func TestBindingServiceRefusesKeysTheWorkloadCannotReceive(t *testing.T) {
	service, database := bindingReviewService(t, BindingServiceOptions{})
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{key: "DATABASE_URL", want: true},
		{key: "_DATABASE_URL"},
		{key: "PORT"},
		{key: "GREGALE_API_URL"},
		{key: "FAAS_APP_ID"},
	} {
		_, err := service.Create(context.Background(), CreateBindingRequest{AccountID: "account-a", DatabaseID: database.ID, AppID: "app-" + tc.key,
			Scope: "production", EnvironmentKey: tc.key, Access: CredentialReadWrite})
		if tc.want && err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		if !tc.want && !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s = %v, want ErrInvalid", tc.key, err)
		}
	}
}

func TestBindingServiceRefusesReleasePinnedApps(t *testing.T) {
	var asked [3]string
	service, database := bindingReviewService(t, BindingServiceOptions{
		ReleaseSecretsPinned: func(_ context.Context, accountID, appID, scope string) (bool, error) {
			asked = [3]string{accountID, appID, scope}
			return scope == "production", nil
		},
	})
	request := CreateBindingRequest{AccountID: "account-a", DatabaseID: database.ID, AppID: "app-a", Scope: "production", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite}
	if _, err := service.Create(context.Background(), request); !errors.Is(err, ErrReleaseSecretsPinned) || !errors.Is(err, ErrConflict) {
		t.Fatalf("pinned create = %v", err)
	}
	if asked != [3]string{"account-a", "app-a", "production"} {
		t.Fatalf("pinned lookup = %v", asked)
	}
	request.Scope = "staging"
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatalf("unpinned scope: %v", err)
	}
}

func TestQualificationReportCarriesSessionEvidence(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "session-qualification", Spec: testSpec(), Mutating: true, Timeout: time.Minute})
	if err != nil || ValidateQualificationReport(report) != nil {
		t.Fatalf("base report: %v / %v", err, ValidateQualificationReport(report))
	}
	passed := func(names ...string) {
		for _, name := range names {
			report.Checks = append(report.Checks, QualificationCheck{Name: name, Passed: true})
		}
	}
	report.CredentialAccess = append(report.CredentialAccess, CredentialSession)
	if !errors.Is(ValidateQualificationReport(report), ErrUnavailable) {
		t.Fatal("session without evidence accepted")
	}
	report.SessionCredentials = &SessionCredentialEvidence{DirectEndpoint: true, SessionStateHeld: true, NotificationDelivered: true, PasswordRecovered: true, Revoked: true}
	passed("session_credentials_probe")
	if err := ValidateQualificationReport(report); err != nil {
		t.Fatalf("session evidence refused: %v", err)
	}
	// Every known mode fits one report.
	report.CredentialAccess = append(report.CredentialAccess, CredentialMigration, CredentialDataAPI, CredentialAuth)
	report.DataAPICredentials = &DataAPICredentialEvidence{SchemaIsolated: true, RLSEnforced: true, PasswordRecovered: true, RotationPreservesData: true, Revoked: true}
	report.AuthCredentials = &AuthCredentialEvidence{SchemaOwned: true, CustomerIsolated: true, PasswordRecovered: true, RotationPreservesData: true, Revoked: true}
	passed("data_api_credentials_probe", "auth_credentials_probe")
	if len(report.CredentialAccess) != len(knownCredentialAccess) {
		t.Fatalf("modes = %v", report.CredentialAccess)
	}
	if err := ValidateQualificationReport(report); err != nil {
		t.Fatalf("six-mode report refused: %v", err)
	}
	report.CredentialAccess = report.CredentialAccess[:2]
	report.DataAPICredentials, report.AuthCredentials = nil, nil
	if !errors.Is(ValidateQualificationReport(report), ErrInvalid) {
		t.Fatal("session evidence without the mode accepted")
	}
}

func TestSessionMaterialHasDirect(t *testing.T) {
	for _, tc := range []struct {
		endpoints []Endpoint
		want      bool
	}{
		{endpoints: []Endpoint{{Role: EndpointDirect, Host: "db", Port: 5432}}, want: true},
		{endpoints: []Endpoint{{Role: EndpointPooled, Host: "pool", Port: 5432}, {Role: EndpointDirect, Host: "db", Port: 5432}}, want: true},
		{endpoints: []Endpoint{{Role: EndpointPooled, Host: "pool", Port: 5432}}},
		{},
	} {
		if got := SessionMaterialHasDirect(CredentialMaterial{Endpoints: tc.endpoints}); got != tc.want {
			t.Fatalf("%+v = %v, want %v", tc.endpoints, got, tc.want)
		}
	}
}
