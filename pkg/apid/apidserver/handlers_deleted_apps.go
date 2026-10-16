package apidserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/apprecovery"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) recoveryHandlers() apprecovery.Handler {
	return apprecovery.Handler{
		Store: s.store, RestoreWithActivity: s.restoreAppWithActivity,
		OnRestored: func(ctx context.Context, _ *http.Request, acct state.Account, app state.App) {
			_ = s.notif.Notify(ctx, db.NotifyAppChanged, fmt.Sprintf(`{"app_id":"%s","kind":"restored"}`, app.ID))
			s.audit.Emit(ctx, "app.restored", &acct.ID, map[string]any{"app_id": app.ID, "slug": app.Slug})
		},
		Response: s.appResponseWithContext, ParkedRef: s.withParkedDeploymentRef,
	}
}

func (s *server) listDeletedApps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.recoveryHandlers().List(w, r, acct)
}
func (s *server) getDeletedApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.recoveryHandlers().Get(w, r, acct)
}
func (s *server) restoreDeletedApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.recoveryHandlers().Restore(w, r, acct)
}
func (s *server) restoreApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.recoveryHandlers().LegacyRestore(w, r, acct)
}
