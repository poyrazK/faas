//go:build !no_pg

// adr: 20261027163626
package state

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
)

func pgEdgeRuleMatchHostScope(t *testing.T, pool *pgxpool.Pool, id string) pgtype.Text {
	t.Helper()
	var scope pgtype.Text
	if err := pool.QueryRow(t.Context(), `SELECT match_host_scope FROM edge_rules WHERE id=$1::uuid`, id).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	return scope
}

// The trigger fills match_host_scope on every write, and the scope is never
// narrower than what the gateway's matcher accepts: every matched host is the
// scope or ends with "."+scope. A narrower scope would let the per-host
// binding guard miss a route that applies to a changed hostname.
func TestPgEdgeRuleMatchHostScope(t *testing.T) {
	_, pool, account, app := trafficHostPGFixture(t)
	hosts := []string{"api.example.com", "API.example.com", "foo.example.com", "afoo.example.com", "abc.example.com",
		"b.example.com", "x_y.example.com", "promo-1.apps.gregale.dev", "promo-.apps.gregale.dev", "é1.test",
		"example.com", "api.x", "ab", "a1b"}
	for _, tc := range []struct {
		pattern string
		scope   string
		global  bool // NULL: covers only the rule's own app's hosts
	}{
		{pattern: "*", scope: ""},
		{pattern: "api.*", scope: ""},
		{pattern: "*.example.com", scope: "example.com"},
		{pattern: "promo-*.apps.gregale.dev", scope: "apps.gregale.dev"},
		{pattern: "*foo.example.com", scope: "example.com"},
		{pattern: "a?c.example.com", scope: "example.com"},
		{pattern: "[ab].example.com", scope: "example.com"},
		{pattern: `api.\*`, scope: ""},
		{pattern: "API.Example.COM", scope: "api.example.com"},
		{pattern: " api.example.com ", scope: "api.example.com"},
		{pattern: "x_y.example.com", scope: "example.com"},
		{pattern: "é?.test", scope: "test"},
		{pattern: "a*?*b", scope: ""},
		{pattern: "@app", global: true},
		{pattern: "@APP", global: true},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			var id string
			err := pool.QueryRow(t.Context(), `INSERT INTO edge_rules(account_id, app_id, match_host, kind, action)
VALUES ($1::uuid, $2::uuid, $3, 'headers', '{}') RETURNING id`, account.ID, app.ID, tc.pattern).Scan(&id)
			if err != nil {
				t.Fatal(err)
			}
			scope := pgEdgeRuleMatchHostScope(t, pool, id)
			if tc.global {
				if scope.Valid {
					t.Fatalf("scope = %q, want NULL for the app-wide selector", scope.String)
				}
				return
			}
			if !scope.Valid || scope.String != tc.scope {
				t.Fatalf("scope = %+v, want %q", scope, tc.scope)
			}
			for _, host := range hosts {
				lower := strings.ToLower(host)
				if api.EdgeRuleHostMatches(tc.pattern, host) && scope.String != "" && lower != scope.String && !strings.HasSuffix(lower, "."+scope.String) {
					t.Errorf("%q matches %q but lies outside scope %q", tc.pattern, host, scope.String)
				}
			}
		})
	}

	// A changed match_host is rescoped, and a direct write to the scope is
	// recomputed rather than kept.
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO edge_rules(account_id, app_id, match_host, kind, action)
VALUES ($1::uuid, $2::uuid, '*', 'headers', '{}') RETURNING id`, account.ID, app.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, update := range []string{
		`UPDATE edge_rules SET match_host='promo-*.apps.gregale.dev' WHERE id=$1::uuid`,
		`UPDATE edge_rules SET match_host_scope='bogus' WHERE id=$1::uuid`,
	} {
		if _, err := pool.Exec(t.Context(), update, id); err != nil {
			t.Fatal(err)
		}
		if scope := pgEdgeRuleMatchHostScope(t, pool, id); scope.String != "apps.gregale.dev" {
			t.Fatalf("after %q: scope = %+v, want apps.gregale.dev", update, scope)
		}
	}
}
