// adr: 20261027163626 — route candidates for the scoped binding guard.
package state

import (
	"context"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// trafficRouteScope limits global route reads to the routes that can match
// the names a binding write may change. A route's match_host_scope bounds
// every hostname its pattern matches: the scope itself or a strict
// descendant of it, where the empty scope bounds nothing. So a route can match
// exact host h only if its scope is h, a proper ancestor of h, or empty; and
// a host inside subtree s only if its scope is s, an ancestor of s, a
// descendant of s, or empty. A nil scope reads every route.
type trafficRouteScope struct {
	// Names are the exact hosts and subtree roots with all their ancestors.
	Names []string
	// Trees are the subtree roots, whose descendant scopes also match.
	Trees []string
}

// newTrafficRouteScope returns nil (every route) when a subtree is every
// hostname.
func newTrafficRouteScope(hosts, subtrees []string) *trafficRouteScope {
	names, trees := map[string]bool{}, map[string]bool{}
	add := func(name string) {
		for tail := name; ; tail = tail[strings.IndexByte(tail, '.')+1:] {
			names[tail] = true
			if !strings.ContainsRune(tail, '.') {
				return
			}
		}
	}
	for _, host := range hosts {
		if host = normalizeTrafficLockName(host); host != "" {
			add(host)
		}
	}
	for _, subtree := range subtrees {
		subtree = normalizeTrafficLockName(subtree)
		if subtree == "" {
			return nil
		}
		add(subtree)
		trees[subtree] = true
	}
	scope := &trafficRouteScope{Names: make([]string, 0, len(names)), Trees: make([]string, 0, len(trees))}
	for name := range names {
		scope.Names = append(scope.Names, name)
	}
	for tree := range trees {
		scope.Trees = append(scope.Trees, tree)
	}
	sort.Strings(scope.Names)
	sort.Strings(scope.Trees)
	return scope
}

func (s *trafficRouteScope) params() (names, trees []string) {
	if s == nil {
		return nil, nil
	}
	// A non-nil empty array still scopes: it admits only empty-scope routes.
	return append([]string{}, s.Names...), append([]string{}, s.Trees...)
}

// readTrafficRouteOwners lists the accounts with an enabled route in scope.
func readTrafficRouteOwners(ctx context.Context, reader sqlc.DBTX, scope *trafficRouteScope) ([]pgtype.UUID, error) {
	names, trees := scope.params()
	return sqlc.New().ReadTrafficGlobalRouteAccounts(ctx, reader, sqlc.ReadTrafficGlobalRouteAccountsParams{
		RouteScopeNames: names, RouteScopeTrees: trees, MaxInputs: api.TrafficPolicyMaxAnalysisInputs,
	})
}

// readTrafficRouteCandidates is the global route analysis limited to scope.
func readTrafficRouteCandidates(ctx context.Context, tx pgx.Tx, appsSuffix string, scope *trafficRouteScope) (trafficHostAnalysis, error) {
	return readTrafficHostAnalysisScoped(ctx, tx, pgtype.UUID{}, appsSuffix, scope)
}
