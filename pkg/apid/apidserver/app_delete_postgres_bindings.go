package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// appPostgresBindingsProblem refuses to delete an app whose managed
// PostgreSQL bindings are live. Only the binding service revokes a binding's
// provider credential; the app purge cannot (state.
// AppPurgeBlockedManagedPostgresBinding), so a deleted app kept a live
// credential and an unpurgeable tombstone. Bindings already being deleted do
// not block: their revocation is under way.
func (s *server) appPostgresBindingsProblem(ctx context.Context, app state.App) *api.Problem {
	if s.managedPostgresBindings == nil {
		return nil
	}
	bindings, err := s.managedPostgresBindings.ListForApp(ctx, app.AccountID, app.ID, "")
	if errors.Is(err, managedpostgres.ErrUnsupported) {
		return nil
	}
	if err != nil {
		return api.ErrCapacity("list-postgres-bindings")
	}
	var live []string
	for _, binding := range bindings {
		if binding.State != string(managedpostgres.BindingStateDeleting) {
			live = append(live, binding.BindingID)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return api.NewProblem(http.StatusConflict, "app_has_postgres_bindings", "Conflict",
		fmt.Sprintf("Delete this app's managed PostgreSQL bindings before deleting the app: %s.", strings.Join(live, ", "))).
		WithHint(fmt.Sprintf("Run gregale app delete %[1]s --delete-bindings to revoke them first, gregale postgres bindings delete ID for each binding, or gregale data-api delete %[1]s for a Data API.", app.Slug)).
		WithDocs("https://gregale.dev/docs/managed-postgres")
}

// listAppPostgresBindings lists the app's live managed PostgreSQL bindings on
// every database it is bound to, including organization databases and
// environment copies the database listing leaves out, so `gregale data-api
// delete` can revoke exactly what deleting the app refuses to leave live.
func (s *server) listAppPostgresBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	bindings, err := s.managedPostgresBindings.ListForApp(r.Context(), app.AccountID, app.ID, "")
	if errors.Is(err, managedpostgres.ErrUnsupported) {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	items := make([]api.AppPostgresBinding, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, api.AppPostgresBinding{ID: binding.BindingID, Database: binding.DatabaseName, Scope: binding.Scope,
			EnvironmentKey: binding.EnvironmentKey, Access: binding.Access, State: binding.State})
	}
	writeJSON(w, http.StatusOK, api.AppPostgresBindingList{Items: items})
}
