package dbqueryinsights

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestChangesStartsNewBaselines(t *testing.T) {
	start := time.Now().UTC()
	first := Sample{At: start, BootAt: start.Add(-time.Hour), ResetAt: start.Add(-time.Minute),
		Counters: []Counter{{QueryID: "1", UserID: 10, Calls: 10, ExecutionMS: 30}}}
	next := first
	next.At = next.At.Add(Interval)
	next.Counters = []Counter{{QueryID: "1", UserID: 10, Calls: 15, ExecutionMS: 45}}
	rows, ok := Changes(first, next)
	if !ok || len(rows) != 1 || rows[0].Calls != 5 || rows[0].ExecutionMS != 15 {
		t.Fatalf("delta: %+v", rows)
	}
	for _, mutate := range []func(*Sample){
		func(s *Sample) { s.BootAt = start },
		func(s *Sample) { s.ResetAt = start },
		func(s *Sample) { s.Evictions++ },
		func(s *Sample) { s.At = s.At.Add(3 * Interval) },
		func(s *Sample) { s.Counters = []Counter{{QueryID: "1", UserID: 10, Calls: 1}} },
	} {
		s := next
		mutate(&s)
		if rows, ok := Changes(first, s); ok || len(rows) != 0 {
			t.Fatalf("read across a baseline break: %+v", rows)
		}
	}
}

func TestQueryInsightsRanksExecutionTimeAndRecoversFromReset(t *testing.T) {
	dsn := os.Getenv("FAAS_POSTGRES_QUERY_INSIGHTS_DSN")
	if dsn == "" {
		t.Skip("FAAS_POSTGRES_QUERY_INSIGHTS_DSN is unset")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, InstallSQL); err != nil {
		t.Fatal(err)
	}
	source, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	source.Path = "/insights_fixture"
	app, err := pgx.Connect(ctx, source.String())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(ctx)

	before := insightSample(t, ctx, admin)
	const fast = "SELECT pg_sleep(0.003)"
	for range 60 {
		if _, err := app.Exec(ctx, fast); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Exec(ctx, "SELECT pg_sleep(0.05) FROM (VALUES (1)) AS row(n)"); err != nil {
		t.Fatal(err)
	}
	after := insightSample(t, ctx, admin)
	delta, ok := Changes(before, after)
	if !ok || len(delta) != 2 {
		t.Fatalf("unexpected interval delta: %+v", delta)
	}
	const id = "7318ed5e-aa11-4af2-a052-d2db6f4cc1f5"
	if _, err := admin.Exec(ctx, "CREATE TEMP TABLE native_postgres_query_intervals (database_id uuid, started_at timestamptz, ended_at timestamptz, boot_at timestamptz, counters jsonb)"); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "INSERT INTO native_postgres_query_intervals VALUES ($1,$2,$3,$4,$5::jsonb)", id, before.At, after.At, after.BootAt, encoded); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(ctx, RankedSQL, id, before.At.Add(-time.Second), after.At.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		boot  time.Time
		id    string
		calls int64
		ms    float64
	}
	var ranked []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.boot, &r.id, &r.calls, &r.ms); err != nil {
			t.Fatal(err)
		}
		ranked = append(ranked, r)
	}
	rows.Close()
	if rows.Err() != nil || len(ranked) != 2 || ranked[0].calls != 60 || ranked[1].calls != 1 ||
		ranked[0].ms <= ranked[1].ms || ranked[0].ms/60 >= ranked[1].ms {
		t.Fatalf("expected frequent query to rank first and rare query to cost more per call: %+v (%v)", ranked, rows.Err())
	}
	if _, err := admin.Exec(ctx, "SELECT gregale_query_insights.pg_stat_statements_reset()"); err != nil {
		t.Fatal(err)
	}
	baseline := insightSample(t, ctx, admin)
	if rows, ok := Changes(after, baseline); ok || len(rows) > 0 {
		t.Fatalf("reported counters across a reset: %+v", rows)
	}
	if _, err := app.Exec(ctx, fast); err != nil {
		t.Fatal(err)
	}
	delta, ok = Changes(baseline, insightSample(t, ctx, admin))
	if !ok || len(delta) != 1 || delta[0].Calls != 1 {
		t.Fatalf("unexpected new-baseline delta: %+v", delta)
	}
}

func insightSample(t *testing.T, ctx context.Context, admin *pgx.Conn) Sample {
	t.Helper()
	results, err := admin.PgConn().Exec(ctx, SampleSQL("insights_fixture")).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if len(result.Rows) == 1 {
			var sample Sample
			if err := json.Unmarshal(result.Rows[0][0], &sample); err != nil || !sample.Valid() {
				t.Fatalf("invalid sample: %v, %s", err, result.Rows[0][0])
			}
			return sample
		}
	}
	t.Fatal("PostgreSQL returned no query sample")
	return Sample{}
}
