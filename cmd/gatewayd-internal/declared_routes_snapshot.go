// adr: 570
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type declaredRouteSnapshotKey struct{}

type declaredRouteSnapshot struct {
	app    gateway.App
	policy declaredRoutePolicy
}

var _ gateway.DeclaredRoutePolicySnapshotter = (*declaredRoutesMatcher)(nil)

func pinnedDeclaredRoutePolicy(ctx context.Context, app gateway.App) (declaredRouteSnapshot, bool) {
	snapshot, ok := ctx.Value(declaredRouteSnapshotKey{}).(declaredRouteSnapshot)
	return snapshot, ok && snapshot.app.ID == app.ID && snapshot.app.AccountID == app.AccountID && snapshot.app.PinnedDeploymentScope == app.PinnedDeploymentScope
}

func (m *declaredRoutesMatcher) PinDeclaredRoutePolicy(ctx context.Context, app gateway.App) (context.Context, gateway.App, string, error) {
	if pinned, ok := pinnedDeclaredRoutePolicy(ctx, app); ok {
		return ctx, pinned.app, pinned.policy.revision, nil
	}
	if app.PublicPolicySource != nil && app.ImportedRoutePolicy == nil {
		return nil, gateway.App{}, "", fmt.Errorf("public route contract projection is unavailable")
	}
	resolved, err := m.ResolveScopedRoutePolicy(ctx, app)
	if err != nil {
		return nil, gateway.App{}, "", fmt.Errorf("pin scoped route contract: %w", err)
	}
	policy, err := m.loadPolicy(ctx, resolved)
	if err != nil {
		return nil, gateway.App{}, "", fmt.Errorf("pin declared route contract: %w", err)
	}
	// Explicit/scoped source lists are copied too. The compiled policy is
	// immutable after publication; invalidation replaces the cache entry.
	resolved.DeclaredRoutes = slices.Clone(resolved.DeclaredRoutes)
	for index := range resolved.DeclaredRoutes {
		resolved.DeclaredRoutes[index].Methods = slices.Clone(resolved.DeclaredRoutes[index].Methods)
	}
	if resolved.ImportedRoutePolicy != nil {
		contract := *resolved.ImportedRoutePolicy
		contract.Document = slices.Clone(contract.Document)
		resolved.ImportedRoutePolicy = &contract
	}
	ctx = context.WithValue(ctx, declaredRouteSnapshotKey{}, declaredRouteSnapshot{app: resolved, policy: policy})
	return ctx, resolved, policy.revision, nil
}

func sealDeclaredRoutePolicy(policy *declaredRoutePolicy) error {
	routes := make([]gateway.DeclaredRoute, 0, len(policy.routes))
	for _, compiled := range policy.routes {
		methods := make([]string, 0, len(compiled.methods))
		for method := range compiled.methods {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		routes = append(routes, gateway.DeclaredRoute{Path: compiled.path, Methods: methods})
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return strings.Join(routes[i].Methods, ",") < strings.Join(routes[j].Methods, ",")
	})
	encoded, err := json.Marshal(struct {
		Missing bool
		Routes  []gateway.DeclaredRoute
	}{policy.missing, routes})
	if err != nil {
		return fmt.Errorf("seal declared route contract: %w", err)
	}
	digest := sha256.Sum256(encoded)
	policy.revision = "declared-routes-v1:" + hex.EncodeToString(digest[:])
	return nil
}

func (m *declaredRoutesMatcher) finishDeclaredRouteLoad(load *declaredRouteLoad) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, load)
}

func (m *declaredRoutesMatcher) publishDeclaredRoutePolicy(load *declaredRouteLoad, policy declaredRoutePolicy) (declaredRoutePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if load.invalidated {
		return declaredRoutePolicy{}, fmt.Errorf("declared route contract changed during snapshot load")
	}
	m.entries[load.key] = policy
	return policy, nil
}
