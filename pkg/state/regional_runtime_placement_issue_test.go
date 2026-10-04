package state

// adr: 497
import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func initialRegionalPlacement(f regionalCapacityFixture, kind string) runtimequota.Placement {
	p := runtimequota.Placement{Permit: f.permit(kind)}
	if kind == runtimequota.CapacityServing {
		p.Seat = &runtimequota.ServingSeat{Permit: p.Permit, ID: uuid.NewString()}
	}
	return p
}

func runRegionalPlacementIssueContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	for _, kind := range []string{runtimequota.CapacityServing, runtimequota.CapacityWarm, runtimequota.CapacityPrime, runtimequota.CapacityAppTask} {
		t.Run(kind+"-concurrent-retry", func(t *testing.T) {
			f := fixture(t)
			p := initialRegionalPlacement(f, kind)
			var wg sync.WaitGroup
			done := make(chan error, 4)
			for range 4 {
				wg.Go(func() { done <- f.ledger.IssueRegionalRuntimePlacement(t.Context(), p) })
			}
			wg.Wait()
			close(done)
			for err := range done {
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			before, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.Permit.ID)
			if err != nil || before.IssuedAt == nil {
				t.Fatal("physical issuance missing", err)
			}
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			after, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.Permit.ID)
			if err != nil || !before.CreatedAt.Equal(after.CreatedAt) || !before.IssuedAt.Equal(*after.IssuedAt) {
				t.Fatal("retry changed durable physical timestamps", err)
			}
			seats := int64(0)
			if p.Seat != nil {
				seats = 1
			}
			f.usage(t, 1, 1, seats)
			if err := CheckRegionalRuntimeQuotaAdmission(t.Context(), f.quota.authority.Store, f.app()); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
				t.Fatal("issuance activated execution", err)
			}
		})
	}
	for _, planned := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "planned"}[planned]+"-seat-failure-rolls-back-issuance", func(t *testing.T) {
			f := fixture(t)
			for range f.quota.grant.Concurrency {
				if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), initialRegionalPlacement(f, runtimequota.CapacityServing)); err != nil {
					t.Fatal(err)
				}
			}
			p := initialRegionalPlacement(f, runtimequota.CapacityServing)
			if planned {
				f.reserve(t, p.Permit)
			}
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), p); !errors.Is(err, ErrRegionalRuntimeCapacity) {
				t.Fatal("full concurrency admitted a seat", err)
			}
			r, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.Permit.ID)
			if planned && (err != nil || r.IssuedAt != nil) {
				t.Fatal("failure issued an existing planned physical hold", err)
			}
			if !planned && !errors.Is(err, ErrNotFound) {
				t.Fatal("failure left a new physical hold", r, err)
			}
			if _, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), p.Seat.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed seat persisted", err)
			}
		})
	}
	t.Run("concurrent-capacity-conservation", func(t *testing.T) {
		f := fixture(t)
		var wg sync.WaitGroup
		done := make(chan error, 8)
		for range 8 {
			p := initialRegionalPlacement(f, runtimequota.CapacityServing)
			wg.Go(func() { done <- f.ledger.IssueRegionalRuntimePlacement(t.Context(), p) })
		}
		wg.Wait()
		close(done)
		accepted := 0
		for err := range done {
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrRegionalRuntimeCapacity) {
				t.Fatal(err)
			}
		}
		if accepted != f.quota.grant.Concurrency {
			t.Fatal("unexpected accepted seats", accepted)
		}
		f.usage(t, int64(accepted), int64(accepted), int64(accepted))
	})
	for _, pin := range []string{"permit", "deployment", "run", "kind", "seat", "rollout"} {
		t.Run("conflicting-retry-"+pin, func(t *testing.T) {
			f := fixture(t)
			p := initialRegionalPlacement(f, runtimequota.CapacityServing)
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			changed := p.Clone()
			switch pin {
			case "permit":
				changed.Permit.ID = uuid.NewString()
			case "deployment":
				changed.Permit.DeploymentID = uuid.NewString()
			case "run":
				changed.Permit.RunID = uuid.NewString()
			case "kind":
				changed.Permit.Kind = runtimequota.CapacityWarm
			case "seat":
				changed.Seat.ID = uuid.NewString()
			case "rollout":
				changed.Seat.Rollout = true
			}
			changed.Seat.Permit = changed.Permit
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), changed); !errors.Is(err, ErrConflict) {
				t.Fatal("changed immutable attempt accepted", err)
			}
			f.usage(t, 1, 1, 1)
		})
	}
	for _, fence := range []string{"physical", "seat", "retirement"} {
		t.Run("retry-after-"+fence, func(t *testing.T) {
			f := fixture(t)
			p := initialRegionalPlacement(f, runtimequota.CapacityServing)
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fence {
			case "physical":
				_, err = f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.Permit.ID, uuid.NewString())
			case "seat":
				_, err = f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), p.Seat.ID, uuid.NewString())
			case "retirement":
				_, envelope := f.quota.retirement(t, f.quota.grant)
				_, err = f.quota.authority.AcceptRetirement(t.Context(), envelope)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), p); err == nil {
				t.Fatal("retry reopened a fence")
			}
			f.usage(t, 1, 1, 1)
		})
	}
	for _, issuedSteady := range []bool{false, true} {
		t.Run(map[bool]string{false: "planned", true: "issued"}[issuedSteady]+"-rollout-parent", func(t *testing.T) {
			f := fixture(t)
			steady := initialRegionalPlacement(f, runtimequota.CapacityServing)
			if issuedSteady {
				if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), steady); err != nil {
					t.Fatal(err)
				}
			} else {
				f.reserve(t, steady.Permit)
				if _, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), *steady.Seat); err != nil {
					t.Fatal(err)
				}
			}
			rollout := initialRegionalPlacement(f, runtimequota.CapacityServing)
			rollout.Seat.Rollout = true
			err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), rollout)
			if issuedSteady {
				if err != nil {
					t.Fatal("issued steady parent did not permit rollout", err)
				}
				f.usage(t, 2, 2, 2)
			} else {
				if !errors.Is(err, ErrRegionalRuntimeCapacity) {
					t.Fatal("planned parent authorized rollout", err)
				}
				f.usage(t, 1, 0, 1)
			}
		})
	}
	t.Run("cancelled-creates-no-hold", func(t *testing.T) {
		f := fixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := f.ledger.IssueRegionalRuntimePlacement(ctx, initialRegionalPlacement(f, runtimequota.CapacityServing)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		f.usage(t, 0, 0, 0)
	})
}

func TestRegionalRuntimePlacementIssueContract(t *testing.T) {
	runRegionalPlacementIssueContract(t, memIssuedPlacementFixture)
}
