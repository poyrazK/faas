package state

// adr: 496
import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func memIssuedPlacementFixture(t *testing.T) regionalCapacityFixture {
	t.Helper()
	s := NewMemStore()
	account, err := s.CreateAccount(t.Context(), "issued-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return newRegionalCapacityFixture(t, s, s, account, uuid.NewString())
}

func issuedPlacement(t *testing.T, f regionalCapacityFixture, kind string) runtimequota.Placement {
	t.Helper()
	p := f.permit(kind)
	f.reserve(t, p)
	f.issue(t, p)
	placement := runtimequota.Placement{Permit: p}
	if kind == runtimequota.CapacityServing {
		seat := f.seat(t, p, false)
		f.issueSeat(t, seat)
		placement.Seat = &seat
	}
	return placement
}

func runIssuedRegionalPlacementContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	for _, kind := range []string{runtimequota.CapacityServing, runtimequota.CapacityWarm, runtimequota.CapacityPrime, runtimequota.CapacityAppTask} {
		t.Run(kind, func(t *testing.T) {
			f := fixture(t)
			p := f.permit(kind)
			placement := runtimequota.Placement{Permit: p}
			if kind == runtimequota.CapacityServing {
				placement.Seat = &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}
			}
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
				t.Fatalf("fabricated permit accepted: %v", err)
			}
			f.reserve(t, p)
			if placement.Seat != nil {
				if _, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), *placement.Seat); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
				t.Fatalf("planned permit accepted: %v", err)
			}
			f.issue(t, p)
			if placement.Seat != nil {
				if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
					t.Fatalf("unissued seat accepted: %v", err)
				}
				f.issueSeat(t, *placement.Seat)
			}
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); err != nil {
				t.Fatal("fully issued bookkeeping rejected", err)
			}
			if err := CheckRegionalRuntimeQuotaAdmission(t.Context(), f.quota.authority.Store, f.app()); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("issued bookkeeping activated execution: %v", err)
			}
			if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
				t.Fatalf("physical fence ignored: %v", err)
			}
			seats := int64(0)
			if placement.Seat != nil {
				seats = 1
			}
			f.usage(t, 1, 1, seats)
		})
	}
	t.Run("warm-seat-fence-does-not-release-physical-hold", func(t *testing.T) {
		f := fixture(t)
		placement := issuedPlacement(t, f, runtimequota.CapacityWarm)
		seat := f.seat(t, placement.Permit, false)
		f.issueSeat(t, seat)
		placement.Seat = &seat
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); err != nil {
			t.Fatal(err)
		}
		if _, err := f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
			t.Fatalf("seat fence ignored: %v", err)
		}
		placement.Seat = nil
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); err != nil {
			t.Fatal("paused physical bookkeeping lost", err)
		}
		if err := placement.ValidateBinding(runtimequota.LifecycleBinding{InstanceID: placement.Permit.InstanceID, AppID: f.app(), AccountID: placement.Permit.Grant.Binding.RegionalAccountID, DeploymentID: placement.Permit.DeploymentID, Plan: placement.Permit.Grant.Policy.Plan, Purpose: runtimequota.CapacityServing}); !errors.Is(err, runtimequota.ErrBinding) {
			t.Fatalf("paused bookkeeping supplied serving authority: %v", err)
		}
		f.usage(t, 1, 1, 1)
	})
	for _, pin := range []string{"instance", "run", "deployment", "node", "epoch", "kind", "rollout"} {
		t.Run("changed-"+pin, func(t *testing.T) {
			f := fixture(t)
			placement := issuedPlacement(t, f, runtimequota.CapacityServing)
			switch pin {
			case "instance":
				placement.Permit.InstanceID = uuid.NewString()
			case "run":
				placement.Permit.RunID = uuid.NewString()
			case "deployment":
				placement.Permit.DeploymentID = uuid.NewString()
			case "node":
				placement.Permit.NodeID = uuid.NewString()
			case "epoch":
				placement.Permit.HostEpochID = uuid.NewString()
			case "kind":
				placement.Permit.Kind = runtimequota.CapacityWarm
			case "rollout":
				placement.Seat.Rollout = true
			}
			placement.Seat.Permit = placement.Permit
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrConflict) {
				t.Fatalf("issued %s pin replaced: %v", pin, err)
			}
		})
	}
	t.Run("retired-and-superseded-grants", func(t *testing.T) {
		f := fixture(t)
		placement := issuedPlacement(t, f, runtimequota.CapacityWarm)
		_, envelope := f.quota.retirement(t, f.quota.grant)
		if _, err := f.quota.authority.AcceptRetirement(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("retired grant admitted: %v", err)
		}
		fresh := f.quota.grant
		fresh.Generation++
		fresh.AllocationID = uuid.NewString()
		if _, err := f.quota.authority.AcceptGrant(t.Context(), f.quota.grantEnvelope(t, fresh)); err != nil {
			t.Fatal(err)
		}
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrConflict) {
			t.Fatalf("superseded grant admitted: %v", err)
		}
		f.usage(t, 1, 1, 0)
	})
	t.Run("cancelled-read", func(t *testing.T) {
		f := fixture(t)
		placement := issuedPlacement(t, f, runtimequota.CapacityWarm)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(ctx, placement); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled check accepted: %v", err)
		}
	})
	t.Run("closed-receipts-remain-unavailable", func(t *testing.T) {
		f := fixture(t)
		placement := issuedPlacement(t, f, runtimequota.CapacityServing)
		fence := uuid.NewString()
		if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), placement.Permit.ID, fence); err != nil {
			t.Fatal(err)
		}
		// The signer authenticates a fixture assertion; this portable test
		// does not establish native pause/teardown or emit an operational stop.
		f.stop(t, placement.Permit, fence)
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
			t.Fatalf("closed physical or serving receipt reopened: %v", err)
		}
		f.usage(t, 0, 0, 0)
	})
	t.Run("deleted-app-retains-issued-holds", func(t *testing.T) {
		f := fixture(t)
		placement := issuedPlacement(t, f, runtimequota.CapacityServing)
		if err := f.ledger.(Store).DeleteApp(t.Context(), f.app()); err != nil {
			t.Fatal(err)
		}
		if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted app retained execution eligibility: %v", err)
		}
		f.usage(t, 1, 1, 1)
	})
}

func TestRegionalRuntimeIssuedPlacementContract(t *testing.T) {
	runIssuedRegionalPlacementContract(t, memIssuedPlacementFixture)
}
