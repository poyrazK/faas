package state

// adr: 618
import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var _ RegionalRuntimeCapacityStore = (*MemStore)(nil)

func (s *MemStore) CheckRegionalRuntimeIssuedPlacement(ctx context.Context, p runtimequota.Placement) error {
	if !validIssuedRegionalPlacement(p) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	app := p.Permit.Grant.Binding.RegionalAppID
	if err := reserveRegionalCapacityCheck(p.Permit, s.regionalRuntimeQuotas[capacityKey(app, p.Permit.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return err
	}
	if !s.currentRegionalQuotaApp(p.Permit.Grant.Binding) {
		return ErrNotFound
	}
	if err := checkIssuedRegionalPhysical(p.Permit, s.regionalRuntimeCapacity[capacityKey(app, p.Permit.ID)]); err != nil {
		return err
	}
	if p.Seat != nil {
		return checkIssuedRegionalSeat(*p.Seat, s.regionalRuntimeServingSeats[capacityKey(app, p.Seat.ID)])
	}
	return nil
}

func capacityKey(app, id string) string { return app + "/" + id }

func (s *MemStore) capacityRowsLocked(app, allocation string) []RegionalRuntimeCapacityReceipt {
	rows := []RegionalRuntimeCapacityReceipt{}
	for _, r := range s.regionalRuntimeCapacity {
		if r.Permit.Grant.Binding.RegionalAppID == app && r.Permit.Grant.AllocationID == allocation {
			rows = append(rows, cloneRegionalCapacity(r))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Permit.ID < rows[j].Permit.ID })
	return rows
}

func (s *MemStore) ListRegionalRuntimeCapacity(_ context.Context, app, allocation, after string, limit int) ([]RegionalRuntimeCapacityReceipt, error) {
	if !validCapacityPage(app, allocation, after, limit) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	page := []RegionalRuntimeCapacityReceipt{}
	for _, r := range s.capacityRowsLocked(app, allocation) {
		if r.ReleasedAt == nil && r.Permit.ID > after {
			page = append(page, r)
			if len(page) == limit {
				break
			}
		}
	}
	return page, nil
}

func (s *MemStore) RegionalRuntimeCapacityUsage(_ context.Context, app, allocation string) (RegionalRuntimeCapacityUsage, error) {
	if !validRegionalReleaseIDs(app, allocation, allocation) {
		return RegionalRuntimeCapacityUsage{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out RegionalRuntimeCapacityUsage
	for _, r := range s.capacityRowsLocked(app, allocation) {
		if r.ReleasedAt != nil {
			continue
		}
		out.TotalHeld++
		if r.IssuedAt != nil {
			out.IssuedHeld++
		}
	}
	return out, nil
}

func (s *MemStore) RegionalRuntimeCapacity(_ context.Context, app, id string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.regionalRuntimeCapacity[capacityKey(app, id)]
	if !ok {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	return cloneRegionalCapacity(r), nil
}

func (s *MemStore) ReserveRegionalRuntimeCapacity(_ context.Context, p runtimequota.CapacityPermit) (RegionalRuntimeCapacityReceipt, error) {
	if p.Validate() != nil {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app := p.Grant.Binding.RegionalAppID
	key := capacityKey(app, p.ID)
	if old, ok := s.regionalRuntimeCapacity[key]; ok {
		if old.Permit != p {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		return cloneRegionalCapacity(old), nil
	}
	if err := reserveRegionalCapacityCheck(p, s.regionalRuntimeQuotas[capacityKey(app, p.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	if !s.currentRegionalQuotaApp(p.Grant.Binding) {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	for _, old := range s.regionalRuntimeCapacity {
		q := old.Permit
		if q.Grant.Binding.RegionalAppID == app && q.NodeID == p.NodeID && q.HostEpochID == p.HostEpochID && q.InstanceID == p.InstanceID && q.RunID == p.RunID {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
	}
	r := RegionalRuntimeCapacityReceipt{Permit: p, CreatedAt: regionalCapacityNow()}
	s.regionalRuntimeCapacity[key] = r
	return cloneRegionalCapacity(r), nil
}

func (s *MemStore) IssueRegionalRuntimeCapacity(_ context.Context, app, id string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := capacityKey(app, id)
	r, ok := s.regionalRuntimeCapacity[key]
	if !ok {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	if r.IssuedAt != nil {
		return cloneRegionalCapacity(r), nil
	}
	if r.ReleasedAt != nil || r.FenceID != "" {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	if err := reserveRegionalCapacityCheck(r.Permit, s.regionalRuntimeQuotas[capacityKey(app, r.Permit.Grant.AllocationID)], s.regionalRuntimeQuotaHeads[app]); err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	if !s.currentRegionalQuotaApp(r.Permit.Grant.Binding) {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	now := regionalCapacityNow()
	r.IssuedAt = &now
	s.regionalRuntimeCapacity[key] = r
	return cloneRegionalCapacity(r), nil
}

func (s *MemStore) FenceRegionalRuntimeCapacity(_ context.Context, app, id, fenceID string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(app, id, fenceID) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := capacityKey(app, id)
	r, ok := s.regionalRuntimeCapacity[key]
	if !ok {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	r, err := fenceRegionalCapacity(r, fenceID)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	s.regionalRuntimeCapacity[key] = r
	s.closeSeatsForPhysicalLocked(r)
	return cloneRegionalCapacity(r), nil
}

func (s *MemStore) applyRegionalRuntimeCapacityStop(_ context.Context, stop runtimequota.CapacityStop, e runtimequota.Envelope) (RegionalRuntimeCapacityReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := capacityKey(stop.Permit.Grant.Binding.RegionalAppID, stop.Permit.ID)
	r, ok := s.regionalRuntimeCapacity[key]
	if !ok {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	r, err := stopRegionalCapacity(r, stop, e)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	s.regionalRuntimeCapacity[key] = cloneRegionalCapacity(r)
	s.closeSeatsForPhysicalLocked(r)
	return cloneRegionalCapacity(r), nil
}

// Retirement and issuance share the journal lock, so an unissued permit is
// either cancelled here or already retained as potentially executing.
func (s *MemStore) retireRegionalCapacityLocked(retirement runtimequota.Retirement) {
	for key, r := range s.regionalRuntimeCapacity {
		if r.Permit.Grant != retirement.Grant || r.FenceID != "" {
			continue
		}
		r, _ = fenceRegionalCapacity(r, retirement.RetirementID)
		s.regionalRuntimeCapacity[key] = r
	}
	s.retireServingSeatsLocked(retirement)
}
