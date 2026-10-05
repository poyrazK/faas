package state

// adr: 618
import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type regionalCapacityFixture struct {
	quota     regionalQuotaFixture
	ledger    RegionalRuntimeCapacityLedger
	authority RegionalRuntimeCapacityAuthority
	cell      runtimequota.Signer
}

func newRegionalCapacityFixture(t *testing.T, s Store, ledger RegionalRuntimeCapacityLedger, account Account, cellID string) regionalCapacityFixture {
	t.Helper()
	quota := s.(RegionalRuntimeQuotaStore)
	f := regionalCapacityFixture{quota: newRegionalQuotaFixture(t, s, quota, account, cellID), ledger: ledger}
	if _, err := f.quota.authority.AcceptGrant(t.Context(), f.quota.grantEnvelope(t, f.quota.grant)); err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.cell, err = runtimequota.NewSigner(cellID, private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: cellID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	f.authority = RegionalRuntimeCapacityAuthority{Store: ledger, Cells: trust}
	return f
}
func (f regionalCapacityFixture) app() string { return f.quota.grant.Binding.RegionalAppID }
func (f regionalCapacityFixture) permit(kind string) runtimequota.CapacityPermit {
	return runtimequota.CapacityPermit{Grant: f.quota.grant, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: kind}
}
func (f regionalCapacityFixture) reserve(t *testing.T, p runtimequota.CapacityPermit) {
	t.Helper()
	if _, err := f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}
func (f regionalCapacityFixture) issue(t *testing.T, p runtimequota.CapacityPermit) {
	t.Helper()
	if _, err := f.ledger.IssueRegionalRuntimeCapacity(t.Context(), f.app(), p.ID); err != nil {
		t.Fatal(err)
	}
}
func (f regionalCapacityFixture) seat(t *testing.T, p runtimequota.CapacityPermit, rollout bool) runtimequota.ServingSeat {
	t.Helper()
	seat := runtimequota.ServingSeat{Permit: p, ID: uuid.NewString(), Rollout: rollout}
	if _, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), seat); err != nil {
		t.Fatal(err)
	}
	return seat
}
func (f regionalCapacityFixture) issueSeat(t *testing.T, seat runtimequota.ServingSeat) {
	t.Helper()
	if _, err := f.ledger.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID); err != nil {
		t.Fatal(err)
	}
}
func (f regionalCapacityFixture) stop(t *testing.T, p runtimequota.CapacityPermit, fence string) RegionalRuntimeCapacityReceipt {
	t.Helper()
	e, err := f.cell.SignCapacityStop(runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.authority.AcceptStop(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f regionalCapacityFixture) stopSeat(t *testing.T, seat runtimequota.ServingSeat, fence string) RegionalRuntimeServingSeatReceipt {
	t.Helper()
	e, err := f.cell.SignServingSeatStop(runtimequota.ServingSeatStop{Seat: seat, FenceID: fence, AdmissionFrozen: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.authority.AcceptServingSeatStop(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f regionalCapacityFixture) usage(t *testing.T, physical, issued, seats int64) {
	t.Helper()
	p, err := f.ledger.RegionalRuntimeCapacityUsage(t.Context(), f.app(), f.quota.grant.AllocationID)
	if err != nil || p.TotalHeld != physical || p.IssuedHeld != issued {
		t.Fatalf("physical holds: %+v %v", p, err)
	}
	s, err := f.ledger.RegionalRuntimeServingSeatUsage(t.Context(), f.app(), f.quota.grant.AllocationID)
	if err != nil || s.TotalHeld != seats || s.SteadyHeld+s.RolloutHeld != seats {
		t.Fatalf("serving holds: %+v %v", s, err)
	}
}

func runRegionalRuntimeCapacityContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	t.Run("physical-issue-and-cancel-tombstone", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		old, _ := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		retry, err := f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), p)
		if err != nil || !reflect.DeepEqual(old, retry) {
			t.Fatalf("reserve retry: %+v %v", retry, err)
		}
		fence := uuid.NewString()
		closed, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, fence)
		if err != nil || closed.ReleasedAt == nil || closed.IssuedAt != nil {
			t.Fatalf("cancel: %+v %v", closed, err)
		}
		if _, err = f.ledger.IssueRegionalRuntimeCapacity(t.Context(), f.app(), p.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("cancelled permit reissued: %v", err)
		}
		retry, err = f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), p)
		if err != nil || !reflect.DeepEqual(closed, retry) {
			t.Fatalf("cancel replay: %+v %v", retry, err)
		}
		for _, mutate := range []func(*runtimequota.CapacityPermit){func(q *runtimequota.CapacityPermit) { q.HostEpochID = uuid.NewString() }, func(q *runtimequota.CapacityPermit) { q.RunID = uuid.NewString() }, func(q *runtimequota.CapacityPermit) {
			q.Grant.Policy.Concurrency++
			q.Grant.Policy.PeakConcurrency++
			q.Grant.Policy.ID = api.RuntimeQuotaPolicyID(q.Grant.Policy.Plan, q.Grant.Policy.Concurrency, q.Grant.Policy.PeakConcurrency)
		}} {
			bad := p
			mutate(&bad)
			if _, err = f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), bad); !errors.Is(err, ErrConflict) {
				t.Fatalf("permit rebound: %v", err)
			}
		}
		sameAttempt := p
		sameAttempt.ID = uuid.NewString()
		if _, err = f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), sameAttempt); !errors.Is(err, ErrConflict) {
			t.Fatalf("physical attempt rebound: %v", err)
		}
		f.usage(t, 0, 0, 0)
		head, err := f.quota.authority.Store.RegionalRuntimeQuotaHead(t.Context(), f.app())
		if err != nil || head.AdmissionEnabled {
			t.Fatalf("bookkeeping activated: %+v %v", head, err)
		}
	})
	t.Run("pause-returns-seat-keeps-physical", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		seat := f.seat(t, p, false)
		if _, err := f.ledger.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("seat issued before physical: %v", err)
		}
		f.issue(t, p)
		f.issueSeat(t, seat)
		f.usage(t, 1, 1, 1)
		fence := uuid.NewString()
		r, err := f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID, fence)
		if err != nil || r.ReleasedAt != nil {
			t.Fatalf("fence lost issued seat: %+v %v", r, err)
		}
		if _, err = f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}); !errors.Is(err, ErrConflict) {
			t.Fatalf("double seat: %v", err)
		}
		closed := f.stopSeat(t, seat, fence)
		f.usage(t, 1, 1, 0)
		closed.Stop.FenceID = uuid.NewString()
		closed.StopEnvelope.Payload = "caller mutation"
		*closed.ReleasedAt = closed.ReleasedAt.Add(1)
		original, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID)
		if err != nil || original.Stop.FenceID != fence || original.StopEnvelope.Payload == "caller mutation" {
			t.Fatalf("mutable seat receipt: %+v %v", original, err)
		}
		fresh := f.seat(t, p, false)
		f.issueSeat(t, fresh)
		f.usage(t, 1, 1, 1)
		replay, err := f.ledger.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID)
		if err != nil || replay.ReleasedAt == nil {
			t.Fatalf("closed issued retry reopened: %+v %v", replay, err)
		}
	})
	t.Run("host-stop-closes-seats-with-original-proof", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		f.issue(t, p)
		seat := f.seat(t, p, false)
		f.issueSeat(t, seat)
		seatFence := uuid.NewString()
		if _, err := f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID, seatFence); err != nil {
			t.Fatal(err)
		}
		fence := uuid.NewString()
		r, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, fence)
		if err != nil || r.ReleasedAt != nil {
			t.Fatalf("host fence released without stop: %+v %v", r, err)
		}
		f.usage(t, 1, 1, 1)
		closed := f.stop(t, p, fence)
		f.usage(t, 0, 0, 0)
		seatClosed, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID)
		if err != nil || !seatClosed.ClosedByPhysicalStop || seatClosed.Stop != nil || seatClosed.FenceID != seatFence || !seatClosed.ReleasedAt.Equal(*closed.ReleasedAt) {
			t.Fatalf("host closure: %+v %v", seatClosed, err)
		}
		late := f.stopSeat(t, seat, seatFence)
		if !reflect.DeepEqual(late, seatClosed) {
			t.Fatal("late pause rewrote host evidence")
		}
		closed.Stop.Permit.RunID = uuid.NewString()
		closed.StopEnvelope.Payload = "mutation"
		*closed.IssuedAt = closed.IssuedAt.Add(1)
		replay := f.stop(t, p, fence)
		if replay.Stop.Permit != p || replay.StopEnvelope.Payload == "mutation" {
			t.Fatal("mutable physical receipt")
		}
		info, err := f.ledger.IssueRegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		if err != nil || info.ReleasedAt == nil {
			t.Fatalf("issued retry reopened: %+v %v", info, err)
		}
	})
	t.Run("steady-rollout-conservation-and-order", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		steady := f.seat(t, p, false)
		same := f.permit(runtimequota.CapacityServing)
		same.DeploymentID = p.DeploymentID
		f.reserve(t, same)
		if _, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), runtimequota.ServingSeat{Permit: same, ID: uuid.NewString(), Rollout: true}); !errors.Is(err, ErrRegionalRuntimeCapacity) {
			t.Fatalf("same deployment rollout: %v", err)
		}
		extra := f.permit(runtimequota.CapacityServing)
		f.reserve(t, extra)
		rollout := f.seat(t, extra, true)
		f.issue(t, extra)
		if _, err := f.ledger.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), rollout.ID); !errors.Is(err, ErrRegionalRuntimeCapacity) {
			t.Fatalf("rollout before steady issuance: %v", err)
		}
		f.issue(t, p)
		f.issueSeat(t, steady)
		f.issueSeat(t, rollout)
		f.seat(t, same, false)
		full := f.permit(runtimequota.CapacityServing)
		f.reserve(t, full)
		for _, rollout := range []bool{false, true} {
			if _, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), runtimequota.ServingSeat{Permit: full, ID: uuid.NewString(), Rollout: rollout}); !errors.Is(err, ErrRegionalRuntimeCapacity) {
				t.Fatalf("over quota rollout=%v: %v", rollout, err)
			}
		}
		f.usage(t, 4, 2, 3)
	})
	t.Run("fenced-steady-cannot-justify-new-rollout", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		f.issue(t, p)
		steady := f.seat(t, p, false)
		f.issueSeat(t, steady)
		q := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, q)
		rollout := f.seat(t, q, true)
		f.issue(t, q)
		if _, err := f.ledger.FenceRegionalRuntimeServingSeat(t.Context(), f.app(), steady.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if _, err := f.ledger.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), rollout.ID); !errors.Is(err, ErrRegionalRuntimeCapacity) {
			t.Fatalf("fenced steady justified rollout: %v", err)
		}
	})
	t.Run("nonserving-kinds-and-planned-seat-cancellation", func(t *testing.T) {
		f := fixture(t)
		for _, kind := range []string{runtimequota.CapacityPrime, runtimequota.CapacityAppTask} {
			p := f.permit(kind)
			f.reserve(t, p)
			f.issue(t, p)
			if _, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("nonserving seat: %v", err)
			}
		}
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		seat := f.seat(t, p, false)
		if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		r, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID)
		if err != nil || r.ReleasedAt == nil || r.IssuedAt != nil {
			t.Fatalf("planned seat retained: %+v %v", r, err)
		}
		f.usage(t, 2, 2, 0)
	})
	t.Run("retirement-cancels-plans-retains-issued", func(t *testing.T) {
		f := fixture(t)
		issued := f.permit(runtimequota.CapacityServing)
		f.reserve(t, issued)
		f.issue(t, issued)
		seat := f.seat(t, issued, false)
		f.issueSeat(t, seat)
		planned := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, planned)
		plannedSeat := f.seat(t, planned, false)
		retired, e := f.quota.retirement(t, f.quota.grant)
		if _, err := f.quota.authority.AcceptRetirement(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		p, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), planned.ID)
		if err != nil || p.ReleasedAt == nil || p.FenceID != retired.RetirementID {
			t.Fatalf("planned retirement: %+v %v", p, err)
		}
		s, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), plannedSeat.ID)
		if err != nil || s.ReleasedAt == nil {
			t.Fatalf("planned seat retirement: %+v %v", s, err)
		}
		f.usage(t, 1, 1, 1)
		if _, err = f.ledger.IssueRegionalRuntimeCapacity(t.Context(), f.app(), planned.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("retired plan issue: %v", err)
		}
		if _, err = f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), f.permit(runtimequota.CapacityPrime)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("retired reserve: %v", err)
		}
		f.stop(t, issued, retired.RetirementID)
		f.usage(t, 0, 0, 0)
	})
	t.Run("head-supersession-retains-original-stops", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		f.issue(t, p)
		seat := f.seat(t, p, false)
		planned := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, planned)
		next := f.quota.grant
		next.Generation++
		next.AllocationID = uuid.NewString()
		if _, err := f.quota.authority.AcceptGrant(t.Context(), f.quota.grantEnvelope(t, next)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.ledger.IssueRegionalRuntimeCapacity(t.Context(), f.app(), planned.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("superseded issue: %v", err)
		}
		if _, err := f.ledger.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("superseded seat: %v", err)
		}
		fence := uuid.NewString()
		if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, fence); err != nil {
			t.Fatal(err)
		}
		f.stop(t, p, fence)
	})
	t.Run("exact-stop-and-trust-before-storage", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityServing)
		f.reserve(t, p)
		f.issue(t, p)
		seat := f.seat(t, p, false)
		f.issueSeat(t, seat)
		fence := uuid.NewString()
		if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, fence); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*runtimequota.CapacityStop){func(s *runtimequota.CapacityStop) { s.Permit.RunID = uuid.NewString() }, func(s *runtimequota.CapacityStop) { s.Permit.HostEpochID = uuid.NewString() }, func(s *runtimequota.CapacityStop) { s.Permit.DeploymentID = uuid.NewString() }, func(s *runtimequota.CapacityStop) { s.FenceID = uuid.NewString() }, func(s *runtimequota.CapacityStop) { s.Permit.Grant.Concurrency++ }} {
			stop := runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}
			mutate(&stop)
			e, err := f.cell.SignCapacityStop(stop)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.authority.AcceptStop(t.Context(), e); !errors.Is(err, ErrConflict) {
				t.Fatalf("wrong exact stop: %v", err)
			}
		}
		e, err := f.cell.SignCapacityStop(runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true})
		if err != nil {
			t.Fatal(err)
		}
		wrong := f.authority
		wrong.Cells = runtimequota.Trust{}
		if _, err = wrong.AcceptStop(t.Context(), e); !errors.Is(err, runtimequota.ErrTrust) {
			t.Fatalf("untrusted stop: %v", err)
		}
		f.usage(t, 1, 1, 1)
		wrongSeat := seat
		wrongSeat.Permit.NodeID = uuid.NewString()
		se, err := f.cell.SignServingSeatStop(runtimequota.ServingSeatStop{Seat: wrongSeat, FenceID: fence, AdmissionFrozen: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.authority.AcceptServingSeatStop(t.Context(), se); !errors.Is(err, ErrConflict) {
			t.Fatalf("wrong pause stop: %v", err)
		}
		f.stop(t, p, fence)
	})

	t.Run("key-rotation-retains-first-stop-envelope", func(t *testing.T) {
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
		seatClosed := f.stopSeat(t, seat, seatFence)
		physicalFence := uuid.NewString()
		if _, err := f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, physicalFence); err != nil {
			t.Fatal(err)
		}
		physicalClosed := f.stop(t, p, physicalFence)
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		f.cell, err = runtimequota.NewSigner(f.quota.grant.Binding.CellID, private)
		if err != nil {
			t.Fatal(err)
		}
		f.authority.Cells, err = runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: f.quota.grant.Binding.CellID, Public: pub}})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.stop(t, p, physicalFence), physicalClosed) || !reflect.DeepEqual(f.stopSeat(t, seat, seatFence), seatClosed) {
			t.Fatal("rotation rewrote accepted evidence")
		}
	})
	t.Run("bounded-held-pages", func(t *testing.T) {
		f := fixture(t)
		ids := []string{}
		for range 5 {
			p := f.permit(runtimequota.CapacityPrime)
			f.reserve(t, p)
			ids = append(ids, p.ID)
		}
		sort.Strings(ids)
		first, err := f.ledger.ListRegionalRuntimeCapacity(t.Context(), f.app(), f.quota.grant.AllocationID, "", 2)
		if err != nil || len(first) != 2 || first[1].Permit.ID != ids[1] {
			t.Fatalf("first page: %+v %v", first, err)
		}
		next, err := f.ledger.ListRegionalRuntimeCapacity(t.Context(), f.app(), f.quota.grant.AllocationID, ids[1], 2)
		if err != nil || len(next) != 2 || next[0].Permit.ID != ids[2] {
			t.Fatalf("next page: %+v %v", next, err)
		}
		if _, err = f.ledger.FenceRegionalRuntimeCapacity(t.Context(), f.app(), ids[2], uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		next, err = f.ledger.ListRegionalRuntimeCapacity(t.Context(), f.app(), f.quota.grant.AllocationID, ids[1], 2)
		if err != nil || len(next) != 2 || next[0].Permit.ID != ids[3] {
			t.Fatalf("released in held page: %+v %v", next, err)
		}
		for _, limit := range []int{0, api.RegionalRuntimeCapacityPageMax + 1} {
			if _, err = f.ledger.ListRegionalRuntimeServingSeats(t.Context(), f.app(), f.quota.grant.AllocationID, "", limit); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("unbounded seat page: %v", err)
			}
		}
	})
	t.Run("concurrent-seat-conservation", func(t *testing.T) {
		f := fixture(t)
		var wg sync.WaitGroup
		results := make(chan error, 16)
		for range 16 {
			p := f.permit(runtimequota.CapacityServing)
			f.reserve(t, p)
			wg.Go(func() {
				_, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()})
				results <- err
			})
		}
		wg.Wait()
		close(results)
		admitted := 0
		for err := range results {
			if err == nil {
				admitted++
			} else if !errors.Is(err, ErrRegionalRuntimeCapacity) {
				t.Fatal(err)
			}
		}
		if admitted != 2 {
			t.Fatalf("admitted %d seats", admitted)
		}
		f.usage(t, 16, 0, 2)
	})
	t.Run("retirement-issuance-race", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		_, e := f.quota.retirement(t, f.quota.grant)
		var wg sync.WaitGroup
		var issueErr, retireErr error
		wg.Go(func() { _, issueErr = f.ledger.IssueRegionalRuntimeCapacity(t.Context(), f.app(), p.ID) })
		wg.Go(func() { _, retireErr = f.quota.authority.AcceptRetirement(t.Context(), e) })
		wg.Wait()
		if retireErr != nil || (issueErr != nil && !errors.Is(issueErr, ErrConflict) && !errors.Is(issueErr, ErrRegionalRuntimeQuotaFrozen)) {
			t.Fatalf("race: issue=%v retire=%v", issueErr, retireErr)
		}
		r, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		if err != nil || r.FenceID == "" || (r.IssuedAt == nil) != (r.ReleasedAt != nil) {
			t.Fatalf("lost race hold: %+v %v", r, err)
		}
	})
}

func TestMemRegionalRuntimeCapacityContract(t *testing.T) {
	s := NewMemStore()
	account, err := s.CreateAccount(t.Context(), "capacity@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	cell := uuid.NewString()
	runRegionalRuntimeCapacityContract(t, func(t *testing.T) regionalCapacityFixture { return newRegionalCapacityFixture(t, s, s, account, cell) })
}

func TestMemRegionalRuntimeCapacityCollection(t *testing.T) {
	s := NewMemStore()
	account, err := s.CreateAccount(t.Context(), "collected-capacity@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := newRegionalCapacityFixture(t, s, s, account, uuid.NewString())
	p := f.permit(runtimequota.CapacityWarm)
	f.reserve(t, p)
	f.issue(t, p)
	seat := f.seat(t, p, false)
	f.issueSeat(t, seat)
	s.mu.Lock()
	delete(s.apps, f.app())
	s.mu.Unlock()
	f.usage(t, 1, 1, 1)
	retirement, e := f.quota.retirement(t, f.quota.grant)
	if _, err = f.quota.authority.AcceptRetirement(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	f.stop(t, p, retirement.RetirementID)
	f.usage(t, 0, 0, 0)
	if r, err := f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), p); err != nil || r.Stop == nil {
		t.Fatalf("collected physical replay: %+v %v", r, err)
	}
	if r, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), seat); err != nil || !r.ClosedByPhysicalStop {
		t.Fatalf("collected seat replay: %+v %v", r, err)
	}
}
