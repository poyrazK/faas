package state

// adr: 626
import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func resumeIntent(t *testing.T, p runtimequota.CapacityPermit) runtimequota.ServingResumeIntent {
	t.Helper()
	i, err := runtimequota.NewServingResumeIntent(p, uuid.NewString(), false)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func runRegionalServingResumeContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	t.Run("competing-proposals-and-lost-response", func(t *testing.T) {
		f := fixture(t)
		s := f.ledger.(RegionalRuntimeServingResumeStore)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		f.issue(t, p)
		before, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		results := make(chan RegionalRuntimeServingResumeReceipt, 8)
		for range 8 {
			i := resumeIntent(t, p)
			wg.Go(func() {
				r, issueErr := s.IssueRegionalRuntimeServingResume(t.Context(), i)
				if issueErr != nil {
					t.Error(issueErr)
				}
				results <- r
			})
		}
		wg.Wait()
		close(results)
		var retained RegionalRuntimeServingResumeReceipt
		for r := range results {
			if retained.Intent.ID == "" {
				retained = r
			}
			if r.Intent != retained.Intent || !r.CreatedAt.Equal(retained.CreatedAt) {
				t.Fatal("concurrent proposals minted multiple authorizations", retained, r)
			}
		}
		for _, i := range []runtimequota.ServingResumeIntent{retained.Intent, resumeIntent(t, p)} {
			r, issueErr := s.IssueRegionalRuntimeServingResume(t.Context(), i)
			if issueErr != nil || r.Intent != retained.Intent || !r.CreatedAt.Equal(retained.CreatedAt) {
				t.Fatal("uncertain retry did not recover original intent", r, issueErr)
			}
		}
		if err = f.ledger.CheckRegionalRuntimeIssuedPlacement(t.Context(), retained.Intent.Placement()); err != nil {
			t.Fatal(err)
		}
		after, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		if err != nil || before.Permit != after.Permit || !before.CreatedAt.Equal(after.CreatedAt) || !before.IssuedAt.Equal(*after.IssuedAt) {
			t.Fatal("resume changed physical receipt", after, err)
		}
		f.usage(t, 1, 1, 1)
		if err = CheckRegionalRuntimeQuotaAdmission(t.Context(), f.quota.authority.Store, f.app()); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatal("resume activated admission", err)
		}
	})
	t.Run("fence-release-and-fresh-intent", func(t *testing.T) {
		f := fixture(t)
		s := f.ledger.(RegionalRuntimeServingResumeStore)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		f.issue(t, p)
		old, err := s.IssueRegionalRuntimeServingResume(t.Context(), resumeIntent(t, p))
		if err != nil {
			t.Fatal(err)
		}
		fence := uuid.NewString()
		if _, err = f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), old.Intent.Seat.ID, fence); err != nil {
			t.Fatal(err)
		}
		fresh := resumeIntent(t, p)
		for _, i := range []runtimequota.ServingResumeIntent{old.Intent, fresh} {
			if _, err = s.IssueRegionalRuntimeServingResume(t.Context(), i); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
				t.Fatal("fence was reopened or replaced", err)
			}
		}
		f.usage(t, 1, 1, 1)
		// Authenticated assertions test bookkeeping only; no native proof is inferred.
		f.stopSeat(t, old.Intent.Seat, fence)
		f.usage(t, 1, 1, 0)
		next, err := s.IssueRegionalRuntimeServingResume(t.Context(), fresh)
		if err != nil || next.Intent != fresh || next.Intent.Seat.ID == old.Intent.Seat.ID {
			t.Fatal("released seat did not permit a fresh intent", next, err)
		}
		if _, err = s.IssueRegionalRuntimeServingResume(t.Context(), old.Intent); !errors.Is(err, ErrRegionalRuntimePlacementUnavailable) {
			t.Fatal("old ID replay substituted new authorization", err)
		}
		recovered, err := s.RegionalRuntimeServingResume(t.Context(), f.app(), old.Intent.ID)
		if err != nil || recovered.Intent != old.Intent || !recovered.CreatedAt.Equal(old.CreatedAt) {
			t.Fatal("closed intent not retained", recovered, err)
		}
		f.usage(t, 1, 1, 1)
	})
	for _, pin := range []string{"deployment", "node", "epoch", "run", "purpose", "rollout"} {
		t.Run("immutable-"+pin, func(t *testing.T) {
			f := fixture(t)
			s := f.ledger.(RegionalRuntimeServingResumeStore)
			p := f.permit(runtimequota.CapacityWarm)
			f.reserve(t, p)
			f.issue(t, p)
			old, err := s.IssueRegionalRuntimeServingResume(t.Context(), resumeIntent(t, p))
			if err != nil {
				t.Fatal(err)
			}
			bad := old.Intent
			switch pin {
			case "deployment":
				bad.Seat.Permit.DeploymentID = uuid.NewString()
			case "node":
				bad.Seat.Permit.NodeID = uuid.NewString()
			case "epoch":
				bad.Seat.Permit.HostEpochID = uuid.NewString()
			case "run":
				bad.Seat.Permit.RunID = uuid.NewString()
			case "purpose":
				bad.Seat.Permit.Kind = runtimequota.CapacityServing
			case "rollout":
				bad.Seat.Rollout = true
			}
			if _, err = s.IssueRegionalRuntimeServingResume(t.Context(), bad); !errors.Is(err, ErrConflict) {
				t.Fatal("immutable intent was rebound", err)
			}
			f.usage(t, 1, 1, 1)
		})
	}
	for _, fence := range []string{"unissued", "physical", "retirement", "initial-held-seat"} {
		t.Run("refuse-"+fence, func(t *testing.T) {
			f := fixture(t)
			s := f.ledger.(RegionalRuntimeServingResumeStore)
			p := f.permit(runtimequota.CapacityWarm)
			f.reserve(t, p)
			if fence != "unissued" {
				f.issue(t, p)
			}
			switch fence {
			case "physical":
				if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "retirement":
				_, e := f.quota.retirement(t, f.quota.grant)
				if _, err := f.quota.authority.AcceptRetirement(t.Context(), e); err != nil {
					t.Fatal(err)
				}
			case "initial-held-seat":
				seat := f.seat(t, p, false)
				f.issueSeat(t, seat)
			}
			i := resumeIntent(t, p)
			if _, err := s.IssueRegionalRuntimeServingResume(t.Context(), i); err == nil {
				t.Fatal("ineligible resume issued")
			}
			if _, err := s.RegionalRuntimeServingResume(t.Context(), f.app(), i.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("failure retained a new intent", err)
			}
			if _, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), i.Seat.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("failure retained a new seat", err)
			}
		})
	}
	t.Run("capacity-failure-is-atomic", func(t *testing.T) {
		f := fixture(t)
		s := f.ledger.(RegionalRuntimeServingResumeStore)
		for range f.quota.grant.Concurrency {
			if err := f.ledger.IssueRegionalRuntimePlacement(t.Context(), initialRegionalPlacement(f, runtimequota.CapacityServing)); err != nil {
				t.Fatal(err)
			}
		}
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		f.issue(t, p)
		i := resumeIntent(t, p)
		if _, err := s.IssueRegionalRuntimeServingResume(t.Context(), i); !errors.Is(err, ErrRegionalRuntimeCapacity) {
			t.Fatal(err)
		}
		if _, err := s.RegionalRuntimeServingResume(t.Context(), f.app(), i.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), i.Seat.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		f.usage(t, int64(f.quota.grant.Concurrency+1), int64(f.quota.grant.Concurrency+1), int64(f.quota.grant.Concurrency))
	})
	t.Run("cancelled-creates-no-intent", func(t *testing.T) {
		f := fixture(t)
		s := f.ledger.(RegionalRuntimeServingResumeStore)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		f.issue(t, p)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := s.IssueRegionalRuntimeServingResume(ctx, resumeIntent(t, p)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		f.usage(t, 1, 1, 0)
	})
}

func TestRegionalServingResumeContract(t *testing.T) {
	runRegionalServingResumeContract(t, memIssuedPlacementFixture)
}
