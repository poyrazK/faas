//go:build !no_pg

package migrations_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-438/449/453/458: replaying the route migration tail must preserve saved
// intent, receipts, history, abort configuration, and pending later events.
func TestRouteMigrationsReplayPreservesData(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	versions := routeTailVersions(t)
	account, app, deployment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	routeReplayExec(t, pool, `INSERT INTO accounts(id, email, plan) VALUES ($1, $2, 'pro')`, account, account+"@example.com")
	routeReplayExec(t, pool, `INSERT INTO apps(id, account_id, slug, ram_mb) VALUES ($1, $2, $3, 128)`, app, account, "route-replay-"+app[:8])
	routeReplayExec(t, pool, `INSERT INTO deployments(id, app_id, image_digest, status) VALUES ($1, $2, $3, 'building')`, deployment, app, "sha256:"+strings.Repeat("a", 64))
	routeReplayExec(t, pool, `INSERT INTO saved_route_requirements(app_id, account_id, revision, sha256, requirements)
		VALUES ($1, $2, 7, $3, '{"version":"2"}')`, app, account, strings.Repeat("a", 64))
	routeReplayExec(t, pool, `INSERT INTO route_policy_receipts(id, account_id, app_id, idempotency_key, request_sha256, receipt)
		VALUES ($1, $2, $3, 'retained', $4, '{"retained":true}')`, uuid.NewString(), account, app, strings.Repeat("a", 64))
	routeReplayExec(t, pool, `INSERT INTO route_check_history(id, deployment_id, app_id, account_id, checked_at, encoded_bytes, entry)
		VALUES ($1, $2, $3, $4, now(), 15, '{"version":"1"}')`, uuid.NewString(), deployment, app, account)
	routeReplayExec(t, pool, `INSERT INTO route_health_gates(app_id, account_id, mode, revision, routes, on_regression)
		VALUES ($1, $2, 'enforce', 9, '[{"method":"POST","path":"/checkout"}]', 'abort')`, app, account)
	routeReplayExec(t, pool, `INSERT INTO route_health_notification_state(deployment_id, app_id, account_id, context_key, status, updated_at)
		VALUES ($1, $2, $3, $4, 'aborted', now())`, deployment, app, account, strings.Repeat("b", 64))
	events := []string{"issue.impact_threshold_reached", "routes.requirements.changed", "routes.health.blocked", "routes.health.resumed", "routes.health.aborted", "routes.monitor.violated", "routes.monitor.recovered"}
	for _, event := range events {
		routeReplayExec(t, pool, `INSERT INTO app_webhook_event_outbox(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
			VALUES ($1, $2, $3, $4, '{}', ARRAY[$5::uuid])`, account, app, event, uuid.NewString(), uuid.NewString())
	}
	routeReplayExec(t, pool, `SELECT enqueue_automatic_route_check($1, $2)`, app, deployment)

	// A drifted ledger can lose the entire tail or just an earlier version.
	// Keep later event rows present while every earlier migration is replayed.
	cases := [][]int64{versions}
	for _, version := range versions {
		cases = append(cases, []int64{version})
	}
	for _, replay := range cases {
		t.Run(strconv.FormatInt(replay[0], 10)+"_"+strconv.Itoa(len(replay)), func(t *testing.T) {
			tag, err := pool.Exec(t.Context(), `DELETE FROM goose_db_version WHERE version_id = ANY($1::bigint[])`, replay)
			if err != nil || tag.RowsAffected() != int64(len(replay)) {
				t.Fatalf("remove ledger rows: %v (%d)", err, tag.RowsAffected())
			}
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatalf("replay with retained data: %v", err)
			}
		})
	}

	var retained bool
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT revision = 7 FROM saved_route_requirements WHERE app_id = $1) AND
		(SELECT receipt = '{"retained":true}'::jsonb FROM route_policy_receipts WHERE app_id = $1) AND
		(SELECT count(*) = 1 FROM route_check_history WHERE deployment_id = $2) AND
		(SELECT mode = 'enforce' AND revision = 9 AND on_regression = 'abort' AND routes->0->>'path' = '/checkout' FROM route_health_gates WHERE app_id = $1) AND
		(SELECT status = 'aborted' FROM route_health_notification_state WHERE deployment_id = $2) AND
		(SELECT count(*) = $3 FROM app_webhook_event_outbox WHERE app_id = $1)`, app, deployment, len(events)).Scan(&retained); err != nil || !retained {
		t.Fatalf("retained route data changed: %t, %v", retained, err)
	}

	var before, after string
	if err := pool.QueryRow(t.Context(), `SELECT request_id::text FROM automatic_route_checks WHERE deployment_id = $1`, deployment).Scan(&before); err != nil {
		t.Fatal(err)
	}
	routeReplayExec(t, pool, `UPDATE saved_route_requirements SET revision = revision + 1 WHERE app_id = $1`, app)
	if err := pool.QueryRow(t.Context(), `SELECT request_id::text FROM automatic_route_checks WHERE deployment_id = $1`, deployment).Scan(&after); err != nil || before == after {
		t.Fatalf("saved intent trigger did not enqueue after replay: %v", err)
	}
}

func routeTailVersions(t *testing.T) []int64 {
	t.Helper()
	names := map[string]bool{
		"route_policy_receipts": true, "saved_route_requirements": true,
		"automatic_route_checks": true, "canary_route_gates": true,
		"route_policy_monitoring": true, "route_check_history": true,
		"route_health_gates": true, "route_health_history": true,
		"route_health_notifications": true, "route_health_automatic_abort": true,
		"route_production_monitoring": true,
	}
	var versions []int64
	for _, migration := range migrations.LoadMigrations(t) {
		_, name, ok := strings.Cut(strings.TrimSuffix(migration.Name, ".sql"), "_")
		if ok && names[name] {
			versions = append(versions, migration.Version)
		}
	}
	if len(versions) != len(names) {
		t.Fatalf("route migration versions = %v, want %d", versions, len(names))
	}
	return versions
}

func routeReplayExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
