package attention

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// The rules read only these columns; the real migration creates the signal
// table.
const fixtureDDL = `
CREATE TABLE accounts (id uuid PRIMARY KEY);
CREATE TABLE apps (id uuid PRIMARY KEY, account_id uuid NOT NULL, slug text NOT NULL, status text NOT NULL DEFAULT 'active');
CREATE TABLE deployments (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), app_id uuid NOT NULL, status text NOT NULL,
  created_at timestamptz NOT NULL, deleted_at timestamptz, error text, error_code text);
CREATE TABLE custom_domains (domain text PRIMARY KEY, app_id uuid NOT NULL, cert_status text NOT NULL DEFAULT 'pending',
  cert_failed_at timestamptz, cert_last_error text, verified_at timestamptz, verification_expires_at timestamptz NOT NULL);
CREATE TABLE customer_api_errors (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, account_id uuid NOT NULL,
  occurred_at timestamptz NOT NULL, code text NOT NULL);
CREATE TABLE events (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, at timestamptz NOT NULL, kind text NOT NULL, data jsonb);
CREATE TABLE jobs (id uuid PRIMARY KEY, account_id uuid NOT NULL, name text NOT NULL, status text NOT NULL DEFAULT 'active');
CREATE TABLE job_runs (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), job_id uuid NOT NULL, trigger_kind text NOT NULL,
  aggregate_status text NOT NULL, finished_at timestamptz);
CREATE TABLE billing_payment_failure_receipts (account_id uuid NOT NULL, event_type text NOT NULL, processed_at timestamptz NOT NULL);
CREATE TABLE billing_payment_recovery_receipts (account_id uuid NOT NULL, processed_at timestamptz NOT NULL);
`

// attentionMigrations create and widen the signal table.
var attentionMigrations = []string{
	"../../../migrations/20261113122715352_customer_attention_signals.sql",
	"../../../migrations/20261113152905644_customer_attention_outcomes.sql",
}

func pgEvaluator(t *testing.T) (*Evaluator, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for PostgreSQL attention tests")
	}
	ctx := t.Context()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "attention_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	ddls := []string{fixtureDDL}
	for _, path := range attentionMigrations {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ddls = append(ddls, strings.SplitN(strings.SplitN(string(migration), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0])
	}
	for _, ddl := range ddls {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	return New(pool, nil, nil), pool
}

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	now  time.Time
}

func (f fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f fixture) account() string {
	id := uuid.NewString()
	f.exec("INSERT INTO accounts(id) VALUES ($1)", id)
	return id
}

func (f fixture) app(account, slug string) string {
	id := uuid.NewString()
	f.exec("INSERT INTO apps(id, account_id, slug) VALUES ($1, $2, $3)", id, account, slug)
	return id
}

func (f fixture) deploy(app, status string, ago time.Duration, code string) {
	f.exec("INSERT INTO deployments(app_id, status, created_at, error_code, error) VALUES ($1, $2, $3, $4, 'build failed')",
		app, status, f.now.Add(-ago), code)
}

func (f fixture) refusals(account, code string, n int, ago time.Duration) {
	for i := 0; i < n; i++ {
		f.exec("INSERT INTO customer_api_errors(account_id, occurred_at, code) VALUES ($1, $2, $3)", account, f.now.Add(-ago), code)
	}
}

func find(signals []api.ObsAttentionSignal, account, kind, subject string) *api.ObsAttentionSignal {
	for i := range signals {
		if signals[i].AccountID == account && signals[i].Kind == kind && signals[i].Subject == subject {
			return &signals[i]
		}
	}
	return nil
}

func TestEvaluateOpensRefreshesDismissesAndResolves(t *testing.T) {
	e, pool := pgEvaluator(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	e.Now = func() time.Time { return now }
	f := fixture{t: t, pool: pool, now: now}

	stuck, healthy, recovered := f.account(), f.account(), f.account()
	shop := f.app(stuck, "shop")
	for _, ago := range []time.Duration{3 * time.Hour, 2 * time.Hour, time.Hour} {
		f.deploy(shop, "failed", ago, "build_failed")
	}
	// Two failures stay below the threshold.
	blog := f.app(healthy, "blog")
	f.deploy(blog, "failed", 2*time.Hour, "build_failed")
	f.deploy(blog, "failed", time.Hour, "build_failed")
	// Three failures, then a live deployment: not stuck.
	api1 := f.app(recovered, "api")
	for _, ago := range []time.Duration{5 * time.Hour, 4 * time.Hour, 3 * time.Hour} {
		f.deploy(api1, "failed", ago, "oom")
	}
	f.deploy(api1, "live", time.Hour, "")
	// Failures older than the window do not count.
	old := f.app(healthy, "old")
	for _, ago := range []time.Duration{30 * time.Hour, 29 * time.Hour, 28 * time.Hour} {
		f.deploy(old, "failed", ago, "build_failed")
	}

	f.refusals(stuck, "plan_excess", api.AttentionRefusedBurst, 10*time.Minute)
	f.refusals(healthy, "plan_excess", api.AttentionRefusedBurst-1, 10*time.Minute)
	f.refusals(healthy, api.CodeNotFound, 50, 10*time.Minute)
	f.refusals(recovered, "plan_excess", api.AttentionRefusedBurst, 2*time.Hour)

	f.exec("INSERT INTO custom_domains(domain, app_id, cert_status, cert_failed_at, cert_last_error, verified_at, verification_expires_at) VALUES ($1, $2, 'failed', $3, 'CAA forbids', $3, $4)",
		"www.shop.example", shop, now.Add(-2*time.Hour), now.Add(5*24*time.Hour))
	f.exec("INSERT INTO custom_domains(domain, app_id, cert_status, cert_failed_at, verified_at, verification_expires_at) VALUES ($1, $2, 'failed', $3, $3, $4)",
		"fresh.shop.example", shop, now.Add(-10*time.Minute), now.Add(5*24*time.Hour))
	f.exec("INSERT INTO custom_domains(domain, app_id, verification_expires_at) VALUES ($1, $2, $3)",
		"new.blog.example", blog, now.Add(7*24*time.Hour-2*time.Hour))
	f.exec("INSERT INTO custom_domains(domain, app_id, verification_expires_at) VALUES ($1, $2, $3)",
		"slow.blog.example", blog, now.Add(5*24*time.Hour))

	result, err := e.Evaluate(ctx)
	if err != nil || result.Skipped {
		t.Fatalf("evaluate = %+v, %v", result, err)
	}
	if result.Opened != 4 || result.Refreshed != 0 || result.Resolved != 0 {
		t.Fatalf("first evaluation = %+v; want 4 opened", result)
	}
	if result.OpenByKind[api.AttentionKindDeployFailing] != 1 || result.OpenByKind[api.AttentionKindRefusedBurst] != 1 || result.OpenByKind[api.AttentionKindDomainStuck] != 2 {
		t.Fatalf("open by kind = %v", result.OpenByKind)
	}
	signals, err := e.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	deploy := find(signals, stuck, api.AttentionKindDeployFailing, "shop")
	if deploy == nil || deploy.Observed != 3 || deploy.Detail != "build_failed" {
		t.Fatalf("deploy_failing = %+v in %+v", deploy, signals)
	}
	if s := find(signals, stuck, api.AttentionKindRefusedBurst, "plan_excess"); s == nil || s.Observed != int64(api.AttentionRefusedBurst) {
		t.Fatalf("refused_burst = %+v", s)
	}
	if s := find(signals, stuck, api.AttentionKindDomainStuck, "www.shop.example"); s == nil || !strings.Contains(s.Detail, "CAA forbids") {
		t.Fatalf("failed domain = %+v", s)
	}
	if s := find(signals, healthy, api.AttentionKindDomainStuck, "slow.blog.example"); s == nil || s.Detail != "domain not verified" {
		t.Fatalf("unverified domain = %+v", s)
	}
	for _, missing := range [][3]string{{healthy, api.AttentionKindDeployFailing, "blog"}, {recovered, api.AttentionKindDeployFailing, "api"},
		{healthy, api.AttentionKindDeployFailing, "old"}, {healthy, api.AttentionKindRefusedBurst, "plan_excess"},
		{healthy, api.AttentionKindRefusedBurst, api.CodeNotFound}, {recovered, api.AttentionKindRefusedBurst, "plan_excess"},
		{stuck, api.AttentionKindDomainStuck, "fresh.shop.example"}, {healthy, api.AttentionKindDomainStuck, "new.blog.example"}} {
		if s := find(signals, missing[0], missing[1], missing[2]); s != nil {
			t.Errorf("unexpected signal %+v", s)
		}
	}

	// A second evaluation refreshes rather than duplicating.
	f.deploy(shop, "failed", 30*time.Minute, "image_too_large")
	now = now.Add(5 * time.Minute)
	result, err = e.Evaluate(ctx)
	if err != nil || result.Opened != 0 || result.Refreshed != 4 || result.Resolved != 0 {
		t.Fatalf("second evaluation = %+v, %v", result, err)
	}
	signals, _ = e.List(ctx, Filter{AccountID: stuck, Kind: api.AttentionKindDeployFailing})
	if len(signals) != 1 || signals[0].Observed != 4 || signals[0].Detail != "image_too_large" || !signals[0].LastSeenAt.Equal(now) {
		t.Fatalf("refreshed = %+v", signals)
	}

	// Dismissing keeps the signal quiet while its condition holds.
	staff := f.account()
	if err := e.Dismiss(ctx, signals[0].ID, staff, "contacted", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("contacted is not a dismiss outcome: %v", err)
	}
	if err := e.Dismiss(ctx, signals[0].ID, staff, api.AttentionOutcomeNotNeeded, strings.Repeat("x", api.AttentionDismissReasonMax+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long note err = %v", err)
	}
	if err := e.Dismiss(ctx, signals[0].ID, staff, api.AttentionOutcomeAlreadyInTouch, "  customer knows, fixing Dockerfile "); err != nil {
		t.Fatal(err)
	}
	if err := e.Dismiss(ctx, signals[0].ID, staff, api.AttentionOutcomeNotNeeded, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second dismiss err = %v", err)
	}
	now = now.Add(5 * time.Minute)
	if result, err = e.Evaluate(ctx); err != nil || result.Opened != 0 || result.OpenByKind[api.AttentionKindDeployFailing] != 0 {
		t.Fatalf("after dismiss = %+v, %v", result, err)
	}
	signals, _ = e.List(ctx, Filter{AccountID: stuck, Kind: api.AttentionKindDeployFailing})
	if len(signals) != 1 || signals[0].State != api.AttentionStateDismissed || signals[0].DismissReason != "customer knows, fixing Dockerfile" || signals[0].DismissedBy != staff ||
		signals[0].Outcome != api.AttentionOutcomeAlreadyInTouch {
		t.Fatalf("dismissed = %+v", signals)
	}

	// A successful deployment resolves it; a later failure streak opens a new one.
	f.deploy(shop, "live", time.Minute, "")
	now = now.Add(5 * time.Minute)
	if result, err = e.Evaluate(ctx); err != nil || result.Resolved != 1 {
		t.Fatalf("after live = %+v, %v", result, err)
	}
	resolved, _ := e.List(ctx, Filter{AccountID: stuck, Kind: api.AttentionKindDeployFailing, States: []string{api.AttentionStateResolved}})
	if len(resolved) != 1 || resolved[0].ResolvedAt == nil || resolved[0].Outcome != api.AttentionOutcomeAlreadyInTouch {
		t.Fatalf("resolved = %+v", resolved)
	}

	// Another replica holding the lock makes this evaluation a no-op.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", evaluateLockKey); err != nil {
		t.Fatal(err)
	}
	if result, err := e.Evaluate(ctx); err != nil || !result.Skipped {
		t.Fatalf("locked evaluation = %+v, %v", result, err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", evaluateLockKey); err != nil {
		t.Fatal(err)
	}

	// Resolved signals are pruned after the retention period.
	now = now.Add(api.AttentionResolvedRetention + time.Hour)
	if result, err = e.Evaluate(ctx); err != nil || result.Pruned < 1 {
		t.Fatalf("prune = %+v, %v", result, err)
	}
}

func TestNilEvaluatorAndBound(t *testing.T) {
	var e *Evaluator
	if r, err := e.Evaluate(context.Background()); err != nil || r.Opened != 0 {
		t.Fatal("nil evaluator evaluated")
	}
	if list, err := e.List(context.Background(), Filter{}); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("nil list = %v %v", list, err)
	}
	if e.Dismiss(context.Background(), 1, uuid.NewString(), api.AttentionOutcomeNotNeeded, "") == nil {
		t.Fatal("nil dismiss succeeded")
	}
	if e.Contacted(context.Background(), 1, uuid.NewString(), uuid.NewString()) == nil {
		t.Fatal("nil contacted succeeded")
	}
	if _, err := e.Get(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil get = %v", err)
	}
	if s, err := e.Summary(context.Background(), time.Now()); err != nil || s == nil {
		t.Fatalf("nil summary = %v %v", s, err)
	}
	if got := bound("ééé", 3); got != "é" {
		t.Fatalf("bound split a rune: %q", got)
	}
}
