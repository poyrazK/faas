package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type operatorUsageStore struct {
	UsageStore
	snapshot UsageSnapshot
}

func (s *operatorUsageStore) UsageSnapshot(context.Context, string, time.Time) (UsageSnapshot, error) {
	return s.snapshot, nil
}

func pendingOperatorUsage(now time.Time) UsageSnapshot {
	end := now.UTC().Truncate(time.Hour)
	return UsageSnapshot{Databases: []UsageProgress{{Window: time.Hour, CreatedAt: end.Add(-30 * time.Minute), EndedAt: end.Add(-10 * time.Minute), Terminal: true, CollectedFrom: end.Add(-time.Hour), CollectedUntil: end, ObservedAt: now, CorrectionObservedAt: now}}}
}

func TestOperatorTestUsageDeferralRequiresExactLiveApproval(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*OperatorTestApproval, map[string]string, *time.Time)
		account string
		want    bool
	}{
		{name: "explicit bounded test", want: true},
		{name: "option absent", change: func(a *OperatorTestApproval, _ map[string]string, _ *time.Time) {
			a.AllowPendingFinalCorrections = false
		}},
		{name: "other account", account: "account-b"},
		{name: "ordinary staging", change: func(_ *OperatorTestApproval, e map[string]string, _ *time.Time) { e[EnvironmentEnv] = "staging" }},
		{name: "canary disabled", change: func(_ *OperatorTestApproval, e map[string]string, _ *time.Time) { e[ProductionCanaryEnv] = "false" }},
		{name: "approval missing", change: func(_ *OperatorTestApproval, e map[string]string, _ *time.Time) { delete(e, OperatorTestPathEnv) }},
		{name: "wrong placement", change: func(a *OperatorTestApproval, _ map[string]string, _ *time.Time) { a.BackendFingerprint = "other" }},
		{name: "empty allowlist", change: func(_ *OperatorTestApproval, e map[string]string, _ *time.Time) { e[CanaryAccountsEnv] = "" }},
		{name: "too long", change: func(a *OperatorTestApproval, _ map[string]string, _ *time.Time) {
			a.ExpiresAt = a.StartedAt.Add(3*time.Hour + time.Second)
		}},
		{name: "expired", change: func(a *OperatorTestApproval, _ map[string]string, n *time.Time) { *n = a.ExpiresAt }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, artifact, env, now := productionCanaryFixture(t)
			store := &operatorUsageStore{snapshot: pendingOperatorUsage(now)}
			approval := OperatorTestApproval{Version: 1, Purpose: "operator_test", BackendID: artifact.BackendID, BackendFingerprint: artifact.BackendFingerprint, CanaryAccounts: []string{"account-a"}, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), AllowPendingFinalCorrections: true}
			path := filepath.Join(t.TempDir(), "operator-test.json")
			env[OperatorTestPathEnv] = path
			if tc.change != nil {
				tc.change(&approval, env, &now)
			}
			encoded, _ := json.Marshal(approval)
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			account := tc.account
			if account == "" {
				account = "account-a"
			}
			admit := NewOperatorTestUsageAdmission(registry, store, func(k string) string { return env[k] }, func() time.Time { return now })
			err := admit(context.Background(), account, UsageCeilings{})
			if tc.want && err != nil {
				t.Fatalf("approved fully collected test blocked: %v", err)
			}
			if !tc.want && !errors.Is(err, ErrUsageStale) {
				t.Fatalf("ordinary or invalid approval relaxed admission: %v", err)
			}
			if tc.want {
				if !store.snapshot.Stale(registry.UsagePolicy(), now) {
					t.Fatal("operator admission asserted final settlement")
				}
				if !errors.Is(registry.UsagePolicy().Admit(context.Background(), store, account, now), ErrUsageStale) {
					t.Fatal("ordinary admission changed")
				}
				now = approval.ExpiresAt
				if !errors.Is(admit(context.Background(), account, UsageCeilings{}), ErrUsageStale) {
					t.Fatal("deferral survived approval expiry without restart")
				}
			}
		})
	}
}

func TestOperatorTestUsageDeferralKeepsEvidenceAndLimits(t *testing.T) {
	now := time.Date(2026, 10, 14, 11, 20, 0, 0, time.UTC)
	policy := enabledUsagePolicy()
	for _, tc := range []struct {
		name     string
		change   func(*UsageSnapshot)
		ceilings UsageCeilings
		want     error
	}{
		{name: "fully collected pending correction"},
		{name: "unknown identity", change: func(s *UsageSnapshot) { s.Databases[0].Unresolved = true }, want: ErrUsageStale},
		{name: "missing observation", change: func(s *UsageSnapshot) { s.Databases[0].ObservedAt = time.Time{} }, want: ErrUsageStale},
		{name: "old observation", change: func(s *UsageSnapshot) { s.Databases[0].ObservedAt = now.Add(-policy.StaleAfter - time.Second) }, want: ErrUsageStale},
		{name: "old correction observation", change: func(s *UsageSnapshot) {
			s.Databases[0].CorrectionObservedAt = now.Add(-policy.StaleAfter - time.Second)
		}, want: ErrUsageStale},
		{name: "future observation", change: func(s *UsageSnapshot) { s.Databases[0].ObservedAt = now.Add(time.Second) }, want: ErrUsageStale},
		{name: "observation before covered window closes", change: func(s *UsageSnapshot) { s.Databases[0].ObservedAt = now.Truncate(time.Hour).Add(-time.Second) }, want: ErrUsageStale},
		{name: "future correction observation", change: func(s *UsageSnapshot) { s.Databases[0].CorrectionObservedAt = now.Add(time.Second) }, want: ErrUsageStale},
		{name: "coverage hole", change: func(s *UsageSnapshot) { s.Databases[0].CollectedUntil = now.Truncate(time.Hour).Add(-time.Hour) }, want: ErrUsageStale},
		{name: "missing first window", change: func(s *UsageSnapshot) { s.Databases[0].CollectedFrom = now.Truncate(time.Hour) }, want: ErrUsageStale},
		{name: "unconfirmed shutdown", change: func(s *UsageSnapshot) { s.Databases[0].EndedAt = time.Time{} }, want: ErrUsageStale},
		{name: "active stale resource", change: func(s *UsageSnapshot) {
			s.Databases[0].Terminal = false
			s.Databases[0].ObservedAt = now.Add(-policy.StaleAfter - time.Second)
		}, want: ErrUsageStale},
		{name: "missing active coverage", change: func(s *UsageSnapshot) { s.ReadyDatabases = 2 }, want: ErrUsageStale},
		{name: "cost ceiling", change: func(s *UsageSnapshot) { s.CostMillicents = policy.MaxMonthlyCostMillicents }, want: ErrQuotaExceeded},
		{name: "compute ceiling", change: func(s *UsageSnapshot) { s.ComputeUnitSeconds = policy.MaxMonthlyComputeUnitSeconds }, want: ErrQuotaExceeded},
		{name: "storage ceiling", change: func(s *UsageSnapshot) { s.StorageByteSeconds = policy.MaxMonthlyStorageByteSeconds }, want: ErrQuotaExceeded},
		{name: "history ceiling", change: func(s *UsageSnapshot) { s.HistoryByteSeconds = policy.MaxMonthlyHistoryByteSeconds }, want: ErrQuotaExceeded},
		{name: "egress ceiling", change: func(s *UsageSnapshot) { s.EgressBytes = policy.MaxMonthlyEgressBytes }, want: ErrQuotaExceeded},
		{name: "plan ceiling", change: func(s *UsageSnapshot) { s.StorageByteSeconds = 10 }, ceilings: UsageCeilings{MaxMonthlyStorageByteSeconds: 10}, want: ErrQuotaExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := pendingOperatorUsage(now)
			if tc.change != nil {
				tc.change(&snapshot)
			}
			before := snapshot
			before.Databases = append([]UsageProgress(nil), snapshot.Databases...)
			store := &operatorUsageStore{snapshot: snapshot}
			err := policy.admitWithCeilings(context.Background(), store, "account-a", now, tc.ceilings, true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("admission = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(store.snapshot, before) {
				t.Fatal("admission rewrote accounting evidence")
			}
		})
	}
}
