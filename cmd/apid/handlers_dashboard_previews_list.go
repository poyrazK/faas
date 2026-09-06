// Mega-C PR-1 / issue #961 leaf 3 dashboard polish:
// /dashboard/previews — a global previews page for the account.
//
// Customers running many apps with parallel PRs quickly lose
// track of which preview is open against which app. The per-app
// preview panel (app_detail.html:307-374) covers the drill-down;
// this page covers the cross-account overview. The shape mirrors
// renderAppsList: every preview row across every parent, ordered
// by CreatedAt DESC, with a "Tear down" link that POSTs to the
// new /v1/preview/{slug}/destroy endpoint via the dashboard's
// existing CSRF envelope.
//
// Cross-account isolation: ListPreviewsForAccount filters by
// acct.ID inside the store, so a signed-in account can never see
// another account's preview rows even via this view.
package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderPreviewsList renders /dashboard/previews. Data slice is
// the account's preview rows ordered by CreatedAt DESC; the
// template (pkg/dashboard/templates/previews_list.html) renders
// each row with a parent link + a "Tear down" link to the destroy
// endpoint.
//
// Empty-state matches the apps-list empty-state shape so a fresh
// account lands on a helpful "no previews yet" page rather than
// an empty table.
func (s *server) renderPreviewsList(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	rows, err := s.store.ListPreviewsForAccount(ctx, acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	items := make([]dashboard.PreviewListItem, 0, len(rows))
	for _, p := range rows {
		items = append(items, dashboard.PreviewListItem{
			Slug:          p.Slug,
			ParentSlug:    p.PreviewOfSlug,
			PRNumber:      p.PreviewPrNumber,
			IsDev:         p.PreviewPrNumber == 0,
			PRState:       p.PreviewPrState,
			ExpiresAt:     p.PreviewExpiresAt,
			CreatedAt:     p.CreatedAt,
			Hostname:      previewHostnameFor(p.Slug),
			DestroyAction: "/dashboard/apps/" + p.PreviewOfSlug + "/preview/" + p.Slug + "/destroy",
		})
	}
	nonce := httpsec.NonceFromContext(ctx)
	page := dashboard.Page{
		Title:   "Previews",
		Body:    "previews_list",
		Account: dashboardAccountView(acct, len(items)),
		Data:    items,
	}
	if err := dashboard.Render(w, log, nonce, page); err != nil {
		log.Error("dashboard renderPreviewsList: template", "err", err, "account_id", acct.ID)
	}
}

// previewHostnameFor mirrors pkg/githubd.previewHostnameForSlug
// without importing the githubd package (dashboard is apid-side,
// githubd is daemon-side). The canonical preview hostname is
// <slug>.apps.gregale.dev; this returns just the slug's hostname
// for the dashboard's "Open preview" link. Empty string for
// empty slug so the template can guard.
func previewHostnameFor(slug string) string {
	if slug == "" {
		return ""
	}
	return slug + ".apps.gregale.dev"
}
