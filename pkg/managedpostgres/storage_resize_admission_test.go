package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type storageGrowthFleetStore struct{ *MemoryStore }

func (*storageGrowthFleetStore) FleetTotals(context.Context, time.Time) (FleetTotals, error) {
	return FleetTotals{Databases: 1, CostMillicents: 2}, nil
}

func TestStorageGrowthHonorsSpendAdmission(t *testing.T) {
	for _, guard := range []string{"account-usage", "fleet-spend"} {
		t.Run(guard, func(t *testing.T) {
			svc, store, _, d, _ := resizeFixtureWith(t, func(c *Capabilities) { c.ClassResize = true; c.StorageResize = true })
			want := ErrUsageStale
			checks := 0
			if guard == "account-usage" {
				svc.admit = func(context.Context, string) error { checks++; return want }
			} else {
				svc.store = &storageGrowthFleetStore{store}
				svc.registry.fleet = FleetPolicy{MaxMonthlyCostMillicents: 1}
				want = ErrFleetPaused
			}
			// Establish that this same source really is behind the spend guard.
			if _, err := svc.Resize(t.Context(), resizeRequest(d)); !errors.Is(err, want) {
				t.Fatalf("control class increase was not blocked: %v", err)
			}
			checks = 0
			op, err := svc.Resize(t.Context(), ResizeDatabaseRequest{AccountID: d.AccountID, DatabaseID: d.ID, RequestID: uuid.NewString(), TargetStorageLimitBytes: 2 * d.Spec.StorageLimitBytes})
			t.Logf("storage=%d -> %d bytes, state=%s, admission checks=%d, error=%v", d.Spec.StorageLimitBytes, 2*d.Spec.StorageLimitBytes, op.State, checks, err)
			if !errors.Is(err, want) {
				t.Errorf("storage growth bypassed the %s guard: operation=%s, error=%v", guard, op.State, err)
			}
		})
	}
}
