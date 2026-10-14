//go:build !no_pg

package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPAlertsPreservePublishedEdgeActivityMetrics(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	migrateUpOnce(ctx, t, pool)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	// Copy the installed CHECK constraints without tenant foreign keys or
	// triggers, so the test exercises the final migration order directly.
	probe := func(t *testing.T) {
		t.Helper()
		if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS pg_temp.mcp_alert_metric_probe;
CREATE TEMP TABLE mcp_alert_metric_probe (LIKE alert_rules INCLUDING DEFAULTS INCLUDING CONSTRAINTS)`); err != nil {
			t.Fatal(err)
		}
		for _, metric := range []string{
			"edge_rule_hits", "edge_rule_log_hits",
			"mcp_policy_unconverged_gateways", "mcp_policy_unverified",
			"mcp_catalog_drift", "mcp_catalog_monitor_unavailable",
			"mcp_oauth_reauthorization_required", "mcp_oauth_renewal_failed",
			"mcp_oauth_retirement_expiring", "mcp_oauth_issuance_unknown",
			"unrecognized_alert_metric",
		} {
			_, err := conn.Exec(ctx, `INSERT INTO mcp_alert_metric_probe
(account_id,app_id,name,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed,action)
VALUES ('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002',
'metric ordering probe',$1,'gt',1,'1h','https://example.com/hook',$2,'webhook')`, metric, []byte{1})
			if metric != "unrecognized_alert_metric" {
				if err != nil {
					t.Errorf("installed constraints rejected %s: %v", metric, err)
				}
				continue
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Errorf("unknown metric error = %v; want CHECK violation", err)
			}
		}
	}
	t.Run("fresh_schema", probe)

	// Replaying main's already-published widening must retain the later MCP
	// metric families as well as its own edge-rule metrics.
	raw, err := migrations.FS.ReadFile("20261014093127861_alert_rules_edge_rule_activity.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("published edge activity migration has no Down section")
	}
	if _, err := conn.Exec(ctx, up); err != nil {
		t.Fatal("replay published edge activity migration", err)
	}
	t.Run("replay_published_edge_activity", probe)
}
