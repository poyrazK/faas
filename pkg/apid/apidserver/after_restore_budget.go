package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
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
	return unboundAfterRestoreBudgetProblem(hook)
}

// manifestAfterRestoreBudgetProblem is afterRestoreBudgetProblem for a deploy
// whose gregale.yaml may declare the binding the hook reconnects to. The
// manifest's databases and buckets are bound later in the same deploy, so a
// declaration for this app counts as a binding (ADR-20261110120000); before,
// a first deploy with a declared database and a longer hook was refused.
func (s *server) manifestAfterRestoreBudgetProblem(ctx context.Context, accountID string, app state.App, manifest *gregalemanifest.Manifest, hook *api.AfterRestoreHook) *api.Problem {
	if hook == nil || hook.TimeoutMS <= api.AfterRestoreHookMaxTimeoutMS || manifestDeclaresManagedBinding(manifest, app.Slug) {
		return nil
	}
	return s.afterRestoreBudgetProblem(ctx, accountID, app.ID, hook)
}

// manifestDeclaresManagedBinding reports whether gregale.yaml declares a
// managed database or bucket for the app. An entry without `app` targets the
// deployed app.
func manifestDeclaresManagedBinding(manifest *gregalemanifest.Manifest, slug string) bool {
	if manifest == nil {
		return false
	}
	targets := func(app string) bool {
		app = strings.ToLower(strings.TrimSpace(app))
		return app == "" || app == strings.ToLower(slug)
	}
	for _, dependency := range manifest.Databases {
		if targets(dependency.App) {
			return true
		}
	}
	for _, dependency := range manifest.Buckets {
		if targets(dependency.App) {
			return true
		}
	}
	return false
}

// unboundAfterRestoreBudgetProblem is the check for an app known to hold no
// managed binding, such as one being created: only the ordinary cap applies.
func unboundAfterRestoreBudgetProblem(hook *api.AfterRestoreHook) *api.Problem {
	if hook == nil || hook.TimeoutMS <= api.AfterRestoreHookMaxTimeoutMS {
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
