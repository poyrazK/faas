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
	"github.com/onebox-faas/faas/pkg/routelifecycle"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sync/singleflight"
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
	if pinned, ok := pinnedDeclaredRoutePolicy(ctx, app); ok {
		return pinned.app, nil
	}
	if app.ImportedRoutePolicy != nil {
		return app, nil // The authoritative hostname view already applied the overlay.
	}
	if app.PinnedDeploymentScope == "" || m == nil || m.store == nil {
		return app, nil
	}
	if reader, ok := m.store.(state.DeploymentWorkloadSpecReader); ok && app.PinnedDeploymentID != "" && app.ProjectID != "" {
		expectedScope := app.PinnedDeploymentScope
		if expectedScope == "default" {
			expectedScope = "production"
		}
		spec, err := reader.ProjectEnvironmentWorkloadSpecForDeployment(ctx, app.AccountID, app.ProjectID, app.PinnedDeploymentID)
		if err == nil {
			hash, hashErr := state.WorkloadSettingsHash(spec.Settings)
			if hashErr != nil || hash != spec.Hash || spec.AppID != app.ID || spec.EnvironmentSlug != expectedScope {
				return gateway.App{}, state.ErrConflict
			}
			app.OnlyAllowDeclaredRoutes = spec.Settings.OnlyAllowDeclaredRoutes
			app.DeclaredRoutes = gatewayDeclaredRoutes(spec.Settings.DeclaredRoutes)
			return app, nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return gateway.App{}, err
		}
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
	lifecycle map[string]routelifecycle.Metadata
	routes    []compiledDeclaredRoute
	expires   time.Time
	// verified is when the store last answered for this entry. An expired
	// entry serves at most staleLimit past verified plus ttl.
	verified         time.Time
	missing          bool
	revision         string
	documentRevision string
}

type declaredRouteCacheKey struct{ appID, accountID string }
type declaredRouteLoad struct {
	key         declaredRouteCacheKey
	invalidated bool
}

// declaredRoutesMatcher compiles an app's imported OpenAPI document once and
// reuses it on the request hot path. Explicit App.DeclaredRoutes take
// precedence and need no database read. Entries are invalidated by the
// app_openapi_doc_changed watcher and also expire after a short TTL as a
// safety net if a notification is lost. An expired entry keeps serving while
// one background read renews it; only an app's first load, or an entry
// unverified for longer than staleLimit, reads on the request path, and
// concurrent requests share that read (ADR-20261028110044 amendment 6).
type declaredRoutesMatcher struct {
	store declaredRouteDocStore
	ttl   time.Duration
	now   func() time.Time
	// loadTimeout bounds every document read; retry is how soon a failed
	// renewal reads again; staleLimit is how long past its TTL an entry the
	// store could not renew keeps serving.
	loadTimeout, retry, staleLimit time.Duration

	mu                sync.RWMutex
	entries           map[declaredRouteCacheKey]declaredRoutePolicy
	pending           map[*declaredRouteLoad]struct{}
	deploymentEntries map[deploymentLifecycleKey]declaredRoutePolicy
	generations       map[string]uint64
	// imported holds snapshot-pinned documents by content digest.
	imported map[string]declaredRoutePolicy
	// baselines holds each app's route removal baseline contract, used only
	// to label requests its current contract no longer declares.
	baselines  map[declaredRouteCacheKey]declaredRoutePolicy
	refreshing map[string]bool
	loads      singleflight.Group
	// refreshes tracks background reads so tests can wait for them.
	refreshes sync.WaitGroup
}

func newDeclaredRoutesMatcher(store declaredRouteDocStore) *declaredRoutesMatcher {
	return &declaredRoutesMatcher{
		store:             store,
		ttl:               5 * time.Minute,
		now:               time.Now,
		loadTimeout:       2 * time.Second,
		retry:             10 * time.Second,
		staleLimit:        30 * time.Minute,
		entries:           make(map[declaredRouteCacheKey]declaredRoutePolicy),
		pending:           make(map[*declaredRouteLoad]struct{}),
		deploymentEntries: make(map[deploymentLifecycleKey]declaredRoutePolicy),
		generations:       make(map[string]uint64),
		baselines:         make(map[declaredRouteCacheKey]declaredRoutePolicy),
		refreshing:        make(map[string]bool),
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
//
// A route the current contract does not declare keeps the template of the
// app's route removal baseline, so a removed route's remaining traffic keeps
// its own protected label until removal is approved (ADR-20261028110044
// amendment 6). The baseline only labels: it never admits a request.
func (m *declaredRoutesMatcher) ResolveObservedRoute(ctx context.Context, app gateway.App, requestPath, requestMethod string) (string, bool, error) {
	policy, err := m.loadPolicy(ctx, app)
	if err != nil {
		return "", false, err
	}
	if !policy.missing {
		if template, matched := matchingDeclaredTemplate(policy.routes, requestPath, requestMethod); matched {
			return template, true, nil
		}
	}
	template, matched := matchingDeclaredTemplate(m.removalBaseline(ctx, app), requestPath, requestMethod)
	return template, matched, nil
}

func (m *declaredRoutesMatcher) loadPolicy(ctx context.Context, app gateway.App) (declaredRoutePolicy, error) {
	if pinned, ok := pinnedDeclaredRoutePolicy(ctx, app); ok {
		return pinned.policy, nil
	}
	if len(app.DeclaredRoutes) > 0 {
		return compileDeclaredRoutes(app.DeclaredRoutes)
	}
	if app.ImportedRoutePolicy != nil {
		return m.loadImportedSnapshotPolicy(app)
	}
	if m == nil || m.store == nil {
		return declaredRoutePolicy{}, fmt.Errorf("declared route document store is not configured")
	}

	readDoc := m.store.GetAppOpenAPIDoc
	if contracts, ok := m.store.(state.RouteContractDocStore); ok {
		// A PR preview is held to its production app's document.
		readDoc = contracts.GetRouteContractOpenAPIDoc
	}
	appID, accountID := app.ID, app.AccountID
	read := func(ctx context.Context) (declaredRoutePolicy, error) {
		doc, _, err := readDoc(ctx, appID, accountID)
		if errors.Is(err, state.ErrNotFound) {
			policy := declaredRoutePolicy{missing: true}
			return policy, sealDeclaredRoutePolicy(&policy)
		}
		if err != nil {
			return declaredRoutePolicy{}, fmt.Errorf("load OpenAPI document for app %s: %w", appID, err)
		}
		return compileOpenAPIDocument(doc)
	}
	return m.cachedPolicy(ctx, m.entries, "doc", declaredRouteCacheKey{appID, accountID}, read)
}

// cachedPolicy serves key from cache. A fresh entry is returned as is; an
// expired one keeps serving, while the store last verified it within ttl plus
// staleLimit, as one background read renews it. Otherwise the request reads,
// sharing one bounded read with every concurrent request for the key.
func (m *declaredRoutesMatcher) cachedPolicy(ctx context.Context, cache map[declaredRouteCacheKey]declaredRoutePolicy, kind string, key declaredRouteCacheKey, read func(context.Context) (declaredRoutePolicy, error)) (declaredRoutePolicy, error) {
	now := m.now()
	m.mu.RLock()
	entry, ok := cache[key]
	m.mu.RUnlock()
	if ok && now.Before(entry.verified.Add(m.ttl+m.staleLimit)) {
		if !now.Before(entry.expires) {
			m.refresh(ctx, cache, kind, key, read)
		}
		return entry, nil
	}
	result := m.loads.DoChan(kind+":"+key.appID+":"+key.accountID, func() (any, error) {
		return m.fetch(ctx, cache, key, read)
	})
	select {
	case <-ctx.Done():
		return declaredRoutePolicy{}, ctx.Err()
	case res := <-result:
		if res.Err != nil {
			return declaredRoutePolicy{}, res.Err
		}
		return res.Val.(declaredRoutePolicy), nil
	}
}

// refresh starts one background read for key unless one is running. The
// read keeps ctx's values but not its cancellation.
func (m *declaredRoutesMatcher) refresh(ctx context.Context, cache map[declaredRouteCacheKey]declaredRoutePolicy, kind string, key declaredRouteCacheKey, read func(context.Context) (declaredRoutePolicy, error)) {
	flight := kind + ":" + key.appID + ":" + key.accountID
	m.mu.Lock()
	if m.refreshing[flight] {
		m.mu.Unlock()
		return
	}
	m.refreshing[flight] = true
	m.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	m.refreshes.Add(1)
	go func() {
		defer m.refreshes.Done()
		defer func() {
			m.mu.Lock()
			delete(m.refreshing, flight)
			m.mu.Unlock()
		}()
		<-m.loads.DoChan(flight, func() (any, error) { return m.fetch(ctx, cache, key, read) })
	}()
}

// fetch reads key once under loadTimeout and publishes the result. A failed
// read keeps the previous entry and retries after m.retry; an invalidation
// during the read discards it.
func (m *declaredRoutesMatcher) fetch(ctx context.Context, cache map[declaredRouteCacheKey]declaredRoutePolicy, key declaredRouteCacheKey, read func(context.Context) (declaredRoutePolicy, error)) (any, error) {
	load := &declaredRouteLoad{key: key}
	m.mu.Lock()
	m.pending[load] = struct{}{}
	m.mu.Unlock()
	defer m.finishDeclaredRouteLoad(load)
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.loadTimeout)
	defer cancel()
	policy, err := read(readCtx)
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if load.invalidated {
		return declaredRoutePolicy{}, fmt.Errorf("declared route contract changed during snapshot load")
	}
	if err != nil {
		if previous, ok := cache[key]; ok {
			previous.expires = now.Add(m.retry)
			cache[key] = previous
		}
		return declaredRoutePolicy{}, err
	}
	policy.expires, policy.verified = now.Add(m.ttl), now
	cache[key] = policy
	return policy, nil
}

// removalBaseline returns the compiled contract of the app's route removal
// baseline. It never reads on the request path: a missing or expired entry
// starts one background read and the request labels without it meanwhile.
func (m *declaredRoutesMatcher) removalBaseline(ctx context.Context, app gateway.App) []compiledDeclaredRoute {
	store, ok := m.store.(state.RouteRemovalBaselineDocStore)
	if m == nil || !ok || app.ID == "" {
		return nil
	}
	appID, accountID := app.ID, app.AccountID
	key := declaredRouteCacheKey{appID, accountID}
	now := m.now()
	m.mu.RLock()
	entry, found := m.baselines[key]
	m.mu.RUnlock()
	if !found || !now.Before(entry.expires) {
		m.refresh(ctx, m.baselines, "baseline", key, func(ctx context.Context) (declaredRoutePolicy, error) {
			doc, err := store.RouteRemovalBaselineOpenAPIDoc(ctx, appID, accountID)
			if errors.Is(err, state.ErrNotFound) {
				return declaredRoutePolicy{missing: true}, nil
			}
			if err != nil {
				return declaredRoutePolicy{}, err
			}
			return compileOpenAPIDocument(doc)
		})
	}
	if !found || entry.missing || !now.Before(entry.verified.Add(m.ttl+m.staleLimit)) {
		return nil
	}
	return entry.routes
}

func (m *declaredRoutesMatcher) Invalidate(appID string) {
	if m == nil || appID == "" {
		return
	}
	m.mu.Lock()
	for key := range m.entries {
		if key.appID == appID {
			delete(m.entries, key)
		}
	}
	m.generations[appID]++
	for key := range m.deploymentEntries {
		if key.appID == appID {
			delete(m.deploymentEntries, key)
		}
	}
	for key := range m.baselines {
		if key.appID == appID {
			delete(m.baselines, key)
		}
	}
	for load := range m.pending {
		if load.key.appID == appID {
			load.invalidated = true
		}
	}
	m.mu.Unlock()
}

func compileOpenAPIDocument(doc []byte) (declaredRoutePolicy, error) {
	spec, err := openapidiff.LoadBytes(doc)
	if err != nil {
		return declaredRoutePolicy{}, fmt.Errorf("compile OpenAPI document: %w", err)
	}
	routes := make([]gateway.DeclaredRoute, 0, len(spec.Paths))
	lifecycle := make(map[string]routelifecycle.Metadata)
	for path, item := range spec.Paths {
		if item == nil {
			continue
		}
		methods := make([]string, 0, len(item.Methods))
		for method, operation := range item.Methods {
			metadata, err := routelifecycle.Parse(operation.Raw)
			if err != nil {
				return declaredRoutePolicy{}, fmt.Errorf("lifecycle %s %s: %w", method, path, err)
			}
			lifecycle[strings.ToUpper(method)+" "+normalizeDeclaredPath(path)] = metadata
			methods = append(methods, strings.ToUpper(method))
		}
		routes = append(routes, gateway.DeclaredRoute{Path: path, Methods: methods})
	}
	policy, err := compileDeclaredRoutes(routes)
	policy.lifecycle = lifecycle
	return policy, err
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
			if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
				staticSegments++
			}
		}
		compiled = append(compiled, compiledDeclaredRoute{path: path, methods: methods, staticSegments: staticSegments})
	}
	policy := declaredRoutePolicy{routes: compiled}
	if err := sealDeclaredRoutePolicy(&policy); err != nil {
		return declaredRoutePolicy{}, err
	}
	return policy, nil
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

// ResolveRouteLifecycle uses imported app metadata only on the app hostname.
// Pinned deployment URLs must never inherit a mutable application contract.
func (m *declaredRoutesMatcher) ResolveRouteLifecycle(ctx context.Context, app gateway.App, path, method string) (routelifecycle.Metadata, error) {
	if app.PinnedDeploymentID != "" || app.PinnedDeploymentScope != "" {
		return routelifecycle.Metadata{}, nil
	}
	// Explicit allowlists govern admission; lifecycle still belongs to the import.
	app.DeclaredRoutes = nil
	policy, err := m.loadPolicy(ctx, app)
	if err != nil || policy.missing {
		return routelifecycle.Metadata{}, err
	}
	template, matched := matchingDeclaredTemplate(policy.routes, path, method)
	if !matched {
		return routelifecycle.Metadata{}, nil
	}
	key := strings.ToUpper(method) + " " + template
	metadata, found := policy.lifecycle[key]
	if !found && strings.EqualFold(method, "HEAD") {
		metadata = policy.lifecycle["GET "+template]
	}
	return metadata, nil
}
