// adr: 20261029190349 — a scoped binding write reads only the claims and
// reservations of its scope.
package state

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// trafficScopeLookup is how a scope reaches claims and reservations: the
// exact names of its hosts and of every wildcard ancestor of its hosts and
// subtrees, the reversed '.'+subtree prefixes of what lies inside them, the
// app slugs and alias apps its names under the apps suffix imply, and whether
// a subtree covers every app hostname. fleet means a subtree covers every
// hostname, so only a fleet-wide read is complete.
type trafficScopeLookup struct {
	names, trees, slugs []string
	aliasApps           []pgtype.UUID
	allAppHosts, fleet  bool
}

func newTrafficScopeLookup(scope trafficBindingScope, appsSuffix string) trafficScopeLookup {
	var lookup trafficScopeLookup
	names, slugs := map[string]bool{}, map[string]bool{}
	ancestors := func(name string) {
		for tail := name; strings.ContainsRune(tail, '.'); {
			tail = tail[strings.IndexByte(tail, '.')+1:]
			names["*."+tail] = true
		}
	}
	appsDomain := strings.TrimPrefix(appsSuffix, ".")
	for _, host := range scope.Hosts {
		names[host] = true
		ancestors(host)
		if label, under := strings.CutSuffix(host, appsSuffix); appsSuffix != "" && under && label != "" && !strings.Contains(label, ".") {
			slugs[label] = true
			if app, ok := trafficAliasHostApp(label); ok {
				lookup.aliasApps = append(lookup.aliasApps, app)
			}
		}
	}
	for _, tree := range scope.Subtrees {
		if tree == "" {
			lookup.fleet = true
			continue
		}
		ancestors(tree)
		lookup.trees = append(lookup.trees, reverseTrafficName("."+tree))
		if appsDomain != "" && (tree == appsDomain || strings.HasSuffix(appsDomain, "."+tree)) {
			lookup.allAppHosts = true
		}
	}
	lookup.names, lookup.slugs = sortedTrafficKeys(names), sortedTrafficKeys(slugs)
	sort.Strings(lookup.trees)
	return lookup
}

// trafficAliasHostApp reads the app of an alias label: tag-<name>-<app id
// without dashes>.
func trafficAliasHostApp(label string) (pgtype.UUID, bool) {
	cut := strings.LastIndexByte(label, '-')
	hex := label[cut+1:]
	if !strings.HasPrefix(label, "tag-") || cut < len("tag-") || len(hex) != 32 {
		return pgtype.UUID{}, false
	}
	var app pgtype.UUID
	if err := app.Scan(hex[:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:]); err != nil {
		return pgtype.UUID{}, false
	}
	return app, true
}

func reverseTrafficName(name string) string {
	runes := []rune(name)
	slices.Reverse(runes)
	return string(runes)
}

func sortedTrafficKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// readTrafficBindingClaimsScoped reads the claims of accounts and their
// redirects to app, and the claims lookup names or encloses; a fleet lookup
// reads every claim.
func readTrafficBindingClaimsScoped(ctx context.Context, reader sqlc.DBTX, appsSuffix string, accounts []string, appID string, lookup trafficScopeLookup) (trafficBindingClaims, error) {
	if lookup.fleet {
		return readTrafficBindingClaims(ctx, reader, appsSuffix)
	}
	owners := make([]pgtype.UUID, 0, len(accounts))
	for _, account := range accounts {
		owners = append(owners, uuidToPgtype(account))
	}
	app := pgtype.UUID{}
	if appID != "" {
		app = uuidToPgtype(appID)
	}
	return readBoundedTrafficBindingClaims(ctx, func(bounded context.Context) (sqlc.ReadTrafficBindingClaimsRow, error) {
		row, err := sqlc.New().ReadTrafficBindingClaimsScoped(bounded, reader, sqlc.ReadTrafficBindingClaimsScopedParams{
			MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes, AppsSuffix: appsSuffix,
			Accounts: owners, AppID: app, Names: append([]string{}, lookup.names...), Trees: append([]string{}, lookup.trees...),
			AllAppHosts: lookup.allAppHosts, AliasApps: append([]pgtype.UUID{}, lookup.aliasApps...), Slugs: append([]string{}, lookup.slugs...),
		})
		return sqlc.ReadTrafficBindingClaimsRow(row), err
	})
}

// readTrafficBindingScopeClaims declares a scoped binding write's scope from
// its account's claims, then reads the claims that scope can see.
func readTrafficBindingScopeClaims(ctx context.Context, reader sqlc.DBTX, account string, requested []string, appID, appsSuffix string) (trafficBindingClaims, trafficBindingScope, trafficScopeLookup, error) {
	var named trafficBindingScope
	for _, host := range requested {
		named.add(host)
	}
	named.normalize()
	own, err := readTrafficBindingClaimsScoped(ctx, reader, appsSuffix, []string{account}, appID, newTrafficScopeLookup(named, appsSuffix))
	if err != nil {
		return trafficBindingClaims{}, trafficBindingScope{}, trafficScopeLookup{}, err
	}
	scope, err := declareTrafficBindingScope(ctx, reader, own, account, requested, appID, appsSuffix)
	if err != nil {
		return trafficBindingClaims{}, trafficBindingScope{}, trafficScopeLookup{}, err
	}
	lookup := newTrafficScopeLookup(scope, appsSuffix)
	claims, err := readTrafficBindingClaimsScoped(ctx, reader, appsSuffix, []string{account}, appID, lookup)
	return claims, scope, lookup, err
}

// trafficReservationParams scopes the global view's reservations to lookup;
// nil keeps every reservation in the fleet.
func trafficReservationParams(params *sqlc.ReadTrafficHostAnalysisParams, lookup *trafficScopeLookup) {
	if lookup == nil || lookup.fleet {
		return
	}
	params.ReservationNames = append([]string{}, lookup.names...)
	params.ReservationTrees = append([]string{}, lookup.trees...)
	params.ReservationSlugs = append([]string{}, lookup.slugs...)
	params.ReservationAllApps = lookup.allAppHosts
}
