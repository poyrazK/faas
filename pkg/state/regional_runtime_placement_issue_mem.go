package state

// adr: 623
import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (s *MemStore) IssueRegionalRuntimePlacement(ctx context.Context, p runtimequota.Placement) error {
	if !validIssuedRegionalPlacement(p) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app := p.Permit.Grant.Binding.RegionalAppID
	if err := reserveRegionalCapacityCheck(p.Permit, s.regionalRuntimeQuotas[capacityKey(app, p.Permit.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return err
	}
	if !s.currentRegionalQuotaApp(p.Permit.Grant.Binding) {
		return ErrNotFound
	}
	physical, err := s.prepareRegionalPhysicalLocked(p.Permit)
	if err != nil {
		return err
	}
	var seat RegionalRuntimeServingSeatReceipt
	if p.Seat != nil {
		seat, err = s.prepareRegionalSeatLocked(*p.Seat)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Commit both staged receipts together. Existing issued timestamps survive
	// retries; no failure above has created a new physical or concurrency hold.
	now := regionalCapacityNow()
	if physical.IssuedAt == nil {
		physical.IssuedAt = &now
	}
	s.regionalRuntimeCapacity[capacityKey(app, p.Permit.ID)] = physical
	if p.Seat != nil {
		if seat.IssuedAt == nil {
			seat.IssuedAt = &now
		}
		s.regionalRuntimeServingSeats[capacityKey(app, p.Seat.ID)] = seat
	}
	return nil
}

func (s *MemStore) prepareRegionalPhysicalLocked(p runtimequota.CapacityPermit) (RegionalRuntimeCapacityReceipt, error) {
	app := p.Grant.Binding.RegionalAppID
	if r, ok := s.regionalRuntimeCapacity[capacityKey(app, p.ID)]; ok {
		return r, checkRegionalPhysicalForIssue(p, r)
	}
	for _, r := range s.regionalRuntimeCapacity {
		q := r.Permit
		if q.Grant.Binding.RegionalAppID == app && q.NodeID == p.NodeID && q.HostEpochID == p.HostEpochID && q.InstanceID == p.InstanceID && q.RunID == p.RunID {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
	}
	return RegionalRuntimeCapacityReceipt{Permit: p, CreatedAt: regionalCapacityNow()}, nil
}

func (s *MemStore) prepareRegionalSeatLocked(seat runtimequota.ServingSeat) (RegionalRuntimeServingSeatReceipt, error) {
	p := seat.Permit
	app := p.Grant.Binding.RegionalAppID
	r, exists := s.regionalRuntimeServingSeats[capacityKey(app, seat.ID)]
	if exists {
		if err := checkRegionalSeatForIssue(seat, r); err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
	}
	rows := s.servingSeatsLocked(app, p.Grant.AllocationID)
	if !exists {
		for _, old := range rows {
			if old.ReleasedAt == nil && old.Seat.Permit.ID == p.ID {
				return RegionalRuntimeServingSeatReceipt{}, ErrConflict
			}
		}
		if err := checkServingSeatConservation(seat, rows); err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		r = RegionalRuntimeServingSeatReceipt{Seat: seat, CreatedAt: regionalCapacityNow()}
	}
	if r.IssuedAt == nil {
		if err := checkServingSeatIssue(seat, rows); err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
	}
	return r, nil
}
