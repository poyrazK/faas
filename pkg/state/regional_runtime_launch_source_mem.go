package state

// adr: 633
import (
	"context"
	"sort"
)

func (s *MemStore) RegionalRuntimeLaunchSource(ctx context.Context, app, permit string) (RegionalRuntimeLaunchSource, error) {
	if !validRegionalReleaseIDs(app, permit, permit) {
		return RegionalRuntimeLaunchSource{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeLaunchSource{}, err
	}
	r, ok := s.regionalRuntimeLaunchSources[capacityKey(app, permit)]
	if !ok {
		return RegionalRuntimeLaunchSource{}, ErrNotFound
	}
	return r, nil
}

func (s *MemStore) ListRegionalRuntimeRetirements(ctx context.Context, cell string, after RegionalRuntimeRetirementCursor, limit int) ([]RegionalRuntimeRetirementCandidate, error) {
	if !validRegionalRetirementPage(cell, after, limit) {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([]RegionalRuntimeRetirementCandidate, 0)
	for key, source := range s.regionalRuntimeLaunchSources {
		p := source.Source.Permit
		r := s.regionalRuntimeCapacity[key]
		if p.Grant.Binding.CellID != cell || r.Permit != p || r.IssuedAt == nil || r.FenceID == "" || r.ReleasedAt != nil {
			continue
		}
		app := p.Grant.Binding.RegionalAppID
		if after.AppID != "" && (app < after.AppID || (app == after.AppID && p.ID <= after.PermitID)) {
			continue
		}
		rows = append(rows, RegionalRuntimeRetirementCandidate{Source: source.Source, FenceID: r.FenceID})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Source.Permit, rows[j].Source.Permit
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
