// adr: 570
package main

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/db"
)

// requestPublicRoutingRefresh wakes the routing view (ADR-20261027085556) for
// a notification that can change an app's owner, roster or release graph.
// Release activation and deletion notify app_changed for every member app.
// A deployment route change refreshes synchronously before its
// acknowledgement instead (handleDeploymentRouteInvalidation).
func requestPublicRoutingRefresh(inv invalidator, n db.Notification) {
	refresher, ok := inv.(interface{ RequestPublicRoutingRefresh(string) })
	if !ok {
		return
	}
	var payload struct {
		AppID string `json:"app_id"`
	}
	switch n.Channel {
	case db.NotifyDeploymentChanged:
		if json.Unmarshal([]byte(n.Payload), &payload) == nil && payload.AppID != "" {
			refresher.RequestPublicRoutingRefresh(payload.AppID)
		}
	case db.NotifyAppChanged:
		app, err := appChangedID(n.Payload)
		if err != nil {
			app = "" // an unidentified app refreshes every view
		}
		refresher.RequestPublicRoutingRefresh(app)
	case db.NotifyAppDelete:
		if json.Unmarshal([]byte(n.Payload), &payload) != nil {
			payload.AppID = ""
		}
		refresher.RequestPublicRoutingRefresh(payload.AppID)
	case db.NotifyAccountDeleted:
		refresher.RequestPublicRoutingRefresh("")
	}
}

// refreshPublicRoutingForRoute re-reads the app's routing views before a
// route change is acknowledged: schedd drains the predecessor after every
// gateway acknowledges, so no view may still select it.
func refreshPublicRoutingForRoute(ctx context.Context, inv invalidator, app string) error {
	if refresher, ok := inv.(interface {
		RefreshPublicRouting(context.Context, string) error
	}); ok {
		return refresher.RefreshPublicRouting(ctx, app)
	}
	return nil
}
