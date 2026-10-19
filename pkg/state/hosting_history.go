package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type HostingHistoryStore interface {
	CreateHostingReleaseWithProvenance(context.Context, string, string, api.HostingManifest, *api.HostingReleaseProvenance) (api.HostingRelease, error)
	PromoteHostingReleaseRecorded(context.Context, string, string, string, int64, api.HostingPromotionActor) (api.HostingSite, error)
	ListHostingHistory(context.Context, string, string, int64) (api.HostingHistoryPage, error)
}

func cloneHostingProvenance(p *api.HostingReleaseProvenance) *api.HostingReleaseProvenance {
	if p == nil {
		return nil
	}
	out := *p
	return &out
}
func validHostingActor(a api.HostingPromotionActor) bool {
	return uuid.Validate(a.AccountID) == nil && (a.KeyID == "" || uuid.Validate(a.KeyID) == nil)
}
func hostingHistoryPage(events []api.HostingPromotionEvent) api.HostingHistoryPage {
	page := api.HostingHistoryPage{Events: events, RetentionLimit: api.HostingHistoryPerSite}
	if len(page.Events) > api.HostingHistoryPageSize {
		page.Events = page.Events[:api.HostingHistoryPageSize]
		page.NextBeforeRevision = page.Events[len(page.Events)-1].Revision
	}
	return page
}
