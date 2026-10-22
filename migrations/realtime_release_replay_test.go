//go:build !no_pg

package migrations_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	migrationfiles "github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// Recovery must preserve newer permission vocabularies, retention clocks and
// dry-run state when the release's ledger rows are absent on a populated schema.
func TestRealtimeReleaseReplayPreservesExistingState(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpOnce(ctx, t, pool)
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	var endpoint, group string
	if err := pool.QueryRow(ctx, `INSERT INTO managed_realtime_endpoints
  (app_id,account_id,callback_url,callback_auth_token_sealed)
  VALUES($1,$2,'https://example.test/realtime',''::bytea) RETURNING id::text`, app, account).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO managed_realtime_consumer_groups
  (endpoint_id,channel,name,assigned_sequence,lease_seconds,max_attempts,paused_at,draining,dead_letter_retention_seconds)
  VALUES($1,'jobs','workers',1,30,3,'2026-01-01T00:00:00Z',true,3600) RETURNING id::text`, endpoint).Scan(&group); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO managed_realtime_worker_credentials(group_id,name,token_hash,permissions,expires_at,last_operation)
   VALUES($1,'worker',decode(repeat('ab',32),'hex'),ARRAY['heartbeat','release','drain'],clock_timestamp()+interval '1 hour','release')`,
		`INSERT INTO managed_realtime_consumer_group_messages(group_id,sequence,message,status,attempts,dead_since,first_claim_observed,release_count)
   VALUES($1,1,'{}','dead',2,'2026-01-01T00:00:00Z',true,4)`,
		`INSERT INTO managed_realtime_group_delivery_history(group_id,sequence,event,attempt,fencing_token,failure_code)
   VALUES($1,1,'released',2,1,'retry')`,
		`INSERT INTO managed_realtime_replay_jobs(id,group_id,state,rate,selection,dry_run)
   VALUES(gen_random_uuid(),$1,'checking',1,'[{"sequence":"1"}]',true)`,
	} {
		if _, err := pool.Exec(ctx, query, group); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		for table, query := range map[string]string{
			"managed_realtime_consumer_groups":         "SELECT jsonb_agg(to_jsonb(r))::text FROM managed_realtime_consumer_groups r",
			"managed_realtime_consumer_group_messages": "SELECT jsonb_agg(to_jsonb(r))::text FROM managed_realtime_consumer_group_messages r",
			"managed_realtime_worker_credentials":      "SELECT jsonb_agg(to_jsonb(r))::text FROM managed_realtime_worker_credentials r",
			"managed_realtime_group_delivery_history":  "SELECT jsonb_agg(to_jsonb(r))::text FROM managed_realtime_group_delivery_history r",
			"managed_realtime_replay_jobs":             "SELECT jsonb_agg(to_jsonb(r))::text FROM managed_realtime_replay_jobs r",
		} {
			var json string
			if err := pool.QueryRow(ctx, query).Scan(&json); err != nil {
				t.Fatal(err)
			}
			result[table] = json
		}
		for _, function := range []string{"measure_realtime_group_transition()", "observe_realtime_delivery_history()", "observe_realtime_circuit_failure()"} {
			var definition string
			if err := pool.QueryRow(ctx, `SELECT pg_get_functiondef(to_regprocedure($1))`, function).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			result[function] = definition
		}
		return result
	}
	before := snapshot()
	files, err := migrationfiles.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var versions []int64
	for _, file := range files {
		if !strings.Contains(file.Name(), "_realtime_") || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		version, err := strconv.ParseInt(strings.SplitN(file.Name(), "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if version >= 20261012102700452 && version <= 20261012102700548 {
			versions = append(versions, version)
		}
	}
	if len(versions) != 35 {
		t.Fatalf("release migration cohort: got %d, want 35", len(versions))
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=ANY($1::bigint[])`, versions); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatalf("populated replay %d: %v", attempt, err)
		}
		for key, value := range snapshot() {
			if value != before[key] {
				t.Errorf("replay %d changed %s", attempt, key)
			}
		}
	}
}
