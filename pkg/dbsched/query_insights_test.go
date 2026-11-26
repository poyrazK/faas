package dbsched

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
)

func TestQueryInsightsKeepsParkedHistoryAndFencesOldSamples(t *testing.T) {
	dsn := os.Getenv("FAAS_POSTGRES_QUERY_INSIGHTS_DSN")
	if dsn == "" {
		t.Skip("FAAS_POSTGRES_QUERY_INSIGHTS_DSN is unset")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "test_insights_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE database_vms (
		database_id uuid PRIMARY KEY, state text NOT NULL, node_id text NOT NULL,
		state_changed_at timestamptz NOT NULL, held boolean NOT NULL DEFAULT false,
		replicated boolean NOT NULL DEFAULT false, standby_node text, move_node text
	)`); err != nil {
		t.Fatal(err)
	}
	ddl, err := migrations.FS.ReadFile("20261126083649034_native_postgres_query_insights.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(ddl), "-- +goose Down")
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, up)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	id, parked := uuid.NewString(), uuid.NewString()
	at := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, "INSERT INTO database_vms (database_id,state,node_id,state_changed_at) VALUES ($1,'running','node-a',$3),($2,'parked','node-a',$3)", id, parked, at); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO native_postgres_query_settings (database_id,database_name,enabled,updated_at) VALUES ($1,'orders',true,$3),($2,'orders',true,$3)", id, parked, at); err != nil {
		t.Fatal(err)
	}
	store := &PgStore{pool: pool}
	claims, err := store.claimQuerySamples(ctx, at)
	if err != nil || len(claims) != 1 || claims[0].id != id {
		t.Fatalf("claim: %+v, %v", claims, err)
	}
	first := dbqueryinsights.Sample{At: at, BootAt: at.Add(-time.Minute),
		Counters: []dbqueryinsights.Counter{{QueryID: "42", UserID: 10, Calls: 20, ExecutionMS: 100}}}
	if err := store.recordQuerySample(ctx, claims[0], first); err != nil {
		t.Fatal(err)
	}
	next := first
	next.At = at.Add(dbqueryinsights.Interval)
	next.Counters = []dbqueryinsights.Counter{{QueryID: "42", UserID: 10, Calls: 30, ExecutionMS: 150}}
	if err := store.recordQuerySample(ctx, claims[0], next); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE database_vms SET state='parked',state_changed_at=$2 WHERE database_id=$1", id, next.At.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	closed := next
	closed.At = next.At.Add(dbqueryinsights.Interval)
	if err := store.recordQuerySample(ctx, claims[0], closed); err != nil {
		t.Fatal(err)
	}
	if due, err := store.claimQuerySamples(ctx, closed.At); err != nil || len(due) != 0 {
		t.Fatalf("sampled parked databases: %+v, %v", due, err)
	}
	var atRead time.Time
	var query string
	var calls int64
	var ms float64
	if err := pool.QueryRow(ctx, dbqueryinsights.RankedSQL, id, at, closed.At).Scan(&atRead, &query, &calls, &ms); err != nil ||
		!atRead.Equal(first.BootAt) || query != "42" || calls != 10 || ms != 50 {
		t.Fatalf("parked history: %v %s %d %v, %v", atRead, query, calls, ms, err)
	}
}
