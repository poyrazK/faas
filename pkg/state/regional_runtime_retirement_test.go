package state

// adr: 631
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func runRegionalRuntimeRetirementContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	t.Run("unissued-local-cancellation", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		fence := uuid.NewString()
		r, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, fence)
		if err != nil || r.ReleasedAt == nil || r.IssuedAt != nil || r.Stop != nil || r.StopEnvelope != nil || r.FenceID != fence {
			t.Fatalf("cancellation: %+v %v", r, err)
		}
		f.usage(t, 0, 0, 0)
	})
	t.Run("parallel-first-fence-and-original-envelope", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		f.issue(t, p)
		seat := f.seat(t, p, false)
		f.issueSeat(t, seat)
		seatFence := uuid.NewString()
		if _, err := f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID, seatFence); err != nil {
			t.Fatal(err)
		}
		const n = 8
		results := make(chan RegionalRuntimeCapacityReceipt, n)
		failures := make(chan error, n)
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, uuid.NewString())
				results <- r
				failures <- err
			}()
		}
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		fence := ""
		for r := range results {
			if fence == "" {
				fence = r.FenceID
			}
			if r.FenceID != fence || r.ReleasedAt != nil {
				t.Fatal("first fence/hold changed")
			}
		}
		f.usage(t, 1, 1, 1)
		stop := runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}
		closed, err := f.authority.CompleteHostRetirement(t.Context(), stop, f.cell)
		if err != nil || closed.StopEnvelope == nil || closed.ReleasedAt == nil || closed.Stop == nil || *closed.Stop != stop {
			t.Fatalf("close: %+v %v", closed, err)
		}
		if got, err := f.authority.Cells.VerifyCapacityStop(*closed.StopEnvelope); err != nil || got != stop {
			t.Fatal("wrong signed assertion", err)
		}
		f.usage(t, 0, 0, 0)
		retained, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID)
		if err != nil || !retained.ClosedByPhysicalStop || retained.FenceID != seatFence {
			t.Fatal("prior seat fence lost", err)
		}
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rotated, err := runtimequota.NewSigner(f.cell.IssuerID(), private)
		if err != nil {
			t.Fatal(err)
		}
		f.authority.Cells, err = runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: rotated.IssuerID(), Public: pub}})
		if err != nil {
			t.Fatal(err)
		}
		replay, err := f.authority.CompleteHostRetirement(t.Context(), stop, rotated)
		if err != nil || !reflect.DeepEqual(closed, replay) {
			t.Fatal("rotation replaced first evidence", err)
		}
	})
	t.Run("historical-grant-and-full-identity", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		f.issue(t, p)
		retirement, e := f.quota.retirement(t, f.quota.grant)
		if _, err := f.quota.authority.AcceptRetirement(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		r, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, uuid.NewString())
		if err != nil || r.FenceID != retirement.RetirementID {
			t.Fatal("retirement fence replaced", err)
		}
		bad := p
		bad.RunID = uuid.NewString()
		if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), bad, uuid.NewString()); !errors.Is(err, ErrConflict) {
			t.Fatal("full permit rebound", err)
		}
		stop := runtimequota.CapacityStop{Permit: p, FenceID: r.FenceID, AdmissionFrozen: true}
		if _, err := f.authority.CompleteHostRetirement(t.Context(), stop, f.cell); err != nil {
			t.Fatal("historical grant refused", err)
		}
		f.usage(t, 0, 0, 0)
	})
	t.Run("uncertain-or-untrusted-stop-retains-hold", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityPrime)
		f.reserve(t, p)
		f.issue(t, p)
		fence := uuid.NewString()
		stop := runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}
		if _, err := f.authority.CompleteHostRetirement(t.Context(), stop, f.cell); !errors.Is(err, ErrConflict) {
			t.Fatal("unsigned local fence inferred", err)
		}
		if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, fence); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*runtimequota.CapacityStop){func(s *runtimequota.CapacityStop) { s.FenceID = uuid.NewString() }, func(s *runtimequota.CapacityStop) { s.Permit.HostEpochID = uuid.NewString() }, func(s *runtimequota.CapacityStop) { s.AdmissionFrozen = false }, func(s *runtimequota.CapacityStop) { s.CountedRuntimes = 1 }} {
			bad := stop
			mutate(&bad)
			if _, err := f.authority.CompleteHostRetirement(t.Context(), bad, f.cell); err == nil {
				t.Fatal("uncertain stop signed")
			}
		}
		untrusted := f.authority
		untrusted.Cells = runtimequota.Trust{}
		if _, err := untrusted.CompleteHostRetirement(t.Context(), stop, f.cell); !errors.Is(err, runtimequota.ErrTrust) {
			t.Fatal("unpinned signer accepted", err)
		}
		f.usage(t, 1, 1, 0)
		r, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		if err != nil || r.StopEnvelope != nil || r.Stop != nil || r.ReleasedAt != nil {
			t.Fatal("failed closure wrote evidence", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.authority.CompleteHostRetirement(ctx, stop, f.cell); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled stop accepted", err)
		}
	})
}

func TestMemRegionalRuntimeRetirementContract(t *testing.T) {
	runRegionalRuntimeRetirementContract(t, func(t *testing.T) regionalCapacityFixture {
		s := NewMemStore()
		account, err := s.CreateAccount(t.Context(), "retirement@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return newRegionalCapacityFixture(t, s, s, account, uuid.NewString())
	})
}
