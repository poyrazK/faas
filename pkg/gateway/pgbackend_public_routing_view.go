// adr: 570
package gateway

import (
	"context"

	"github.com/onebox-faas/faas/pkg/publicrouting"
)

func (b *PGBackend) WithPublicRoutingView(view *publicrouting.Registry) *PGBackend {
	b.publicRoutingView.Store(view)
	return b
}

// RequestPublicRoutingRefresh schedules a re-read of the app's routing views
// (every view when app is empty). Notifications are wake-ups only.
func (b *PGBackend) RequestPublicRoutingRefresh(app string) {
	if view := b.publicRoutingView.Load(); view != nil {
		view.RequestRefresh(app)
	}
}

// RefreshPublicRouting re-reads the app's routing views before returning, so
// a route-change acknowledgement covers the views requests admit from.
func (b *PGBackend) RefreshPublicRouting(ctx context.Context, app string) error {
	if view := b.publicRoutingView.Load(); view != nil {
		return view.RefreshApp(ctx, app)
	}
	return nil
}
