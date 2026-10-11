package globaldirectory

// adr: 798
import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *MemoryStore) GlobalRuntimeUsageBinding(ctx context.Context, g runtimequota.Grant) (state.GlobalRuntimeUsageBinding, error) {
	if err := ctx.Err(); err != nil {
		return state.GlobalRuntimeUsageBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, app := range s.apps {
		if app.ID != g.GlobalAppID {
			continue
		}
		o := s.owners[app.OwnerID]
		allocation, ok := s.runtimeQuotas[app.ID][g.AllocationID]
		accountID, parseErr := uuid.Parse(o.AccountID)
		if !ok || o.Shared() || parseErr != nil || accountID.String() != g.OwnerID {
			return state.GlobalRuntimeUsageBinding{}, state.ErrConflict
		}
		for _, t := range allocation.Targets {
			if t.CellID != g.Binding.CellID || t.IssuedAt == nil {
				continue
			}
			original, err := quotaWireGrant(s.runtimeQuotaIssuer, allocation, t)
			if err != nil || original != g {
				return state.GlobalRuntimeUsageBinding{}, state.ErrConflict
			}
			return state.GlobalRuntimeUsageBinding{Grant: original, AccountID: o.AccountID, AppSlug: app.Slug, ReleasedAt: quotaTimeCopy(t.ReleasedAt)}, nil
		}
	}
	return state.GlobalRuntimeUsageBinding{}, state.ErrNotFound
}

func quotaTimeCopy(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func (s *MemoryStore) GlobalRuntimeUsageObligations(ctx context.Context, account string, start, end time.Time) ([]state.GlobalRuntimeUsageBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []state.GlobalRuntimeUsageBinding{}
	for _, app := range s.apps {
		o := s.owners[app.OwnerID]
		if o.Shared() || o.AccountID != account {
			continue
		}
		for _, allocation := range s.runtimeQuotas[app.ID] {
			for _, t := range allocation.Targets {
				if t.IssuedAt == nil || !t.IssuedAt.Before(end) || t.ReleasedAt != nil && !t.ReleasedAt.After(start) {
					continue
				}
				g, err := quotaWireGrant(s.runtimeQuotaIssuer, allocation, t)
				if err != nil {
					return nil, err
				}
				out = append(out, state.GlobalRuntimeUsageBinding{Grant: g, AccountID: o.AccountID, AppSlug: app.Slug, ReleasedAt: quotaTimeCopy(t.ReleasedAt)})
			}
		}
	}
	return out, nil
}
