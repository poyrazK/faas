package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestNewStagingProvisioningGateUsesApprovalArtifact(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, func(config *Config) { config.ProvisioningEnabled = true })
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: backend.Driver,
		ResourceID:   "qualification-gate-artifact",
		Spec:         testSpec(),
		Mutating:     true,
	})
	if err != nil {
		t.Fatalf("QualifyProvider: %v", err)
	}
	lifecycle := passingLifecycleQualificationReport()
	now := report.CompletedAt.Add(time.Minute)
	approval, err := BuildQualificationApproval(report, &lifecycle, backend.ID, backend.Fingerprint, []string{"account-a"}, now, time.Hour)
	if err != nil {
		t.Fatalf("BuildQualificationApproval: %v", err)
	}
	artifact := QualificationArtifact{
		Version:            QualificationArtifactVersion,
		BackendID:          backend.ID,
		BackendFingerprint: backend.Fingerprint,
		Spec:               testSpec(),
		Report:             report,
		Lifecycle:          &lifecycle,
		Approval:           &approval,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "approval.json")
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		EnvironmentEnv:               QualificationStagingEnvironment,
		QualificationApprovalPathEnv: path,
		CanaryAccountsEnv:            "account-a",
		QualificationEnv:             "false",
		QualificationBackendEnv:      "wrong-backend",
		QualificationFingerprintEnv:  "wrong-fingerprint",
	}
	getenv := func(key string) string { return values[key] }
	if !NewStagingProvisioningGate(registry, getenv, func() time.Time { return now })() {
		t.Fatal("valid approval artifact kept the staging gate closed")
	}

	for name, mutate := range map[string]func(*QualificationArtifact){
		"tampered report": func(candidate *QualificationArtifact) {
			candidate.Approval.ReportSHA256 = "tampered"
		},
		"wrong backend": func(candidate *QualificationArtifact) {
			candidate.BackendID = "other-backend"
		},
		"expired": func(candidate *QualificationArtifact) {
			candidate.Approval.ExpiresAt = now.Add(-time.Minute)
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := artifact
			approvalCopy := *artifact.Approval
			candidate.Approval = &approvalCopy
			mutate(&candidate)
			candidateBytes, marshalErr := json.Marshal(candidate)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if writeErr := os.WriteFile(path, candidateBytes, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if NewStagingProvisioningGate(registry, getenv, func() time.Time { return now })() {
				t.Fatal("invalid approval artifact opened the staging gate")
			}
		})
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadQualificationArtifactRejectsTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval.json")
	if err := os.WriteFile(path, []byte("{\"version\":1}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadQualificationArtifact(path); err == nil {
		t.Fatal("trailing artifact data was accepted")
	}
}

func TestNewStagingCanaryAccountGate(t *testing.T) {
	values := map[string]string{}
	getenv := func(key string) string { return values[key] }
	gate := NewStagingCanaryAccountGate(getenv)
	if !gate("account-a") {
		t.Fatal("empty allowlist should keep staging accounts eligible")
	}

	values[CanaryAccountsEnv] = " account-a,account-b "
	if !gate("account-a") || !gate("account-b") {
		t.Fatal("listed account was rejected")
	}
	if gate("account-c") {
		t.Fatal("unlisted account was admitted")
	}

	for name, raw := range map[string]string{
		"empty entry":       "account-a,,account-b",
		"oversized account": "account-a," + strings.Repeat("x", 256),
	} {
		t.Run(name, func(t *testing.T) {
			values[CanaryAccountsEnv] = raw
			if gate("account-a") {
				t.Fatal("malformed allowlist opened the gate")
			}
		})
	}
	values[CanaryAccountsEnv] = strings.Repeat("account-a,", 100) + "account-a"
	if gate("account-a") {
		t.Fatal("oversized allowlist opened the gate")
	}
}

func TestBuildAndEvaluateQualificationApproval(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: "fake",
		ResourceID:   "qualification-approval",
		Spec:         testSpec(),
		Mutating:     true,
	})
	if err != nil {
		t.Fatalf("QualifyProvider: %v", err)
	}
	now := report.CompletedAt.Add(time.Minute)
	lifecycle := passingLifecycleQualificationReport()
	approval, err := BuildQualificationApproval(report, &lifecycle, "backend-default", "fingerprint-default", []string{"account-b", "account-a"}, now, time.Hour)
	if err != nil {
		t.Fatalf("BuildQualificationApproval: %v", err)
	}
	artifact := QualificationArtifact{
		Version:            qualificationArtifactVersion,
		BackendID:          "backend-default",
		BackendFingerprint: "fingerprint-default",
		Spec:               testSpec(),
		Report:             report,
		Lifecycle:          &lifecycle,
		Approval:           &approval,
		ApprovalEnv: map[string]string{
			QualificationEnv:            "true",
			QualificationBackendEnv:     approval.BackendID,
			QualificationFingerprintEnv: approval.BackendFingerprint,
			QualificationUntilEnv:       approval.ExpiresAt.UTC().Format(time.RFC3339),
			CanaryAccountsEnv:           "account-a,account-b",
		},
	}
	readiness := EvaluateQualificationArtifact(artifact, "backend-default", "fingerprint-default", []string{"account-a", "account-b"}, now)
	if !readiness.Ready || len(readiness.Reasons) != 0 {
		t.Fatalf("readiness = %+v", readiness)
	}
	if len(approval.CanaryAccounts) != 2 || approval.CanaryAccounts[0] != "account-a" {
		t.Fatalf("approval = %+v", approval)
	}
}

func TestEvaluateQualificationArtifactFailsClosed(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: "fake",
		ResourceID:   "qualification-approval-invalid",
		Spec:         testSpec(),
		Mutating:     true,
	})
	if err != nil {
		t.Fatalf("QualifyProvider: %v", err)
	}
	now := report.CompletedAt.Add(time.Minute)
	approval, err := BuildQualificationApproval(report, nil, "backend-default", "fingerprint-default", nil, now, time.Hour)
	if err != nil {
		t.Fatalf("BuildQualificationApproval: %v", err)
	}
	artifact := QualificationArtifact{
		Version:            qualificationArtifactVersion,
		BackendID:          "backend-default",
		BackendFingerprint: "fingerprint-default",
		Spec:               testSpec(),
		Report:             report,
		Approval:           &approval,
	}
	readiness := EvaluateQualificationArtifact(artifact, "other-backend", "other-fingerprint", nil, now.Add(2*time.Hour))
	if readiness.Ready {
		t.Fatal("invalid approval was reported ready")
	}
	for _, wanted := range []string{"backend_mismatch", "backend_fingerprint_mismatch", "approval_expired", "lifecycle_not_qualified"} {
		found := false
		for _, reason := range readiness.Reasons {
			if reason == wanted {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("readiness reasons = %v, missing %q", readiness.Reasons, wanted)
		}
	}
}

func TestParseStagingCanaryAccountsRejectsDuplicates(t *testing.T) {
	if _, err := ParseStagingCanaryAccounts("account-a,account-a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate allowlist error = %v", err)
	}
}

func TestRegistryVerifyQualificationArtifactChecksConfiguredBackend(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: backend.Driver,
		ResourceID:   "qualification-registry",
		Spec:         testSpec(),
		Mutating:     true,
	})
	if err != nil {
		t.Fatalf("QualifyProvider: %v", err)
	}
	now := report.CompletedAt.Add(time.Minute)
	lifecycle := passingLifecycleQualificationReport()
	approval, err := BuildQualificationApproval(report, &lifecycle, backend.ID, backend.Fingerprint, nil, now, time.Hour)
	if err != nil {
		t.Fatalf("BuildQualificationApproval: %v", err)
	}
	artifact := QualificationArtifact{
		Version:            QualificationArtifactVersion,
		BackendID:          backend.ID,
		BackendFingerprint: backend.Fingerprint,
		Spec:               testSpec(),
		Report:             report,
		Lifecycle:          &lifecycle,
		Approval:           &approval,
	}
	readiness := registry.VerifyQualificationArtifact(artifact, nil, now)
	if !readiness.Ready {
		t.Fatalf("readiness = %+v", readiness)
	}
	artifact.BackendFingerprint = "changed"
	readiness = registry.VerifyQualificationArtifact(artifact, nil, now)
	if readiness.Ready {
		t.Fatal("changed backend fingerprint was reported ready")
	}
}

func passingLifecycleQualificationReport() LifecycleQualificationReport {
	checks := make([]QualificationCheck, 0, len(requiredLifecycleQualificationChecks))
	for _, name := range requiredLifecycleQualificationChecks {
		checks = append(checks, QualificationCheck{Name: name, Passed: true})
	}
	return LifecycleQualificationReport{Checks: checks}
}
