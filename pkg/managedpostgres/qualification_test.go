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
	capabilities      Capabilities
	resourceID        string
	deleted           bool
	provision         int
	inspect           int
	usage             int
	restore           int
	issue             int
	revoke            int
	delete            int
	issueErr          error
	spec              Spec
	pointInTime       time.Time
	privilegeErr      error
	privilegeEvidence *CredentialPrivilegeEvidence
	isolationErr      error
}

func (p *qualificationProvider) Capabilities() Capabilities { return p.capabilities }

func (p *qualificationProvider) Provision(_ context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	p.provision++
	p.spec = request.Spec
	if p.resourceID == "" {
		p.resourceID = "provider-resource"
	}
	return ObservedDatabase{ProviderResourceID: p.resourceID, Status: ProviderStatusReady, Spec: request.Spec}, nil
}

func (p *qualificationProvider) Restore(_ context.Context, request RestoreRequest) (ObservedDatabase, error) {
	p.restore++
	p.pointInTime = request.PointInTime
	return ObservedDatabase{ProviderResourceID: "restored-" + request.ResourceID, Status: ProviderStatusReady, Spec: request.Spec}, nil
}

func (p *qualificationProvider) Inspect(_ context.Context, providerResourceID string) (ObservedDatabase, error) {
	p.inspect++
	return ObservedDatabase{ProviderResourceID: providerResourceID, Status: ProviderStatusReady, Spec: p.spec}, nil
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

func (p *qualificationProvider) ProbeCredentialPrivileges(context.Context, string, CredentialMaterial) (CredentialPrivilegeEvidence, error) {
	if p.privilegeEvidence != nil {
		return *p.privilegeEvidence, p.privilegeErr
	}
	return CredentialPrivilegeEvidence{RuntimeRestricted: true, MigrationSeparated: true, RotationPreservesData: true}, p.privilegeErr
}
func (p *qualificationProvider) VerifyRestoreCredentialIsolation(context.Context, CredentialMaterial, CredentialMaterial) error {
	return p.isolationErr
}

func (p *qualificationProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	p.usage++
	return Usage{Window: window, Readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 1}}}, nil
}

func (*qualificationProvider) PrepareRestore(context.Context, string, CredentialMaterial) (RestoreProbe, error) {
	return RestoreProbe{PointInTime: time.Now().UTC(), Marker: "before"}, nil
}

func (*qualificationProvider) VerifyRestore(context.Context, string, CredentialMaterial, RestoreProbe) error {
	return nil
}

func (*qualificationProvider) CleanupRestore(context.Context, string, CredentialMaterial) error {
	return nil
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
	if !provider.deleted || provider.provision != 2 || provider.inspect != 2 || provider.usage != 1 || provider.restore != 1 || provider.issue != 3 || provider.revoke != 2 || provider.delete != 3 {
		t.Fatalf("provider calls = %+v", provider)
	}
	if len(report.Checks) != 35 {
		t.Fatalf("checks = %d (%+v)", len(report.Checks), report.Checks)
	}
	if report.ScaleToZero == nil || !report.ScaleToZero.Suspended || !report.ScaleToZero.Resumed || report.ScaleToZero.WakeLatencyMS != 250 {
		t.Fatalf("scale-to-zero evidence = %+v", report.ScaleToZero)
	}
	if report.Restore == nil || !report.Restore.Restored || !report.Restore.DataVerified || !report.Restore.Deleted {
		t.Fatalf("restore evidence = %+v", report.Restore)
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
		QualificationVersionEnv:     "3",
		QualificationBackendEnv:     backend.ID,
		QualificationFingerprintEnv: backend.Fingerprint,
		QualificationUntilEnv:       now.Add(time.Hour).Format(time.RFC3339),
	}
	getenv := func(key string) string { return values[key] }
	if !NewStagingProvisioningGate(registry, getenv, func() time.Time { return now })() {
		t.Fatal("fully qualified staging gate stayed closed")
	}
	tests := map[string]func(map[string]string){
		"production":        func(values map[string]string) { values[EnvironmentEnv] = "production" },
		"approval":          func(values map[string]string) { values[QualificationEnv] = "false" },
		"unversioned":       func(values map[string]string) { delete(values, QualificationVersionEnv) },
		"previous contract": func(values map[string]string) { values[QualificationVersionEnv] = "2" },
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
			QualificationVersionEnv:     "3",
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
	missingRestore := artifact
	missingRestore.Report.Restore = nil
	missingRestore.Approval = func() *QualificationApproval {
		copy := *artifact.Approval
		copy.ReportSHA256 = qualificationReportSHA256(missingRestore.Report)
		return &copy
	}()
	readiness = EvaluateQualificationArtifact(missingRestore, "backend-default", "fingerprint-default", nil, now)
	if readiness.Ready {
		t.Fatal("artifact without restore evidence was reported ready")
	}
	foundRestoreReason := false
	for _, reason := range readiness.Reasons {
		if reason == "restore_evidence_missing" {
			foundRestoreReason = true
			break
		}
	}
	if !foundRestoreReason {
		t.Fatalf("readiness reasons = %v, missing restore_evidence_missing", readiness.Reasons)
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

type restoreQualificationProvider struct {
	*qualificationProvider
	prepareErr      error
	verifyErr       error
	oldPoint        bool
	pendingInspects int
	pendingDeletes  int
	cleanup         int
}

func (p *restoreQualificationProvider) PrepareRestore(ctx context.Context, id string, material CredentialMaterial) (RestoreProbe, error) {
	probe, _ := p.qualificationProvider.PrepareRestore(ctx, id, material)
	if p.oldPoint {
		probe.PointInTime = probe.PointInTime.Add(-time.Hour)
	}
	return probe, p.prepareErr
}

func (p *restoreQualificationProvider) VerifyRestore(context.Context, string, CredentialMaterial, RestoreProbe) error {
	return p.verifyErr
}

func (p *restoreQualificationProvider) CleanupRestore(context.Context, string, CredentialMaterial) error {
	p.cleanup++
	return nil
}

func (p *restoreQualificationProvider) Inspect(ctx context.Context, id string) (ObservedDatabase, error) {
	observed, err := p.qualificationProvider.Inspect(ctx, id)
	if strings.HasPrefix(id, "restored-") && p.pendingInspects > 0 {
		p.pendingInspects--
		observed.Status = ProviderStatusPending
	}
	return observed, err
}

func (p *restoreQualificationProvider) Delete(ctx context.Context, request DeleteRequest) (DeleteResult, error) {
	if strings.HasPrefix(request.ProviderResourceID, "restored-") && p.pendingDeletes > 0 {
		p.pendingDeletes--
		return DeleteResult{Done: false}, nil
	}
	return p.qualificationProvider.Delete(ctx, request)
}

func TestRestoreQualificationVerifiesDataAndWaitsForOperations(t *testing.T) {
	for _, test := range []struct {
		name       string
		prepareErr error
		verifyErr  error
		oldPoint   bool
		wantCheck  string
	}{
		{name: "success"},
		{name: "before resource lifetime", oldPoint: true, wantCheck: "restore_prepare"},
		{name: "prepare failure", prepareErr: errors.New("private connection password"), wantCheck: "restore_prepare"},
		{name: "wrong restored data", verifyErr: ErrUnavailable, wantCheck: "restore_data_verified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &restoreQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: testCapabilities()},
				prepareErr: test.prepareErr, verifyErr: test.verifyErr, oldPoint: test.oldPoint, pendingInspects: 2, pendingDeletes: 2}
			report, err := QualifyProvider(context.Background(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "restore-data-probe",
				Spec: testSpec(), Mutating: true, Timeout: time.Second, PollInterval: time.Microsecond})
			if provider.cleanup != 1 || !provider.deleted {
				t.Fatalf("cleanup = %+v", provider)
			}
			if test.wantCheck == "" {
				if err != nil || ValidateQualificationReport(report) != nil || provider.pendingInspects != 0 || provider.pendingDeletes != 0 {
					t.Fatalf("report = %+v, %v", report, err)
				}
				if provider.pointInTime.Before(report.StartedAt) {
					t.Fatal("restore predates resource")
				}
				return
			}
			if !errors.Is(err, ErrQualificationFailed) || ValidateQualificationReport(report) == nil {
				t.Fatalf("failed probe approved: %+v, %v", report, err)
			}
			found := false
			for _, check := range report.Checks {
				if check.Name == test.wantCheck && !check.Passed {
					found = true
				}
				if strings.Contains(check.Error, "password") {
					t.Fatal("private error leaked")
				}
			}
			if !found {
				t.Fatalf("missing failed check %s: %+v", test.wantCheck, report)
			}
		})
	}
}

type lostQualificationCreateProvider struct{ *qualificationProvider }

func (p *lostQualificationCreateProvider) Provision(ctx context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	_, _ = p.qualificationProvider.Provision(ctx, request)
	return ObservedDatabase{}, ErrUnavailable
}

func TestQualificationCleansUpLostProvisionResponseByLogicalIdentity(t *testing.T) {
	provider := &lostQualificationCreateProvider{qualificationProvider: &qualificationProvider{capabilities: testCapabilities()}}
	_, err := QualifyProvider(context.Background(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "lost-create-response", Spec: testSpec(), Mutating: true})
	if !errors.Is(err, ErrQualificationFailed) || !provider.deleted || provider.delete != 1 {
		t.Fatalf("lost create cleanup = %+v, %v", provider, err)
	}
}

func TestQualificationRequiresCredentialPrivilegesAndRestoreIsolation(t *testing.T) {
	for _, kind := range []string{"privilege error", "privilege evidence", "restore isolation"} {
		t.Run(kind, func(t *testing.T) {
			provider := &qualificationProvider{capabilities: testCapabilities()}
			want := "credential_privileges_probe"
			switch kind {
			case "privilege error":
				provider.privilegeErr = ErrUnavailable
			case "privilege evidence":
				provider.privilegeEvidence = &CredentialPrivilegeEvidence{RuntimeRestricted: true}
			case "restore isolation":
				provider.isolationErr = ErrUnavailable
				want = "restore_credentials_isolated"
			}
			report, err := QualifyProvider(context.Background(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "credential-qualification", Spec: testSpec(), Mutating: true, Timeout: time.Minute})
			if !errors.Is(err, ErrQualificationFailed) || !provider.deleted {
				t.Fatalf("qualification cleanup: deleted=%v err=%v", provider.deleted, err)
			}
			if report.Checks[len(report.Checks)-1].Name != want || report.Checks[len(report.Checks)-1].Passed {
				t.Fatalf("failed checks=%+v", report.Checks)
			}
			if _, err := BuildQualificationApproval(report, nil, "backend", "fingerprint", nil, time.Now(), time.Hour); err == nil {
				t.Fatal("failed privileges generated approval")
			}
		})
	}
}

func TestQualificationRejectsPreviousPrivilegeContractAndMissingEvidence(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "credential-qualification", Spec: testSpec(), Mutating: true, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := report.CompletedAt.Add(time.Minute)
	approval, err := BuildQualificationApproval(report, nil, "backend", "fingerprint", nil, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	artifact := QualificationArtifact{Version: 2, BackendID: "backend", BackendFingerprint: "fingerprint", Spec: testSpec(), Report: report, Approval: &approval}
	artifact.Approval.Version = 2
	result := EvaluateQualificationArtifact(artifact, "backend", "fingerprint", nil, now)
	if result.Ready || !strings.Contains(strings.Join(result.Reasons, ","), "artifact_version_invalid") {
		t.Fatalf("old contract accepted: %+v", result)
	}
	report.CredentialPrivileges = nil
	if !errors.Is(ValidateQualificationReport(report), ErrUnavailable) {
		t.Fatal("missing credential evidence accepted")
	}
}
