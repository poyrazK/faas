package state

// adr: 626
import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (s *MemStore) RegionalRuntimeServingResume(ctx context.Context, app, id string) (RegionalRuntimeServingResumeReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeServingResumeReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	r, ok := s.regionalRuntimeServingResumes[capacityKey(app, id)]
	if !ok {
		return RegionalRuntimeServingResumeReceipt{}, ErrNotFound
	}
	return r, nil
}

func (s *MemStore) HeldRegionalRuntimeServingResumeForInstance(ctx context.Context, app, instance, node, run string) (RegionalRuntimeServingResumeReceipt, error) {
	if !validResumeInstanceIDs(app, instance, node, run) {
		return RegionalRuntimeServingResumeReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	var found RegionalRuntimeServingResumeReceipt
	for _, r := range s.regionalRuntimeServingResumes {
		p := r.Intent.Seat.Permit
		seat := s.regionalRuntimeServingSeats[capacityKey(app, r.Intent.Seat.ID)]
		if p.Grant.Binding.RegionalAppID != app || p.InstanceID != instance || p.NodeID != node || p.RunID != run || seat.IssuedAt == nil || seat.ReleasedAt != nil {
			continue
		}
		if found.Intent.ID != "" {
			return RegionalRuntimeServingResumeReceipt{}, ErrConflict
		}
		found = r
	}
	if found.Intent.ID == "" {
		return RegionalRuntimeServingResumeReceipt{}, ErrNotFound
	}
	return found, nil
}

func (s *MemStore) IssueRegionalRuntimeServingResume(ctx context.Context, i runtimequota.ServingResumeIntent) (RegionalRuntimeServingResumeReceipt, error) {
	if i.Validate() != nil {
		return RegionalRuntimeServingResumeReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	p := i.Seat.Permit
	app := p.Grant.Binding.RegionalAppID
	if err := reserveRegionalCapacityCheck(p, s.regionalRuntimeQuotas[capacityKey(app, p.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	if !s.currentRegionalQuotaApp(p.Grant.Binding) {
		return RegionalRuntimeServingResumeReceipt{}, ErrNotFound
	}
	if err := checkIssuedRegionalPhysical(p, s.regionalRuntimeCapacity[capacityKey(app, p.ID)]); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	r, exists := s.regionalRuntimeServingResumes[capacityKey(app, i.ID)]
	if !exists {
		for _, old := range s.regionalRuntimeServingResumes {
			if old.Intent.Seat.Permit.Grant.Binding.RegionalAppID == app && old.Intent.Seat.Permit.ID == p.ID && s.regionalRuntimeServingSeats[capacityKey(app, old.Intent.Seat.ID)].ReleasedAt == nil {
				r, exists = old, true
				break
			}
		}
	}
	if exists {
		if err := checkResumeRetry(i, r.Intent); err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		return r, checkIssuedRegionalSeat(r.Intent.Seat, s.regionalRuntimeServingSeats[capacityKey(app, r.Intent.Seat.ID)])
	}
	seat, err := s.prepareRegionalSeatLocked(i.Seat)
	if err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, err
	}
	now := regionalCapacityNow()
	if seat.IssuedAt == nil {
		seat.IssuedAt = &now
	}
	r = RegionalRuntimeServingResumeReceipt{Intent: i, CreatedAt: now}
	s.regionalRuntimeServingSeats[capacityKey(app, i.Seat.ID)] = seat
	s.regionalRuntimeServingResumes[capacityKey(app, i.ID)] = r
	return r, nil
}
