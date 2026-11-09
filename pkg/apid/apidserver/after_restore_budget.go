package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// afterRestoreBudgetProblem refuses an after_restore timeout above the
// ordinary 2 s cap unless the app holds a managed PostgreSQL or object-storage
// binding (ADR-20261109180000). Those apps reconnect to managed services on
// restore; in production the reconnect missed 2 s and every such wake fell
// back to a cold boot. appID is empty for an app being created, which has no
// binding yet.
func (s *server) afterRestoreBudgetProblem(ctx context.Context, accountID, appID string, hook *api.AfterRestoreHook) *api.Problem {
	if hook == nil || hook.TimeoutMS <= api.AfterRestoreHookMaxTimeoutMS {
		return nil
	}
	bound, err := s.appHasManagedBinding(ctx, accountID, appID)
	if err != nil {
		return api.ErrCapacity("could not read the app's managed bindings")
	}
	if bound {
		return nil
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid lifecycle configuration",
		fmt.Sprintf("after_restore.timeout_ms above %d needs a managed PostgreSQL or object-storage binding; this app has none (got %d)",
			api.AfterRestoreHookMaxTimeoutMS, hook.TimeoutMS)).
		WithHint(fmt.Sprintf("Use at most %d ms, or bind the database or bucket the hook reconnects to first.", api.AfterRestoreHookMaxTimeoutMS))
}

func (s *server) appHasManagedBinding(ctx context.Context, accountID, appID string) (bool, error) {
	if appID == "" {
		return false, nil
	}
	if s.managedPostgresBindings != nil {
		bindings, err := s.managedPostgresBindings.ListForApp(ctx, accountID, appID, "")
		if err != nil && !errors.Is(err, managedpostgres.ErrUnsupported) {
			return false, err
		}
		for _, binding := range bindings {
			if binding.State != string(managedpostgres.BindingStateDeleting) && binding.State != string(managedpostgres.BindingStateDeleted) {
				return true, nil
			}
		}
	}
	if store, ok := s.store.(state.ObjectStorageBindingInventoryStore); ok {
		rows, err := store.ListObjectStorageBindingsForApp(ctx, accountID, appID, "")
		if err != nil {
			return false, err
		}
		if len(rows) > 0 {
			return true, nil
		}
	}
	return false, nil
}
