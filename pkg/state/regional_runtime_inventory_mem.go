package state

// adr: 635
import (
	"context"
	"sort"
)

func (s *MemStore) ListRegionalRuntimeInventory(ctx context.Context, cell string, after RegionalRuntimeRetirementCursor, limit int) ([]RegionalRuntimeInventoryEntry, error) {
	if !validRegionalRetirementPage(cell, after, limit) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([]RegionalRuntimeInventoryEntry, 0)
	for key, receipt := range s.regionalRuntimeCapacity {
		p := receipt.Permit
		app := p.Grant.Binding.RegionalAppID
		if p.Grant.Binding.CellID != cell || receipt.IssuedAt == nil || receipt.ReleasedAt != nil ||
			(after.AppID != "" && (app < after.AppID || (app == after.AppID && p.ID <= after.PermitID))) {
			continue
		}
		entry := RegionalRuntimeInventoryEntry{Receipt: cloneRegionalCapacity(receipt)}
		if launch, ok := s.regionalRuntimeLaunchSources[key]; ok {
			if launch.Source.Validate() != nil || launch.Source.Permit != p ||
				(launch.Resources != (RegionalRuntimeResources{}) && !launch.Resources.Valid()) {
				return nil, ErrConflict
			}
			source := launch.Source
			entry.Source, entry.Resources = &source, launch.Resources
		}
		for _, seat := range s.regionalRuntimeServingSeats {
			if seat.Seat.Permit == p && seat.IssuedAt != nil && seat.ReleasedAt == nil {
				entry.ServingSeatsHeld++
			}
		}
		rows = append(rows, entry)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Receipt.Permit, rows[j].Receipt.Permit
		if a.Grant.Binding.RegionalAppID != b.Grant.Binding.RegionalAppID {
			return a.Grant.Binding.RegionalAppID < b.Grant.Binding.RegionalAppID
		}
		return a.ID < b.ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
