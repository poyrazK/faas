//go:build !no_pg

// adr: 20261029150627 — traffic rule writes no longer take the fleet
// capacity guard.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
)

// holdCapacityStatement opens a transaction holding the lock every capacity
// trigger takes, as an in-flight instance or deployment write does.
func holdCapacityStatement(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM service_capacity_policy WHERE singleton FOR SHARE`); err != nil {
		t.Fatal(err)
	}
	return func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }
}

// Every rule-only writer commits beside an in-flight capacity statement;
// a writer that keeps the guard waits for it.
func TestPgTrafficRuleWritesSkipCapacityGuard(t *testing.T) {
	store, pool, account, project, app, environment := trafficEnvironmentPGFixture(t)
	limits := api.MustLimitsFor(account.Plan)
	release := holdCapacityStatement(t, pool)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	route, err := store.CreateEdgeRule(ctx, trafficHostRule(account, app, "rule-only.example.test"))
	if err != nil {
		t.Fatalf("route create: %v", err)
	}
	headers := trafficHostRule(account, app, "headers.example.test")
	headers.Kind, headers.Action = EdgeRuleKindHeaders, EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}}
	if _, err := store.CreateEdgeRuleIfUnderQuota(ctx, headers, limits); err != nil {
		t.Fatalf("quota create: %v", err)
	}
	enabled := false
	if _, err := store.UpdateEdgeRule(ctx, route.ID, UpdateEdgeRuleParams{Enabled: &enabled}); err != nil {
		t.Fatalf("update: %v", err)
	}
	versions, err := store.ListEdgeRuleSetVersions(ctx, app.ID, 2)
	if err != nil || len(versions) < 2 {
		t.Fatalf("versions=%v err=%v", versions, err)
	}
	if _, err := store.RestoreEdgeRuleSetVersion(ctx, app.ID, versions[1].Version, limits); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	preset, err := store.CreateCorsPresetIfUnderQuota(ctx, CorsPreset{AccountID: account.ID, Name: "rule-only",
		AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}}, limits)
	if err != nil {
		t.Fatalf("preset create: %v", err)
	}
	preset.AllowMethods = []string{"GET", "POST"}
	if _, err := store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset); err != nil {
		t.Fatalf("preset update: %v", err)
	}
	if _, err := store.PutProjectEnvironmentEdgePolicy(ctx, ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID,
		AppID: app.ID, EnvironmentSlug: environment.Slug, Rules: []ProjectEnvironmentEdgeRule{}}); err != nil {
		t.Fatalf("environment edge policy: %v", err)
	}

	waiting, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	if _, err := store.CreateApp(waiting, App{AccountID: account.ID, Slug: "capacity-guarded", Status: AppActive}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("app create ran beside a capacity statement: %v", err)
	}
	release()
	if _, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "capacity-guarded", Status: AppActive}); err != nil {
		t.Fatalf("app create after the capacity statement: %v", err)
	}
}

// A capacity statement no longer waits for an open rule write; it still
// waits for a policy write that keeps the guard.
func TestPgTrafficCapacityStatementsRunBesideRuleWrites(t *testing.T) {
	store, pool, account, _ := trafficHostPGFixture(t)
	for _, tc := range []struct {
		name  string
		begin func(context.Context) (pgx.Tx, error)
		waits bool
	}{
		{"rule write", func(ctx context.Context) (pgx.Tx, error) {
			return store.beginRuleTrafficPolicyMutation(ctx, uuidToPgtype(account.ID), EdgeRuleKindRoute, "open.example.test")
		}, false},
		{"guarded policy write", func(ctx context.Context) (pgx.Tx, error) {
			return store.beginTrafficPolicyMutation(ctx, uuidToPgtype(account.ID))
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open, err := tc.begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = open.Rollback(context.WithoutCancel(t.Context())) }()
			capacity, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = capacity.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := capacity.Exec(t.Context(), `SET LOCAL lock_timeout = '2s'`); err != nil {
				t.Fatal(err)
			}
			_, err = capacity.Exec(t.Context(), `SELECT 1 FROM service_capacity_policy WHERE singleton FOR UPDATE`)
			var pgErr *pgconn.PgError
			waited := errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable
			if waited != tc.waits || !tc.waits && err != nil {
				t.Fatalf("capacity statement waited=%v err=%v, want waited=%v", waited, err, tc.waits)
			}
		})
	}
}

// A rule-only write that fires a capacity trigger ran without the guard the
// trigger needs, so it is refused at commit and leaves nothing behind.
func TestPgTrafficRuleWriteFiringCapacityTriggerFailsClosed(t *testing.T) {
	store, _, account, app := trafficHostPGFixture(t)
	action, err := json.Marshal(EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, statement string
		refused         bool
	}{
		{"capacity lock", `SELECT 1 FROM service_capacity_policy WHERE singleton FOR SHARE`, true},
		{"plain read", `SELECT enabled FROM service_capacity_policy WHERE singleton`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := store.beginRuleTrafficPolicyMutation(t.Context(), uuidToPgtype(account.ID), EdgeRuleKindHeaders)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := tx.Exec(t.Context(), tc.statement); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action)
				VALUES($1,$2,$3,'/',true,'headers',$4::jsonb)`, account.ID, app.ID, tc.name+".example.test", action); err != nil {
				t.Fatal(err)
			}
			err = tx.Commit(t.Context())
			if refused := errors.Is(err, errTrafficRuleCapacityTrigger); refused != tc.refused || !tc.refused && err != nil {
				t.Fatalf("commit err=%v, want refused=%v", err, tc.refused)
			}
			var count int
			if err := store.pool.QueryRow(t.Context(), `SELECT count(*) FROM edge_rules WHERE match_host=$1`, tc.name+".example.test").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 0, false: 1}[tc.refused]; count != want {
				t.Fatalf("rules=%d, want %d", count, want)
			}
		})
	}
}

// With the capacity guard gone, the scoped route locks decide: route writes
// of different accounts on disjoint scopes begin together.
func TestPgTrafficScopedRouteWritesRunConcurrently(t *testing.T) {
	_, pool, first, _ := trafficHostPGFixture(t)
	scoped := NewPgStore(pool, WithTrafficScopedBindingGuard(true))
	second, _ := globalTrafficPeer(t, scoped, "concurrent-route-peer")
	for _, tc := range []struct {
		held, next string
		concurrent bool
	}{
		{"*.a.example.test", "*.b.example.test", true},
		{"*.example.test", "api.example.test", false},
	} {
		t.Run(tc.held+" then "+tc.next, func(t *testing.T) {
			held, err := scoped.beginRuleTrafficPolicyMutation(t.Context(), uuidToPgtype(first.ID), EdgeRuleKindRoute, tc.held)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = held.Rollback(context.WithoutCancel(t.Context())) }()
			timeout := 3 * time.Second
			if tc.concurrent {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			next, err := scoped.beginRuleTrafficPolicyMutation(ctx, uuidToPgtype(second.ID), EdgeRuleKindRoute, tc.next)
			if next != nil {
				_ = next.Rollback(context.WithoutCancel(t.Context()))
			}
			if began := err == nil; began != tc.concurrent {
				t.Fatalf("second route write began=%v (err=%v), want %v", began, err, tc.concurrent)
			}
		})
	}
}
