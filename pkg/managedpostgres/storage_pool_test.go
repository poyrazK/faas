// adr: 20261025094915 — the storage pool lowers a database's provider storage
// limit, and that limit is enforcement state, not drift (decision 9).
package managedpostgres

import (
	"context"
	"testing"
)

func TestSpecForObservation(t *testing.T) {
	expected := testSpec()
	for _, tc := range []struct {
		name    string
		storage int64
		want    int64
	}{
		{name: "the spec's limit", storage: expected.StorageLimitBytes, want: expected.StorageLimitBytes},
		{name: "lowered by the pool", storage: 1 << 20, want: expected.StorageLimitBytes},
		{name: "one byte", storage: 1, want: expected.StorageLimitBytes},
		{name: "above the spec is drift", storage: expected.StorageLimitBytes + 1, want: expected.StorageLimitBytes + 1},
		{name: "zero is no limit, not a pool", storage: 0, want: 0},
		{name: "unset is drift", storage: -1, want: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := expected
			observed.StorageLimitBytes = tc.storage
			if got := SpecForObservation(expected, observed); got.StorageLimitBytes != tc.want {
				t.Fatalf("storage limit %d, want %d", got.StorageLimitBytes, tc.want)
			}
		})
	}
	observed := expected
	observed.StorageLimitBytes, observed.Class = 1<<20, ClassProduction
	if got := SpecForObservation(expected, observed); got == expected || got.Class != ClassProduction {
		t.Fatal("a pool-lowered limit hid a class drift")
	}
}

func TestReconcileAcceptsAPoolLoweredStorageLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		storage int64
		want    State
	}{
		{name: "lowered by the pool", storage: 1 << 20, want: StateReady},
		{name: "raised above the spec", storage: testSpec().StorageLimitBytes + 1, want: StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusPending,
				inspectStatus: ProviderStatusReady, inspectStorageLimit: tc.storage}
			service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
			created, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "events", Spec: testSpec()})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			reconciled, _ := service.Reconcile(context.Background(), "account-a", created.ID)
			if reconciled.ID == "" {
				reconciled, err = service.Get(context.Background(), "account-a", created.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if reconciled.State != tc.want || reconciled.Spec != testSpec() {
				t.Fatalf("database %s with spec %+v; want %s and the stored spec", reconciled.State, reconciled.Spec, tc.want)
			}
		})
	}
}
