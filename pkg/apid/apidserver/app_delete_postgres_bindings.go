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
		WithHint(fmt.Sprintf("Run gregale postgres bindings delete ID for each binding, or gregale data-api delete %s for a Data API.", app.Slug)).
		WithDocs("https://gregale.dev/docs/managed-postgres")
}
