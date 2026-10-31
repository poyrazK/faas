package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

// visibleAccountApps applies the account inventories' rule
// (appaccess.VisibleApps) to apps, the account's own app list, for a view
// that reads per-app rows without addressing an app in its URL. hidden names
// the apps the caller may no longer see, so the view can drop their rows
// while rows of a deleted app keep their account scope. A failed check hides
// every app.
func (s *server) visibleAccountApps(r *http.Request, acct state.Account, apps []state.App) (visible []state.App, hidden map[string]bool, err error) {
	visible, err = appaccess.VisibleApps(r, s.store, actorAccount(r, acct), apps)
	if err != nil {
		visible = nil
	}
	shown := make(map[string]bool, len(visible))
	for _, app := range visible {
		shown[app.ID] = true
	}
	hidden = make(map[string]bool)
	for _, app := range apps {
		if !shown[app.ID] {
			hidden[app.ID] = true
		}
	}
	return visible, hidden, err
}
