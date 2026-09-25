package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func watchDeclaredRouteInvalidations(ctx context.Context, pool *pgxpool.Pool, matcher *declaredRoutesMatcher, log *slog.Logger) {
	if pool == nil || matcher == nil {
		return
	}
	notifications, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyAppOpenAPIDocChanged}, log)
	if err != nil {
		if log != nil {
			log.Warn("gateway: declared-route invalidation subscription failed", "err", err)
		}
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case notification, ok := <-notifications:
			if !ok {
				return
			}
			var payload struct {
				AppID string `json:"app_id"`
			}
			if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil || payload.AppID == "" {
				if log != nil {
					log.Warn("gateway: malformed declared-route invalidation", "payload", notification.Payload)
				}
				continue
			}
			matcher.Invalidate(payload.AppID)
		}
	}
}

// declaredRouteDocStore is the only control-plane capability needed by the
// gateway's route-contract cache. Keeping this smaller than state.Store makes
// the matcher straightforward to unit test and keeps the gateway package free
// of a pkg/state dependency.
type declaredRouteDocStore interface {
	GetAppOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, state.AppOpenAPIDocMeta, error)
}

// ResolveScopedRoutePolicy overlays an environment-owned contract only for
// exact deployment URLs. The ordinary application hostname keeps its legacy
// application-wide contract. An absent scoped row is a backwards-compatible
// fallback; a storage error fails closed at the handler boundary.
func (m *declaredRoutesMatcher) ResolveScopedRoutePolicy(ctx context.Context, app gateway.App) (gateway.App, error) {
	if app.PinnedDeploymentScope == "" || m == nil || m.store == nil {
		return app, nil
	}
	store, ok := m.store.(interface {
		GetProjectEnvironmentRoutePolicy(context.Context, string, string, string) (state.ProjectEnvironmentRoutePolicy, error)
	})
	if !ok {
		return app, nil
	}
	policy, err := store.GetProjectEnvironmentRoutePolicy(ctx, app.AccountID, app.ID, app.PinnedDeploymentScope)
	if errors.Is(err, state.ErrNotFound) {
		return app, nil
	}
	if err != nil {
		return gateway.App{}, err
	}
	app.OnlyAllowDeclaredRoutes = policy.OnlyAllowDeclaredRoutes
	app.DeclaredRoutes = gatewayDeclaredRoutes(policy.DeclaredRoutes)
	return app, nil
}

type compiledDeclaredRoute struct {
	path           string
	methods        map[string]struct{}
	staticSegments int
}

type declaredRoutePolicy struct {
	routes  []compiledDeclaredRoute
	expires time.Time
	missing bool
}

// declaredRoutesMatcher compiles an app's imported OpenAPI document once and
// reuses it on the request hot path. Explicit App.DeclaredRoutes take
// precedence and need no database read. Entries are invalidated by the
// app_openapi_doc_changed watcher and also expire after a short TTL as a
// safety net if a notification is lost.
type declaredRoutesMatcher struct {
	store declaredRouteDocStore
	ttl   time.Duration
	now   func() time.Time

	mu      sync.RWMutex
	entries map[string]declaredRoutePolicy
}

func newDeclaredRoutesMatcher(store declaredRouteDocStore) *declaredRoutesMatcher {
	return &declaredRoutesMatcher{
		store:   store,
		ttl:     5 * time.Minute,
		now:     time.Now,
		entries: make(map[string]declaredRoutePolicy),
	}
}

func (m *declaredRoutesMatcher) MatchDeclaredRoute(ctx context.Context, app gateway.App, requestPath, requestMethod string) (bool, error) {
	policy, err := m.loadPolicy(ctx, app)
	if err != nil {
		return false, err
	}
	if policy.missing {
		return false, state.ErrNotFound
	}
	_, matched := matchingDeclaredTemplate(policy.routes, requestPath, requestMethod)
	return matched, nil
}

// ResolveObservedRoute reuses the declaration cache for metrics and discovery.
// A missing document is normal for zero-config apps, so the caller can fall
// back to conservative identifier inference without altering route policy.
func (m *declaredRoutesMatcher) ResolveObservedRoute(ctx context.Context, app gateway.App, requestPath, requestMethod string) (string, bool, error) {
	policy, err := m.loadPolicy(ctx, app)
	if err != nil {
		return "", false, err
	}
	if policy.missing {
		return "", false, nil
	}
	template, matched := matchingDeclaredTemplate(policy.routes, requestPath, requestMethod)
	return template, matched, nil
}

func (m *declaredRoutesMatcher) loadPolicy(ctx context.Context, app gateway.App) (declaredRoutePolicy, error) {
	if len(app.DeclaredRoutes) > 0 {
		return compileDeclaredRoutes(app.DeclaredRoutes)
	}
	if m == nil || m.store == nil {
		return declaredRoutePolicy{}, fmt.Errorf("declared route document store is not configured")
	}

	now := m.now()
	m.mu.RLock()
	entry, ok := m.entries[app.ID]
	m.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry, nil
	}

	doc, _, err := m.store.GetAppOpenAPIDoc(ctx, app.ID, app.AccountID)
	if errors.Is(err, state.ErrNotFound) {
		policy := declaredRoutePolicy{expires: now.Add(m.ttl), missing: true}
		m.mu.Lock()
		m.entries[app.ID] = policy
		m.mu.Unlock()
		return policy, nil
	}
	if err != nil {
		return declaredRoutePolicy{}, fmt.Errorf("load OpenAPI document for app %s: %w", app.ID, err)
	}
	policy, err := compileOpenAPIDocument(doc)
	if err != nil {
		return declaredRoutePolicy{}, err
	}
	policy.expires = now.Add(m.ttl)
	m.mu.Lock()
	m.entries[app.ID] = policy
	m.mu.Unlock()
	return policy, nil
}

func (m *declaredRoutesMatcher) Invalidate(appID string) {
	if m == nil || appID == "" {
		return
	}
	m.mu.Lock()
	delete(m.entries, appID)
	m.mu.Unlock()
}

func compileOpenAPIDocument(doc []byte) (declaredRoutePolicy, error) {
	spec, err := openapidiff.LoadBytes(doc)
	if err != nil {
		return declaredRoutePolicy{}, fmt.Errorf("compile OpenAPI document: %w", err)
	}
	routes := make([]gateway.DeclaredRoute, 0, len(spec.Paths))
	for path, item := range spec.Paths {
		if item == nil {
			continue
		}
		methods := make([]string, 0, len(item.Methods))
		for method := range item.Methods {
			methods = append(methods, strings.ToUpper(method))
		}
		routes = append(routes, gateway.DeclaredRoute{Path: path, Methods: methods})
	}
	return compileDeclaredRoutes(routes)
}

func compileDeclaredRoutes(routes []gateway.DeclaredRoute) (declaredRoutePolicy, error) {
	compiled := make([]compiledDeclaredRoute, 0, len(routes))
	for _, route := range routes {
		path := normalizeDeclaredPath(route.Path)
		if path == "" {
			return declaredRoutePolicy{}, fmt.Errorf("declared route path must start with '/': %q", route.Path)
		}
		methods := make(map[string]struct{}, len(route.Methods))
		for _, method := range route.Methods {
			method = strings.ToUpper(strings.TrimSpace(method))
			if method == "" {
				continue
			}
			methods[method] = struct{}{}
		}
		if len(methods) == 0 {
			return declaredRoutePolicy{}, fmt.Errorf("declared route %q has no HTTP methods", path)
		}
		staticSegments := 0
		for _, segment := range splitDeclaredPath(path) {
			if !(strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")) {
				staticSegments++
			}
		}
		compiled = append(compiled, compiledDeclaredRoute{path: path, methods: methods, staticSegments: staticSegments})
	}
	return declaredRoutePolicy{routes: compiled}, nil
}

func normalizeDeclaredPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return ""
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func policyMatches(routes []compiledDeclaredRoute, requestPath, requestMethod string) bool {
	_, matched := matchingDeclaredTemplate(routes, requestPath, requestMethod)
	return matched
}

// Prefer the most specific declared path when a static route overlaps a
// parameter route, regardless of map iteration order in the OpenAPI parser.
func matchingDeclaredTemplate(routes []compiledDeclaredRoute, requestPath, requestMethod string) (string, bool) {
	requestPath = normalizeDeclaredPath(requestPath)
	if requestPath == "" {
		return "", false
	}
	method := strings.ToUpper(strings.TrimSpace(requestMethod))
	best, bestStatic := "", -1
	for _, route := range routes {
		if _, ok := route.methods[method]; !ok {
			// RFC 7231 permits HEAD wherever GET is declared. Treating it as
			// an implicit GET keeps the allowlist compatible with ordinary
			// net/http handlers while still requiring the path itself.
			if method != "HEAD" {
				continue
			}
			if _, ok := route.methods["GET"]; !ok {
				continue
			}
		}
		if declaredPathMatches(route.path, requestPath) {
			if route.staticSegments > bestStatic || (route.staticSegments == bestStatic && (best == "" || route.path < best)) {
				best, bestStatic = route.path, route.staticSegments
			}
		}
	}
	return best, best != ""
}

func declaredPathMatches(template, request string) bool {
	templateParts := splitDeclaredPath(template)
	requestParts := splitDeclaredPath(request)
	if len(templateParts) != len(requestParts) {
		return false
	}
	for i := range templateParts {
		segment := templateParts[i]
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && len(segment) > 2 {
			if requestParts[i] == "" {
				return false
			}
			continue
		}
		if segment != requestParts[i] {
			return false
		}
	}
	return true
}

func splitDeclaredPath(path string) []string {
	if path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}
