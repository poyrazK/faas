package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingHistoryStore = (*PgStore)(nil)

func (s *PgStore) ListHostingHistory(ctx context.Context, account, site string, before int64) (api.HostingHistoryPage, error) {
	if before < 0 || before > (1<<53)-1 {
		return api.HostingHistoryPage{}, ErrInvalidArgument
	}
	if _, err := s.GetHostingSite(ctx, account, site); err != nil {
		return api.HostingHistoryPage{}, err
	}
	si, _ := parsePgUUID(site)
	rows, err := sqlc.New().HostingPromotionList(ctx, s.pool, sqlc.HostingPromotionListParams{SiteID: si, BeforeRevision: before, PageSize: api.HostingHistoryPageSize + 1})
	if err != nil {
		return api.HostingHistoryPage{}, err
	}
	events := make([]api.HostingPromotionEvent, 0, len(rows))
	for _, r := range rows {
		e := api.HostingPromotionEvent{SiteID: site, Revision: r.Revision, PreviousRevision: r.PreviousRevision, ReleaseID: uuidString(r.ReleaseID), PreviousReleaseID: uuidString(r.PreviousReleaseID), Actor: api.HostingPromotionActor{AccountID: uuidString(r.ActorAccountID), KeyID: uuidString(r.ActorKeyID)}, AcceptedAt: r.AcceptedAt.Time}
		if len(r.Provenance) > 0 {
			if err = json.Unmarshal(r.Provenance, &e.Provenance); err != nil {
				return api.HostingHistoryPage{}, err
			}
		}
		events = append(events, e)
	}
	return hostingHistoryPage(events), nil
}
