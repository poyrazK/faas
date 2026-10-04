// adr: 582 — explain admission blockers from the same local accounting evidence.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountingDiagnosticReasonsAndBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 17, 0, 0, time.UTC)
	policy := enabledUsagePolicy()
	good := UsageProgress{Window: time.Hour, CollectedFrom: now.Truncate(time.Hour).Add(-time.Hour), CollectedUntil: now.Truncate(time.Hour), ObservedAt: now}
	for _, tc := range []struct {
		name     string
		progress UsageProgress
		state    State
		disabled bool
		reasons  []string
	}{
		{"fresh", good, StateReady, false, []string{}},
		{"unknown", UsageProgress{Unresolved: true}, StateFailed, false, []string{"identity_unknown"}},
		{"legacy", UsageProgress{Unresolved: true, Terminal: true, EndedAt: now}, StateDeleted, false, []string{"legacy_identity_unknown"}},
		{"uncollected", UsageProgress{}, StateReady, false, []string{"coverage_missing"}},
		{"changed window", UsageProgress{Window: 24 * time.Hour, ObservedAt: now}, StateReady, false, []string{"window_mismatch"}},
		{"backlog", UsageProgress{Window: time.Hour, ObservedAt: now, CollectedUntil: now.Truncate(time.Hour).Add(-time.Hour)}, StateReady, false, []string{"coverage_incomplete"}},
		{"stale", UsageProgress{Window: time.Hour, ObservedAt: now.Add(-policy.StaleAfter - time.Nanosecond), CollectedUntil: now.Truncate(time.Hour)}, StateReady, false, []string{"observation_stale"}},
		{"fresh at threshold", UsageProgress{Window: time.Hour, ObservedAt: now.Add(-policy.StaleAfter), CollectedUntil: now.Truncate(time.Hour)}, StateReady, false, []string{}},
		{"shutdown absent", UsageProgress{Window: time.Hour, ObservedAt: now, Terminal: true}, StateDeleted, false, []string{"shutdown_unconfirmed"}},
		{"terminal backlog", UsageProgress{Window: time.Hour, ObservedAt: now, Terminal: true, EndedAt: now, CollectedUntil: now.Truncate(time.Hour)}, StateDeleted, false, []string{"coverage_incomplete", "final_correction_pending"}},
		{"terminal correction", UsageProgress{Window: time.Hour, ObservedAt: now, Terminal: true, EndedAt: now.Truncate(time.Hour), CollectedUntil: now.Truncate(time.Hour)}, StateDeleted, false, []string{"final_correction_pending"}},
		{"settled terminal", UsageProgress{Window: time.Hour, ObservedAt: now.Add(-30 * 24 * time.Hour), Terminal: true, EndedAt: now.Add(-30 * 24 * time.Hour).Truncate(time.Hour), CollectedUntil: now.Add(-30 * 24 * time.Hour).Truncate(time.Hour), CorrectionObservedAt: now.Add(-30 * 24 * time.Hour).Truncate(time.Hour).Add(3 * time.Hour)}, StateDeleted, false, []string{}},
		{"disabled", UsageProgress{Unresolved: true}, StateDeleted, true, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy
			p.Enabled = !tc.disabled
			d := accountingDiagnostic(AccountingCoverage{DatabaseID: "d", State: tc.state, CreatedAt: good.CollectedFrom, Progress: tc.progress}, p, now)
			if !reflect.DeepEqual(d.Reasons, tc.reasons) || d.Blocking != (len(tc.reasons) > 0) {
				t.Fatalf("diagnostic = %+v", d)
			}
			if (UsageSnapshot{Databases: []UsageProgress{tc.progress}}).Stale(p, now) != d.Blocking {
				t.Fatal("diagnosis disagrees with admission")
			}
			if tc.name == "legacy" && (!d.RequiredUntil.IsZero() || !d.CorrectionRequiredAt.IsZero()) {
				t.Fatal("logical tombstone invented shutdown evidence")
			}
		})
	}
}

func TestAccountingDiagnosticsStorePaginationAndSharedRoot(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 1, 0, 17, 0, 0, time.UTC)
			store, registry, provider, root := retirementFixture(t, kind, now.Add(-3*time.Hour))
			for from := root.CreatedAt.Truncate(time.Hour); from.Before(now.Truncate(time.Hour)); from = from.Add(time.Hour) {
				if err := store.RecordUsage(ctx, []UsageRecord{{AccountID: root.AccountID, DatabaseID: root.ID, BackendID: root.BackendID, BackendFingerprint: root.BackendFingerprint, WindowFrom: from, WindowTo: from.Add(time.Hour), ObservedAt: now, Meter: MeterComputeUnitSeconds, Quantity: 60}}); err != nil {
					t.Fatal(err)
				}
			}
			input := postgresTestDatabase(root.AccountID, "child", now.Add(-time.Hour))
			input.BackendID, input.BackendFingerprint = root.BackendID, root.BackendFingerprint
			input.RestoreSourceDatabaseID = root.ID
			input.RestoreSourceResourceID = root.ProviderResourceID
			input.RestorePointInTime = input.CreatedAt
			child := retirementReadyDatabase(t, store, input)
			if err := store.RecordSharedUsage(ctx, child.AccountID, child.ID, root.ID, time.Hour); err != nil {
				t.Fatal(err)
			}
			child = retirementDelete(t, store, child, now.Add(-30*time.Minute), true)
			unknown := diagnosticUnknownDatabase(t, store, root, "unknown", now.Add(-2*time.Hour))
			legacy := diagnosticUnknownDatabase(t, store, root, "legacy", now.Add(-2*time.Hour))
			legacy = retirementDelete(t, store, legacy, now.Add(-time.Hour), true)
			unattempted := postgresTestDatabase(root.AccountID, "unattempted", now)
			unattempted.BackendID, unattempted.BackendFingerprint = root.BackendID, root.BackendFingerprint
			if _, _, err := store.Reserve(ctx, unattempted, 100); err != nil {
				t.Fatal(err)
			}
			otherAccount := uuid.NewString()
			if pg, ok := store.(*PostgresStore); ok {
				a, err := state.NewPgStore(pg.pool).CreateAccount(ctx, uuid.NewString()+"@diagnostics.test", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				otherAccount = a.ID
			}
			foreign := postgresTestDatabase(otherAccount, "foreign", now)
			foreign.BackendID, foreign.BackendFingerprint = root.BackendID, root.BackendFingerprint
			retirementReadyDatabase(t, store, foreign)
			service, err := NewService(registry, store, ServiceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.UsageSnapshot(ctx, root.AccountID, now)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]AccountingDiagnostic{}
			cursor := ""
			for turns := 0; turns < 3; turns++ {
				page, err := service.AccountingDiagnostics(ctx, root.AccountID, strings.ToUpper(cursor), 2, now)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) > 2 {
					t.Fatal("unbounded page")
				}
				for _, d := range page.Items {
					if _, ok := seen[d.DatabaseID]; ok {
						t.Fatal("duplicate page item")
					}
					seen[d.DatabaseID] = d
				}
				if page.NextCursor == "" {
					break
				}
				if page.NextCursor <= cursor {
					t.Fatal("cursor failed to advance")
				}
				cursor = page.NextCursor
			}
			if len(seen) != 4 {
				t.Fatalf("unexpected catalog rows: %+v", seen)
			}
			if seen[root.ID].Blocking || seen[child.ID].Blocking || seen[child.ID].AccountingDatabaseID != root.ID || !seen[child.ID].RequiredFrom.Equal(root.CreatedAt.Truncate(time.Hour)) || !seen[child.ID].RequiredUntil.Equal(now.Truncate(time.Hour)) {
				t.Fatalf("shared root lost: %+v", seen)
			}
			if !reflect.DeepEqual(seen[unknown.ID].Reasons, []string{"identity_unknown"}) || !reflect.DeepEqual(seen[legacy.ID].Reasons, []string{"legacy_identity_unknown"}) || !seen[legacy.ID].RequiredUntil.IsZero() {
				t.Fatalf("unknown obligations lost: %+v", seen)
			}
			after, err := store.UsageSnapshot(ctx, root.AccountID, now)
			if err != nil {
				t.Fatal(err)
			}
			for _, snapshot := range []*UsageSnapshot{&before, &after} {
				sort.Slice(snapshot.Databases, func(i, j int) bool { return fmt.Sprint(snapshot.Databases[i]) < fmt.Sprint(snapshot.Databases[j]) })
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("diagnostics changed ledger")
			}
			if len(provider.usageCalls) != 0 {
				t.Fatal("diagnostics reached provider")
			}
			root = retirementDelete(t, store, root, now, true)
			end := usageEnd(*root.DeletedAt, time.Hour)
			observed := end.Add(3 * time.Hour)
			for from := root.CreatedAt.Truncate(time.Hour); from.Before(end); from = from.Add(time.Hour) {
				if err := store.RecordUsage(ctx, []UsageRecord{{AccountID: root.AccountID, DatabaseID: root.ID, BackendID: root.BackendID, BackendFingerprint: root.BackendFingerprint, WindowFrom: from, WindowTo: from.Add(time.Hour), ObservedAt: observed, Meter: MeterComputeUnitSeconds, Quantity: 60}}); err != nil {
					t.Fatal(err)
				}
			}
			terminal, err := service.AccountingDiagnostics(ctx, root.AccountID, "", 100, observed.AddDate(0, 1, 0))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range terminal.Items {
				if d.DatabaseID == root.ID || d.DatabaseID == child.ID {
					if d.Blocking || !d.RequiredUntil.Equal(end) || !d.CorrectionRequiredAt.Equal(observed) || d.AccountingDatabaseID != root.ID {
						t.Fatalf("terminal root evidence changed at rollover: %+v", d)
					}
				}
			}
			for _, bad := range []struct {
				cursor string
				limit  int
			}{{"bad", 2}, {"", 0}, {"", 101}} {
				if _, err := service.AccountingDiagnostics(ctx, root.AccountID, bad.cursor, bad.limit, now); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid pagination: %v", err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := service.AccountingDiagnostics(canceled, root.AccountID, "", 2, now); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func diagnosticUnknownDatabase(t *testing.T, store retirementUsageStore, root Database, name string, at time.Time) Database {
	t.Helper()
	ctx := context.Background()
	input := postgresTestDatabase(root.AccountID, name, at)
	input.BackendID, input.BackendFingerprint = root.BackendID, root.BackendFingerprint
	d, _, err := store.Reserve(ctx, input, 100)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, d.AccountID, d.ID, uuid.NewString(), StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginAccounting(ctx, d.ID, claim.LeaseToken, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, d.ID, claim.LeaseToken, StateFailed, "unavailable", at.Add(2*time.Second), at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	d, err = store.Get(ctx, d.AccountID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
