package state

// adr: 492
import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var _ RegionalRuntimeCapacityLedger = (*MemStore)(nil)

func (s *MemStore) servingSeatsLocked(app, allocation string) []RegionalRuntimeServingSeatReceipt {
	rows := []RegionalRuntimeServingSeatReceipt{}
	for _, r := range s.regionalRuntimeServingSeats {
		if r.Seat.Permit.Grant.Binding.RegionalAppID == app && r.Seat.Permit.Grant.AllocationID == allocation {
			rows = append(rows, cloneServingSeat(r))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Seat.ID < rows[j].Seat.ID })
	return rows
}

func (s *MemStore) RegionalRuntimeServingSeat(_ context.Context, app, id string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.regionalRuntimeServingSeats[capacityKey(app, id)]
	if !ok {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	return cloneServingSeat(r), nil
}

func (s *MemStore) ListRegionalRuntimeServingSeats(_ context.Context, app, allocation, after string, limit int) ([]RegionalRuntimeServingSeatReceipt, error) {
	if !validCapacityPage(app, allocation, after, limit) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	page := []RegionalRuntimeServingSeatReceipt{}
	for _, r := range s.servingSeatsLocked(app, allocation) {
		if r.ReleasedAt == nil && r.Seat.ID > after {
			page = append(page, r)
			if len(page) == limit {
				break
			}
		}
	}
	return page, nil
}

func (s *MemStore) RegionalRuntimeServingSeatUsage(_ context.Context, app, allocation string) (RegionalRuntimeServingSeatUsage, error) {
	if !validRegionalReleaseIDs(app, allocation, allocation) {
		return RegionalRuntimeServingSeatUsage{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out RegionalRuntimeServingSeatUsage
	for _, r := range s.servingSeatsLocked(app, allocation) {
		if r.ReleasedAt != nil {
			continue
		}
		out.TotalHeld++
		if r.IssuedAt != nil {
			out.IssuedHeld++
		}
		if r.Seat.Rollout {
			out.RolloutHeld++
		} else {
			out.SteadyHeld++
		}
	}
	return out, nil
}

func (s *MemStore) ReserveRegionalRuntimeServingSeat(_ context.Context, seat runtimequota.ServingSeat) (RegionalRuntimeServingSeatReceipt, error) {
	if seat.Validate() != nil {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := seat.Permit
	app := p.Grant.Binding.RegionalAppID
	key := capacityKey(app, seat.ID)
	if r, ok := s.regionalRuntimeServingSeats[key]; ok {
		if r.Seat != seat {
			return RegionalRuntimeServingSeatReceipt{}, ErrConflict
		}
		return cloneServingSeat(r), nil
	}
	physical, ok := s.regionalRuntimeCapacity[capacityKey(app, p.ID)]
	if !ok {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	if physical.Permit != p || physical.FenceID != "" || physical.ReleasedAt != nil {
		return RegionalRuntimeServingSeatReceipt{}, ErrConflict
	}
	if err := reserveRegionalCapacityCheck(p, s.regionalRuntimeQuotas[capacityKey(app, p.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	if !s.currentRegionalQuotaApp(p.Grant.Binding) {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	rows := s.servingSeatsLocked(app, p.Grant.AllocationID)
	for _, r := range rows {
		if r.ReleasedAt == nil && r.Seat.Permit.ID == p.ID {
			return RegionalRuntimeServingSeatReceipt{}, ErrConflict
		}
	}
	if err := checkServingSeatConservation(seat, rows); err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	r := RegionalRuntimeServingSeatReceipt{Seat: seat, CreatedAt: regionalCapacityNow()}
	s.regionalRuntimeServingSeats[key] = r
	return cloneServingSeat(r), nil
}

func (s *MemStore) IssueRegionalRuntimeServingSeat(_ context.Context, app, id string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := capacityKey(app, id)
	r, ok := s.regionalRuntimeServingSeats[key]
	if !ok {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	if r.IssuedAt != nil {
		return cloneServingSeat(r), nil
	}
	if r.ReleasedAt != nil || r.FenceID != "" {
		return RegionalRuntimeServingSeatReceipt{}, ErrConflict
	}
	p := r.Seat.Permit
	physical, ok := s.regionalRuntimeCapacity[capacityKey(app, p.ID)]
	if !ok || physical.Permit != p || physical.IssuedAt == nil || physical.FenceID != "" || physical.ReleasedAt != nil {
		return RegionalRuntimeServingSeatReceipt{}, ErrConflict
	}
	if err := reserveRegionalCapacityCheck(p, s.regionalRuntimeQuotas[capacityKey(app, p.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	if !s.currentRegionalQuotaApp(p.Grant.Binding) {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	if err := checkServingSeatIssue(r.Seat, s.servingSeatsLocked(app, p.Grant.AllocationID)); err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	now := regionalCapacityNow()
	r.IssuedAt = &now
	s.regionalRuntimeServingSeats[key] = r
	return cloneServingSeat(r), nil
}

func (s *MemStore) FenceRegionalRuntimeServingSeat(_ context.Context, app, id, fenceID string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(app, id, fenceID) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := capacityKey(app, id)
	r, ok := s.regionalRuntimeServingSeats[key]
	if !ok {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	r, err := fenceServingSeat(r, fenceID)
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	s.regionalRuntimeServingSeats[key] = r
	return cloneServingSeat(r), nil
}

func (s *MemStore) applyRegionalRuntimeServingSeatStop(_ context.Context, stop runtimequota.ServingSeatStop, e runtimequota.Envelope) (RegionalRuntimeServingSeatReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := capacityKey(stop.Seat.Permit.Grant.Binding.RegionalAppID, stop.Seat.ID)
	r, ok := s.regionalRuntimeServingSeats[key]
	if !ok {
		return RegionalRuntimeServingSeatReceipt{}, ErrNotFound
	}
	r, err := stopServingSeat(r, stop, e)
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	s.regionalRuntimeServingSeats[key] = cloneServingSeat(r)
	return cloneServingSeat(r), nil
}

func (s *MemStore) closeSeatsForPhysicalLocked(physical RegionalRuntimeCapacityReceipt) {
	if physical.FenceID == "" {
		return
	}
	for key, r := range s.regionalRuntimeServingSeats {
		if r.ReleasedAt != nil || r.Seat.Permit != physical.Permit {
			continue
		}
		if r.FenceID == "" {
			r.FenceID = physical.FenceID
		}
		if r.IssuedAt == nil {
			now := regionalCapacityNow()
			r.ReleasedAt = &now
		} else if physical.Stop != nil {
			r.ClosedByPhysicalStop = true
			now := *physical.ReleasedAt
			r.ReleasedAt = &now
		}
		s.regionalRuntimeServingSeats[key] = r
	}
}

func (s *MemStore) retireServingSeatsLocked(retirement runtimequota.Retirement) {
	for key, r := range s.regionalRuntimeServingSeats {
		if r.Seat.Permit.Grant != retirement.Grant || r.FenceID != "" {
			continue
		}
		r, _ = fenceServingSeat(r, retirement.RetirementID)
		s.regionalRuntimeServingSeats[key] = r
	}
}
