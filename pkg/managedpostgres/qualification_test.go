package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type qualificationProvider struct {
	capabilities Capabilities
	resourceID   string
	deleted      bool
	provision    int
	inspect      int
	usage        int
	issue        int
	revoke       int
	delete       int
	issueErr     error
}

func (p *qualificationProvider) Capabilities() Capabilities { return p.capabilities }

func (p *qualificationProvider) Provision(_ context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	p.provision++
	if p.resourceID == "" {
		p.resourceID = "provider-resource"
	}
	return ObservedDatabase{ProviderResourceID: p.resourceID, Status: ProviderStatusReady, Spec: request.Spec}, nil
}

func (*qualificationProvider) Restore(context.Context, RestoreRequest) (ObservedDatabase, error) {
	return ObservedDatabase{}, ErrUnsupported
}

func (p *qualificationProvider) Inspect(_ context.Context, providerResourceID string) (ObservedDatabase, error) {
	p.inspect++
	return ObservedDatabase{ProviderResourceID: providerResourceID, Status: ProviderStatusReady}, nil
}

func (*qualificationProvider) Update(context.Context, UpdateRequest) (ObservedDatabase, error) {
	return ObservedDatabase{}, ErrUnsupported
}

func (p *qualificationProvider) Delete(_ context.Context, request DeleteRequest) (DeleteResult, error) {
	p.delete++
	if request.ProviderResourceID == "" && request.ResourceID == "" {
		return DeleteResult{}, ErrInvalid
	}
	p.deleted = true
	return DeleteResult{Done: true}, nil
}

func (p *qualificationProvider) IssueCredentials(context.Context, CredentialRequest) (CredentialMaterial, error) {
	p.issue++
	if p.issueErr != nil {
		return CredentialMaterial{}, p.issueErr
	}
	return CredentialMaterial{
		ProviderIdentityID: "identity-qualification",
		Username:           "gregale",
		Password:           "qualification-secret",
		Database:           "gregale",
		TLSMode:            "require",
		Endpoints:          []Endpoint{{Role: EndpointPooled, Host: "pool.example.test", Port: 5432}},
	}, nil
}

func (p *qualificationProvider) RevokeCredentials(context.Context, CredentialRequest) error {
	p.revoke++
	return nil
}

func (*qualificationProvider) ProbeScaleToZero(context.Context, string, CredentialMaterial) (ScaleToZeroProbeResult, error) {
	return ScaleToZeroProbeResult{Suspended: true, Resumed: true, WakeLatency: 250 * time.Millisecond}, nil
}

func (p *qualificationProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	p.usage++
	return Usage{Window: window, Readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 1}}}, nil
}

func TestQualifyProviderCapabilityOnlyIsNonMutating(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: "fake",
		ResourceID:   "qualification-capability-only",
		Spec:         testSpec(),
	})
	if err != nil {
		t.Fatalf("QualifyProvider: %v", err)
	}
	if report.Mutating || len(report.Checks) != 5 || provider.provision != 0 {
		t.Fatalf("report = %+v, provider = %+v", report, provider)
	}
	for _, check := range report.Checks {
		if !check.Passed {
			t.Fatalf("failed check: %+v", check)
		}
	}
}

func TestQualifyProviderExercisesLifecycleAndCleansUp(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: "fake",
		ResourceID:   "qualification-lifecycle",
		Spec:         testSpec(),
		Mutating:     true,
		Timeout:      time.Minute,
	})
	if err != nil {
		t.Fatalf("QualifyProvider: %v", err)
	}
	if !provider.deleted || provider.provision != 2 || provider.inspect != 1 || provider.usage != 1 || provider.issue != 1 || provider.revoke != 1 || provider.delete != 2 {
		t.Fatalf("provider calls = %+v", provider)
	}
	if len(report.Checks) != 21 {
		t.Fatalf("checks = %d (%+v)", len(report.Checks), report.Checks)
	}
	if report.ScaleToZero == nil || !report.ScaleToZero.Suspended || !report.ScaleToZero.Resumed || report.ScaleToZero.WakeLatencyMS != 250 {
		t.Fatalf("scale-to-zero evidence = %+v", report.ScaleToZero)
	}
}

func TestQualifyProviderCleansUpAfterCredentialFailure(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities(), issueErr: errors.New("password must not appear")}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: "fake",
		ResourceID:   "qualification-failure",
		Spec:         testSpec(),
		Mutating:     true,
	})
	if !errors.Is(err, ErrQualificationFailed) || !provider.deleted || provider.delete == 0 {
		t.Fatalf("err = %v, provider = %+v", err, provider)
	}
	for _, check := range report.Checks {
		if check.Error == "password must not appear" {
			t.Fatal("provider error leaked into qualification report")
		}
	}
}

func TestNewStagingProvisioningGateRequiresExactQualification(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	values := map[string]string{
		EnvironmentEnv:              "staging",
		QualificationEnv:            "true",
		QualificationBackendEnv:     backend.ID,
		QualificationFingerprintEnv: backend.Fingerprint,
		QualificationUntilEnv:       now.Add(time.Hour).Format(time.RFC3339),
	}
	getenv := func(key string) string { return values[key] }
	if !NewStagingProvisioningGate(registry, getenv, func() time.Time { return now })() {
		t.Fatal("fully qualified staging gate stayed closed")
	}
	tests := map[string]func(map[string]string){
		"production": func(values map[string]string) { values[EnvironmentEnv] = "production" },
		"approval":   func(values map[string]string) { values[QualificationEnv] = "false" },
		"expired": func(values map[string]string) {
			values[QualificationUntilEnv] = now.Add(-time.Minute).Format(time.RFC3339)
		},
		"backend": func(values map[string]string) { values[QualificationBackendEnv] = "other" },
		"fingerprint": func(values map[string]string) {
			values[QualificationFingerprintEnv] = "different"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := make(map[string]string, len(values))
			for key, value := range values {
				candidate[key] = value
			}
			mutate(candidate)
			if NewStagingProvisioningGate(registry, func(key string) string { return candidate[key] }, func() time.Time { return now })() {
				t.Fatal("unqualified staging gate opened")
			}
		})
	}
}
