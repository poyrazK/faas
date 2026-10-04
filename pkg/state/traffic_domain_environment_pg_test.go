//go:build !no_pg

// adr: 531
package state

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedPgScopedDomainLegacyRule(t *testing.T, pool *pgxpool.Pool, rule EdgeRule) {
	t.Helper()
	// Preserve the escaped compiler expansion while avoiding a large Go-side
	// HTML-escaped action buffer merely to seed a legacy database fixture.
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action)
		VALUES($1,$2,$3,$4,'/',true,$5,jsonb_build_object('kind',$5::text,
		'route',CASE WHEN $5='route' THEN jsonb_build_object('target_app_slug',$6::text) ELSE NULL END,
		'headers',CASE WHEN $5='headers' THEN jsonb_build_object('response_headers',jsonb_build_array(jsonb_build_object('name','X-Fallback','action','set','value','shared'))) ELSE NULL END,
		'validate',jsonb_build_object('schema',repeat('&',$7::integer))))`,
		rule.ID, rule.AccountID, rule.AppID, rule.MatchHost, string(rule.Kind),
		func() string {
			if rule.Action.Route != nil {
				return rule.Action.Route.TargetAppSlug
			}
			return ""
		}(), len(rule.Action.Validate.Schema)-2); err != nil {
		t.Fatal(err)
	}
}

func TestPgTrafficScopedDomainVerification(t *testing.T) {
	for _, mode := range trafficScopedDomainModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, project, app, environment := trafficEnvironmentPGFixture(t)
			testTrafficScopedDomainVerification(t, store, account, project, app, environment, mode, func(rule EdgeRule) {
				seedPgScopedDomainLegacyRule(t, pool, rule)
			})
		})
	}
}

func TestPgTrafficScopedDomainOverlayMutation(t *testing.T) {
	store, pool, account, project, app, environment := trafficEnvironmentPGFixture(t)
	testTrafficScopedDomainOverlayMutation(t, store, account, project, app, environment, func(rule EdgeRule) { seedPgScopedDomainLegacyRule(t, pool, rule) })
}
