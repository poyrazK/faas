package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

// A managed Redis cache is an internal app (ADR-20261116192126), so every
// generic app route reached it. Prod hunt 2026-11-28 changed caches through
// them and broke each one: a deploy of the Valkey image replaced the cache
// (data gone, no maxmemory, no command deny-list), a fresh restart emptied
// it, a rename left bound apps' REDIS_URL on the old host, and domains,
// scaling, auth and env changes were all accepted. Only the /v1/redis routes
// change a cache; they reach the app handlers by direct calls, never through
// these routes.

// managedCacheAppRoutes are the mutating app routes a cache still takes.
var managedCacheAppRoutes = map[string]bool{
	// A deleted cache comes back with its marker and data snapshot.
	"POST /v1/apps/{slug}/restore": true,
}

// refuseManagedCacheAppChange refuses a mutating /v1 app route that
// addresses a managed cache. It runs after appaccess.Wrap, so a caller who
// may not see the app still gets its not-found answer.
func (s *server) refuseManagedCacheAppChange(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if readOnlyMethod(r.Method) || managedCacheAppRoutes[r.Pattern] {
			next(w, r, acct)
			return
		}
		app, addressed, err := appaccess.AddressedApp(r, s.store, actorAccount(r, acct).ID)
		if err != nil || !addressed {
			next(w, r, acct)
			return
		}
		if problem := s.managedCacheAppProblem(r, app); problem != nil {
			s.refuseRedis(w, r, problem, nil, "app_id", app.ID)
			return
		}
		next(w, r, acct)
	}
}

// managedCacheAppProblem is the refusal for a mutating request on app when
// app is a managed cache, or nil.
func (s *server) managedCacheAppProblem(r *http.Request, app state.App) *api.Problem {
	if readOnlyMethod(r.Method) || managedCacheAppRoutes[r.Pattern] || !s.isManagedCacheApp(r.Context(), app.ID) {
		return nil
	}
	return errManagedCacheAppRoute(app.Slug, r.Pattern)
}

// isManagedCacheApp reports whether appID is a managed cache. A failed
// lookup lets the request through: the route's own store calls fail the
// same way, and refusing every app change on a store error would be worse.
func (s *server) isManagedCacheApp(ctx context.Context, appID string) bool {
	store, ok := s.store.(state.ManagedValkeyStore)
	if !ok || appID == "" {
		return false
	}
	_, err := store.GetManagedValkeyCache(ctx, appID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.WarnContext(ctx, "apid: managed cache lookup failed", "app_id", appID, "err", err)
	}
	return err == nil
}

func readOnlyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// errManagedCacheAppRoute names the gregale redis command that does what
// the refused app route was asked to.
func errManagedCacheAppRoute(name, pattern string) *api.Problem {
	hint := fmt.Sprintf("Manage it with gregale redis: resize, update, restart, bind, unbind or delete %s.", name)
	switch {
	case strings.Contains(pattern, "/restart"):
		hint = fmt.Sprintf("Run gregale redis restart %s; it keeps the cache's data.", name)
	case strings.HasPrefix(pattern, "DELETE /v1/apps/{slug}") && strings.Count(pattern, "/") == 3:
		hint = fmt.Sprintf("Run gregale redis delete %s.", name)
	case strings.Contains(pattern, "/deploy") || strings.Contains(pattern, "/release") || strings.Contains(pattern, "/rollback"):
		hint = fmt.Sprintf("The cache's image and command are managed. Run gregale redis update %s to move it to the current Valkey release, or gregale redis resize %s to change its size; both keep its data.", name, name)
	}
	return api.NewProblem(http.StatusConflict, "managed_cache_app", "Managed Redis cache",
		fmt.Sprintf("%s is a managed Redis cache; app commands cannot change it", name)).WithHint(hint)
}
