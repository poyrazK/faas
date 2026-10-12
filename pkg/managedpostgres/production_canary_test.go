package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func productionCanaryFixture(t *testing.T) (*Registry, QualificationArtifact, map[string]string, time.Time) {
	t.Helper()
	provider := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, func(config *Config) {
		config.ProvisioningEnabled = true
		config.Usage = UsageConfig{
			Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1000,
			MaxMonthlyComputeUnitSeconds: 1000, MaxMonthlyStorageByteSeconds: 1 << 50,
			MaxMonthlyHistoryByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 30,
			ComputeUnitHourMillicents: 1, StorageGiBHourMillicents: 1,
			HistoryGiBHourMillicents: 1, EgressGiBMillicents: 1,
		}
	})
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := QualifyProvider(context.Background(), provider, QualificationOptions{
		ProviderName: backend.Driver, ResourceID: "production-canary-qualification",
		Spec: testSpec(), Mutating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := passingLifecycleQualificationReport()
	now := report.CompletedAt.Add(time.Minute)
	approval, err := BuildQualificationApproval(report, &lifecycle, backend.ID, backend.Fingerprint, []string{"account-a"}, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	artifact := QualificationArtifact{
		Version: QualificationArtifactVersion, BackendID: backend.ID,
		BackendFingerprint: backend.Fingerprint, Spec: testSpec(), Report: report,
		Lifecycle: &lifecycle, Approval: &approval,
	}
	values := map[string]string{
		EnvironmentEnv: "production", ProductionCanaryEnv: "true",
		QualificationApprovalPathEnv: filepath.Join(t.TempDir(), "approval.json"),
		CanaryAccountsEnv:            "account-a",
	}
	return registry, artifact, values, now
}

func writeCanaryArtifact(t *testing.T, path string, artifact QualificationArtifact) {
	t.Helper()
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProductionCanaryRequiresBoundedQualifiedAdmission(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Registry, *QualificationArtifact, map[string]string, time.Time)
		want   bool
	}{
		{name: "qualified", want: true},
		{name: "opt in absent", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			delete(env, ProductionCanaryEnv)
		}},
		{name: "opt in false", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			env[ProductionCanaryEnv] = "false"
		}},
		{name: "opt in malformed", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			env[ProductionCanaryEnv] = "enable"
		}},
		{name: "wrong environment", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			env[EnvironmentEnv] = "development"
		}},
		{name: "empty allowlist", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			env[CanaryAccountsEnv] = ""
		}},
		{name: "malformed allowlist", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			env[CanaryAccountsEnv] = "account-a,"
		}},
		{name: "different account", mutate: func(_ *Registry, _ *QualificationArtifact, env map[string]string, _ time.Time) {
			env[CanaryAccountsEnv] = "account-b"
		}},
		{name: "configuration disabled", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.ProvisioningEnabled = false
		}},
		{name: "metering disabled", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) { r.usage.Enabled = false }},
		{name: "no compute price", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.usage.ComputeUnitHourMillicents = 0
		}},
		{name: "no storage price", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.usage.StorageGiBHourMillicents = 0
		}},
		{name: "no history price", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.usage.HistoryGiBHourMillicents = 0
		}},
		{name: "no egress price", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.usage.EgressGiBMillicents = 0
		}},
		{name: "no history ceiling", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.usage.MaxMonthlyHistoryByteSeconds = 0
		}},
		{name: "no spend ceiling", mutate: func(r *Registry, _ *QualificationArtifact, _ map[string]string, _ time.Time) {
			r.usage.MaxMonthlyCostMillicents = 0
		}},
		{name: "approval expired", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, now time.Time) {
			a.Approval.ExpiresAt = now
		}},
		{name: "long approval", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, _ time.Time) {
			a.Approval.ExpiresAt = a.Approval.QualifiedAt.Add(24*time.Hour + time.Second)
		}},
		{name: "24 hour approval", want: true, mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, _ time.Time) {
			a.Approval.ExpiresAt = a.Approval.QualifiedAt.Add(24 * time.Hour)
		}},
		{name: "future qualification", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, now time.Time) {
			a.Approval.QualifiedAt = now.Add(time.Minute)
		}},
		{name: "approval missing", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, _ time.Time) { a.Approval = nil }},
		{name: "durable evidence missing", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, _ time.Time) { a.Lifecycle = nil }},
		{name: "obsolete version", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, _ time.Time) { a.Version = 7 }},
		{name: "tampered evidence", mutate: func(_ *Registry, a *QualificationArtifact, _ map[string]string, _ time.Time) {
			a.Approval.ReportSHA256 = "tampered"
		}},
		{name: "legacy approval only", mutate: func(_ *Registry, a *QualificationArtifact, env map[string]string, _ time.Time) {
			delete(env, QualificationApprovalPathEnv)
			env[QualificationEnv] = "true"
			env[QualificationVersionEnv] = "8"
			env[QualificationBackendEnv] = a.BackendID
			env[QualificationFingerprintEnv] = a.BackendFingerprint
			env[QualificationUntilEnv] = a.Approval.ExpiresAt.Format(time.RFC3339)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, artifact, env, now := productionCanaryFixture(t)
			if test.mutate != nil {
				test.mutate(registry, &artifact, env, now)
			}
			if path := env[QualificationApprovalPathEnv]; path != "" {
				writeCanaryArtifact(t, path, artifact)
			}
			gate := NewProvisioningGate(registry, func(key string) string { return env[key] }, func() time.Time { return now })
			if got := gate(); got != test.want {
				t.Fatalf("gate = %t, want %t", got, test.want)
			}
		})
	}
}

func TestProductionCanaryExpiresWithoutDaemonRestart(t *testing.T) {
	registry, artifact, env, now := productionCanaryFixture(t)
	writeCanaryArtifact(t, env[QualificationApprovalPathEnv], artifact)
	getenv := func(key string) string { return env[key] }
	gate := NewProvisioningGate(registry, getenv, func() time.Time { return now })
	if !gate() {
		t.Fatal("qualified canary was denied")
	}
	now = artifact.Approval.ExpiresAt
	if gate() {
		t.Fatal("expired canary remained open")
	}
	now = artifact.Approval.QualifiedAt.Add(time.Minute)
	env[ProductionCanaryEnv] = "false"
	if gate() {
		t.Fatal("disabled canary remained open")
	}
}

func TestProductionCanaryAccountGateRejectsWidening(t *testing.T) {
	env := map[string]string{EnvironmentEnv: "production", ProductionCanaryEnv: "true", CanaryAccountsEnv: "account-a"}
	gate := NewCanaryAccountGate(func(key string) string { return env[key] })
	if !gate("account-a") || gate("account-b") || gate("") {
		t.Fatal("production account allowlist was not enforced")
	}
	for _, raw := range []string{"", "account-a,", "account-a,account-a"} {
		env[CanaryAccountsEnv] = raw
		if gate("account-a") || gate("account-b") {
			t.Fatalf("invalid allowlist %q widened production access", raw)
		}
	}
	env[CanaryAccountsEnv] = "account-a"
	env[ProductionCanaryEnv] = "false"
	if gate("account-a") {
		t.Fatal("disabled production canary allowed an account")
	}
	env[EnvironmentEnv] = "staging"
	env[CanaryAccountsEnv] = ""
	if !gate("account-a") {
		t.Fatal("existing staging eligibility changed")
	}
}

func TestProductionCanaryKeepsReadsAndCleanupAfterExpiry(t *testing.T) {
	registry, artifact, env, now := productionCanaryFixture(t)
	writeCanaryArtifact(t, env[QualificationApprovalPathEnv], artifact)
	provider := &fakeProvider{
		capabilities: testCapabilities(), provisionStatus: ProviderStatusReady,
		inspectStatus: ProviderStatusReady, deleteDone: true,
	}
	backend := registry.backends[artifact.BackendID]
	backend.Provider = provider
	registry.backends[backend.ID] = backend
	getenv := func(key string) string { return env[key] }
	accountGate := NewCanaryAccountGate(getenv)
	service, err := NewService(registry, NewMemoryStore(), ServiceOptions{
		ProvisioningEnabled: NewProvisioningGate(registry, getenv, func() time.Time { return now }),
		ProvisioningAllowed: func(_ context.Context, accountID string) bool { return accountGate(accountID) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := service.Create(ctx, CreateRequest{AccountID: "account-b", Name: "denied", Spec: testSpec()}); !errors.Is(err, ErrUnavailable) || provider.provisionCalls != 0 {
		t.Fatalf("outside-account create = %v, provider calls = %d", err, provider.provisionCalls)
	}
	database, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "canary", Spec: testSpec()})
	if err != nil || database.State != StateReady {
		t.Fatalf("canary create = %v, state = %s", err, database.State)
	}
	now = artifact.Approval.ExpiresAt
	if _, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "expired", Spec: testSpec()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired create = %v", err)
	}
	if _, err := service.Get(ctx, "account-a", database.ID); err != nil {
		t.Fatalf("read after expiry = %v", err)
	}
	deleted, err := service.Delete(ctx, "account-a", database.ID)
	if err != nil || deleted.State != StateDeleted || provider.deleteCalls != 1 {
		t.Fatalf("delete after expiry = %v, state = %s, provider calls = %d", err, deleted.State, provider.deleteCalls)
	}
}
