package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ HostingHistoryStore = (*MemStore)(nil)

func (m *MemStore) ListHostingHistory(_ context.Context, account, site string, before int64) (api.HostingHistoryPage, error) {
	if before < 0 || before > (1<<53)-1 {
		return api.HostingHistoryPage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return api.HostingHistoryPage{}, ErrNotFound
	}
	events := []api.HostingPromotionEvent{}
	for _, event := range m.hostingHistory[site] {
		if before == 0 || event.Revision < before {
			event.Provenance = cloneHostingProvenance(event.Provenance)
			events = append(events, event)
			if len(events) > api.HostingHistoryPageSize {
				break
			}
		}
	}
	return hostingHistoryPage(events), nil
}
