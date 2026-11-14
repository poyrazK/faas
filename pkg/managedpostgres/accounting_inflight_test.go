// adr: 20261104203253 — a create still in flight does not pause its account.
package managedpostgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAccountingInFlightCreationBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 17, 0, 0, time.UTC)
	window := now.Truncate(time.Hour)
	held := now.Add(time.Minute)
	for _, tc := range []struct {
		name     string
		progress UsageProgress
		state    State
		reasons  []string
	}{
		{"in flight", UsageProgress{Unresolved: true, CreatedAt: now.Add(-time.Minute), CreationLeaseUntil: held}, StateProvisioning, []string{}},
		{"reserved at window start", UsageProgress{Unresolved: true, CreatedAt: window, CreationLeaseUntil: held}, StateProvisioning, []string{}},
		{"lease lapsed", UsageProgress{Unresolved: true, CreatedAt: now.Add(-time.Minute), CreationLeaseUntil: now}, StateProvisioning, []string{"identity_unknown"}},
		{"no lease", UsageProgress{Unresolved: true, CreatedAt: now.Add(-time.Minute)}, StateProvisioning, []string{"identity_unknown"}},
		{"reserved last window", UsageProgress{Unresolved: true, CreatedAt: window.Add(-time.Nanosecond), CreationLeaseUntil: held}, StateProvisioning, []string{"identity_unknown"}},
		{"no reservation time", UsageProgress{Unresolved: true, CreationLeaseUntil: held}, StateProvisioning, []string{"identity_unknown"}},
		{"terminal", UsageProgress{Unresolved: true, Terminal: true, EndedAt: now, CreatedAt: now.Add(-time.Minute), CreationLeaseUntil: held}, StateDeleted, []string{"legacy_identity_unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := enabledUsagePolicy()
			d := accountingDiagnostic(AccountingCoverage{DatabaseID: "d", State: tc.state, CreatedAt: tc.progress.CreatedAt, Progress: tc.progress}, policy, now)
			if !reflect.DeepEqual(d.Reasons, tc.reasons) || d.Blocking != (len(tc.reasons) > 0) {
				t.Fatalf("diagnostic = %+v", d)
			}
			if (UsageSnapshot{Databases: []UsageProgress{tc.progress}}).Stale(policy, now) != d.Blocking {
				t.Fatal("diagnosis disagrees with admission")
			}
		})
	}
}

// A second create on the account is admitted while the first one's provider
// call is still running, and refused again once the call's lease lapses or the
// window it was reserved in closes.
func TestUncertainProvisionAdmitsWhileTheCreateIsInFlight(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			policy := registry.UsagePolicy()
			var inFlight, lapsed, closed error
			var lease time.Time
			provider.provisionHook = func() {
				provider.provisionHook = nil
				ctx := context.Background()
				database, err := store.FindByName(ctx, provider.account, "uncertain")
				if err != nil {
					t.Fatal(err)
				}
				if !database.AccountingRequired || database.ProviderResourceID != "" || database.State != StateProvisioning {
					t.Fatalf("not an in-flight obligation: %+v", database)
				}
				lease = database.LeaseUntil
				windowEnd := now.Truncate(policy.Window).Add(policy.Window)
				if !lease.After(now) || !lease.Before(windowEnd) {
					t.Fatalf("lease %v outside [%v, %v)", lease, now, windowEnd)
				}
				inFlight = policy.Admit(ctx, store, provider.account, now)
				lapsed = policy.Admit(ctx, store, provider.account, lease)
				closed = policy.Admit(ctx, store, provider.account, windowEnd)
			}
			database := uncertainAccountingCreate(t, store, provider, service)
			if lease.IsZero() {
				t.Fatal("provision hook did not run")
			}
			if inFlight != nil {
				t.Fatalf("in-flight create paused the account: %v", inFlight)
			}
			if !errors.Is(lapsed, ErrUsageStale) {
				t.Fatalf("lapsed lease escaped admission: %v", lapsed)
			}
			if !errors.Is(closed, ErrUsageStale) {
				t.Fatalf("closed window escaped admission: %v", closed)
			}
			// The reply was lost: the worker released its lease, so the
			// obligation blocks until collection recovers the identity.
			if database.LeaseUntil.After(now) {
				t.Fatalf("lost creation kept its lease: %+v", database)
			}
			if err := policy.Admit(context.Background(), store, provider.account, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("lost creation escaped admission: %v", err)
			}
		})
	}
}
