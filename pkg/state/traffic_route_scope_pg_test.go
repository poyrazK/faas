//go:build !no_pg

// adr: 20261027163626
package state

import (
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
)

// trafficAnalyzerSelects mirrors the analyzer's two selector branches: the
// literal pattern, and its SQL LIKE translation (* -> %, ? -> _, \ escapes).
func trafficAnalyzerSelects(pattern, host string) bool {
	if pattern == host {
		return true
	}
	translated := []rune(strings.NewReplacer("*", "%", "?", "_").Replace(pattern))
	var expr strings.Builder
	expr.WriteString("^(?s)")
	for i := 0; i < len(translated); i++ {
		switch translated[i] {
		case '%':
			expr.WriteString(".*")
		case '_':
			expr.WriteString(".")
		case '\\':
			if i++; i == len(translated) {
				return false
			}
			expr.WriteString(regexp.QuoteMeta(string(translated[i])))
		default:
			expr.WriteString(regexp.QuoteMeta(string(translated[i])))
		}
	}
	expr.WriteString("$")
	return regexp.MustCompile(expr.String()).MatchString(host)
}

func readPgTrafficRouteCandidates(t *testing.T, pool *pgxpool.Pool, scope *trafficRouteScope) ([]trafficHostGroup, []pgtype.UUID) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	view, err := readTrafficRouteCandidates(t.Context(), tx, ".apps.example.test", scope)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := readTrafficRouteOwners(t.Context(), tx, scope)
	if err != nil {
		t.Fatal(err)
	}
	return view.Groups, owners
}

// A scoped read keeps every route the analyzer can select at a changed host,
// so the scoped verdict at that host sums the same routes as the fleet read.
func TestPgTrafficRouteCandidatesKeepEveryMatchingRoute(t *testing.T) {
	store, pool, first, firstApp := trafficHostPGFixture(t)
	second, err := store.CreateAccount(t.Context(), "route-scope@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	secondApp, err := store.CreateApp(t.Context(), App{AccountID: second.ID, Slug: "route-scope", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	patterns := []string{"*", "api.*", "*.example.com", "promo-*.apps.example.test", "*foo.example.com",
		"a?c.example.com", "[ab].example.com", `api.\*`, "API.Example.COM", "api.example.com", "x_y.example.com",
		"a%.example.com", "shop.example.org", "*.example.org", "deep.*.example.com", "é?.test", "a*?*b", "@app",
		`a\.b.example.com`, "*.apps.example.test"}
	hosts := []string{"api.example.com", "abc.example.com", "afoo.example.com", "promo-1.apps.example.test",
		"shop.example.org", "x.example.org", "deep.q.example.com", "é1.test", "ab", "axb", "x_y.example.com",
		"xay.example.com", "a.b.example.com", "api.*", "web.apps.example.test", "example.com"}
	random := rand.New(rand.NewSource(20261028))
	label := func() string {
		return []string{"a", "b", "ab", "api", "x_y", "é", "shop", "apps"}[random.Intn(8)]
	}
	for range 60 {
		parts := []string{label(), label(), []string{"example.com", "example.org", "apps.example.test", "test"}[random.Intn(4)]}
		host := strings.Join(parts, ".")
		hosts = append(hosts, host)
		pattern := []rune(host)
		for range 1 + random.Intn(2) {
			pattern[random.Intn(len(pattern))] = []rune("*?%_")[random.Intn(4)]
		}
		patterns = append(patterns, string(pattern))
	}
	for i, pattern := range patterns {
		account, app := first.ID, firstApp.ID
		if i%2 == 1 {
			account, app = second.ID, secondApp.ID
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id, app_id, match_host, kind, action)
VALUES ($1::uuid, $2::uuid, $3, 'route', '{}')`, account, app, pattern); err != nil {
			t.Fatal(err)
		}
	}
	all, allOwners := readPgTrafficRouteCandidates(t, pool, nil)
	if len(all) == 0 || len(allOwners) != 2 {
		t.Fatalf("fleet read: %d groups, %d owners", len(all), len(allOwners))
	}
	check := func(t *testing.T, scope *trafficRouteScope, samples []string) {
		t.Helper()
		groups, owners := readPgTrafficRouteCandidates(t, pool, scope)
		for _, group := range all {
			for _, host := range samples {
				if trafficAnalyzerSelects(group.Pattern, host) && !slices.Contains(groups, group) {
					t.Errorf("route %q selects %q but the scoped read dropped it", group.Pattern, host)
				}
			}
		}
		if len(groups) > len(all) || len(owners) > len(allOwners) {
			t.Fatalf("scoped read is larger than the fleet read")
		}
	}
	for _, host := range hosts {
		t.Run("host "+host, func(t *testing.T) { check(t, newTrafficRouteScope([]string{host}, nil), []string{host}) })
	}
	for _, subtree := range []string{"example.com", "apps.example.test", "example.org", "test"} {
		samples := []string{"x." + subtree, "a.b." + subtree, "api." + subtree, "promo-9." + subtree}
		for _, host := range hosts {
			if strings.HasSuffix(host, "."+subtree) {
				samples = append(samples, host)
			}
		}
		t.Run("subtree "+subtree, func(t *testing.T) { check(t, newTrafficRouteScope(nil, []string{subtree}), samples) })
	}
	// The scope actually narrows the read: an unrelated host keeps only the
	// routes whose pattern can match any hostname.
	narrow, _ := readPgTrafficRouteCandidates(t, pool, newTrafficRouteScope([]string{"unrelated.invalid"}, nil))
	if len(narrow) >= len(all) {
		t.Fatalf("unrelated host read %d of %d groups", len(narrow), len(all))
	}
	t.Logf("fleet groups %d, unrelated-host groups %d", len(all), len(narrow))
}
