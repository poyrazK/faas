package customererrors

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestNormalizeBoundsAndRejects(t *testing.T) {
	account := uuid.NewString()
	base := Event{AccountID: account, Route: "POST /v1/apps/{slug}/deployments", Code: "validation_failed"}
	if _, ok := Normalize(Event{Route: base.Route, Code: base.Code}); ok {
		t.Fatal("event without account was accepted")
	}
	if _, ok := Normalize(Event{AccountID: "not-a-uuid", Route: base.Route, Code: base.Code}); ok {
		t.Fatal("non-UUID account was accepted")
	}
	if _, ok := Normalize(Event{AccountID: account, Code: base.Code}); ok {
		t.Fatal("event without route was accepted")
	}
	if _, ok := Normalize(Event{AccountID: account, Route: base.Route, Code: "Bad Code <script>"}); ok {
		t.Fatal("malformed code was accepted")
	}
	got, ok := Normalize(Event{AccountID: account, Route: strings.Repeat("r", 500), Code: base.Code,
		AppSlug: "Not_A_Slug", RequestID: "has spaces", TraceID: strings.Repeat("0", 32)})
	if !ok {
		t.Fatal("valid event rejected")
	}
	if len(got.Route) != maxRouteBytes || got.AppSlug != "" || got.RequestID != "" || got.TraceID != "" {
		t.Fatalf("optional fields not cleared or route not bounded: %+v", got)
	}
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	got, ok = Normalize(Event{AccountID: account, Route: base.Route, Code: base.Code, AppSlug: "my-app", RequestID: "req_01:abc", TraceID: trace})
	if !ok || got.AppSlug != "my-app" || got.RequestID != "req_01:abc" || got.TraceID != trace {
		t.Fatalf("valid optional fields dropped: %+v", got)
	}
}

func TestRecordBudgetDropsAndNilRecorder(t *testing.T) {
	var nilRecorder *Recorder
	nilRecorder.Record(Event{})
	if nilRecorder.Dropped() != 0 {
		t.Fatal("nil recorder counted drops")
	}
	summary, err := nilRecorder.Summary(context.Background(), uuid.NewString(), time.Now())
	if err != nil || summary.Total != 0 || summary.ByCode == nil || summary.Recent == nil {
		t.Fatalf("nil recorder summary = %+v, %v", summary, err)
	}

	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	r := &Recorder{queue: make(chan Event, QueueSize), now: func() time.Time { return now }, budgets: map[string]int{}}
	noisy, quiet := uuid.NewString(), uuid.NewString()
	for i := 0; i < AccountBudgetPerMinute+5; i++ {
		r.Record(Event{AccountID: noisy, Route: "GET /v1/apps", Code: "rate_limited"})
	}
	r.Record(Event{AccountID: quiet, Route: "GET /v1/apps", Code: "not_found"})
	if got := len(r.queue); got != AccountBudgetPerMinute+1 {
		t.Fatalf("queued=%d, want %d", got, AccountBudgetPerMinute+1)
	}
	if r.Dropped() != 5 {
		t.Fatalf("dropped=%d, want 5", r.Dropped())
	}
	now = now.Add(time.Minute)
	r.Record(Event{AccountID: noisy, Route: "GET /v1/apps", Code: "rate_limited"})
	if got := len(r.queue); got != AccountBudgetPerMinute+2 {
		t.Fatalf("budget did not reset on the next minute: queued=%d", got)
	}
}

// pgRecorder applies the real migration to an isolated schema.
func pgRecorder(t *testing.T) (*Recorder, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for PostgreSQL customer error tests")
	}
	ctx := t.Context()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "customer_errors_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = root.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	})
	migration, err := os.ReadFile("../../../migrations/20261112174514630_customer_api_errors.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(strings.SplitN(string(migration), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0]
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "CREATE TABLE accounts (id uuid PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return New(pool, nil), pool
}

func TestRecorderPersistsSummarizesLooksUpAndPrunes(t *testing.T) {
	r, pool := pgRecorder(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	r.now = func() time.Time { return now }
	account, other := uuid.NewString(), uuid.NewString()
	for _, id := range []string{account, other} {
		if _, err := pool.Exec(ctx, "INSERT INTO accounts(id) VALUES ($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	r.Record(Event{AccountID: account, OccurredAt: now.Add(-3 * time.Minute), Route: "POST /v1/apps/{slug}/deployments", Code: "plan_excess", AppSlug: "shop", RequestID: "req-1"})
	r.Record(Event{AccountID: account, OccurredAt: now.Add(-2 * time.Minute), Route: "POST /v1/apps/{slug}/deployments", Code: "plan_excess", AppSlug: "shop", RequestID: "req-2"})
	r.Record(Event{AccountID: account, OccurredAt: now.Add(-time.Minute), Route: "GET /v1/apps/{slug}", Code: "not_found", TraceID: trace})
	r.Record(Event{AccountID: account, OccurredAt: now.Add(-48 * time.Hour), Route: "GET /v1/apps", Code: "old_error"})
	r.Record(Event{AccountID: account, OccurredAt: now.Add(-15 * 24 * time.Hour), Route: "GET /v1/apps", Code: "expired"})
	r.Record(Event{AccountID: other, OccurredAt: now, Route: "GET /v1/apps", Code: "forbidden", RequestID: "req-other"})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.Run(runCtx); close(done) }()
	cancel()
	<-done

	summary, err := r.Summary(ctx, account, now.Add(-api.CustomerErrorWindow))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 || len(summary.ByCode) != 2 || summary.ByCode[0].Code != "plan_excess" || summary.ByCode[0].Count != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Recent) != 3 || summary.Recent[0].Code != "not_found" || summary.Recent[0].TraceID != trace || summary.Recent[2].RequestID != "req-1" {
		t.Fatalf("recent = %+v", summary.Recent)
	}

	matches, err := r.Lookup(ctx, "req-2")
	if err != nil || len(matches) != 1 || matches[0].Kind != api.ObsLookupRequest || matches[0].AccountID != account || matches[0].AppSlug != "shop" {
		t.Fatalf("request lookup = %+v, %v", matches, err)
	}
	matches, err = r.Lookup(ctx, trace)
	if err != nil || len(matches) != 1 || matches[0].Kind != api.ObsLookupTrace || matches[0].AccountID != account {
		t.Fatalf("trace lookup = %+v, %v", matches, err)
	}

	own, err := r.RequestForAccount(ctx, account, "req-2")
	if err != nil || own == nil || own.Code != "plan_excess" || own.AppSlug != "shop" || own.RequestID != "req-2" {
		t.Fatalf("own request = %+v, %v", own, err)
	}
	if byTrace, err := r.RequestForAccount(ctx, account, trace); err != nil || byTrace == nil || byTrace.Code != "not_found" {
		t.Fatalf("own trace = %+v, %v", byTrace, err)
	}
	if foreign, err := r.RequestForAccount(ctx, account, "req-other"); err != nil || foreign != nil {
		t.Fatalf("another account's request matched: %+v, %v", foreign, err)
	}
	if missing, err := r.RequestForAccount(ctx, account, "bad id with spaces"); err != nil || missing != nil {
		t.Fatalf("malformed id matched: %+v, %v", missing, err)
	}

	// Retention removes the 15-day-old row; the five live rows remain.
	if deleted, err := r.PruneExpired(ctx); err != nil || deleted != 1 {
		t.Fatalf("pruned %d rows, err %v; want 1", deleted, err)
	}
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM customer_api_errors").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 5 {
		t.Fatalf("rows after prune = %d, want 5", rows)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM customer_api_errors").Scan(&rows); err != nil || rows != 4 {
		t.Fatalf("rows after account delete = %d, %v; want cascade to 4", rows, err)
	}
}
