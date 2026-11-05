//go:build !no_pg

// adr: 570
package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgTrafficAppBindingRetriesRuntimeUpdateBeforeIntent(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh snapshot", true: "cancel and release"}[cancelWaiting], func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			blocker, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := blocker.Exec(t.Context(), `UPDATE apps SET ram_mb=ram_mb WHERE id=$1`, app.ID); err != nil {
				t.Fatal(err)
			}
			var pid int32
			if err := blocker.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			type result struct {
				tx  pgx.Tx
				err error
			}
			done := make(chan result, 1)
			go func() {
				tx, err := store.beginAccountAppTrafficMutation(ctx, account.ID, app.ID)
				done <- result{tx, err}
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if cancelWaiting {
				cancel()
			}
			if err := blocker.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := <-done
			if cancelWaiting {
				if got.tx != nil || !errors.Is(got.err, context.Canceled) {
					t.Fatalf("canceled binding: tx=%v err=%v", got.tx, got.err)
				}
			} else {
				if got.err != nil || got.tx == nil {
					t.Fatalf("fresh binding after concurrent runtime update: %v", got.err)
				}
				if err := got.tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// A new mutation must be able to acquire every released session lock.
			retryCtx, retryCancel := context.WithTimeout(t.Context(), time.Second)
			defer retryCancel()
			recovered, err := store.beginAccountAppTrafficMutation(retryCtx, account.ID, app.ID)
			if err != nil {
				t.Fatalf("binding locks leaked: %v", err)
			}
			if err := recovered.Rollback(retryCtx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func pgTrafficAccountIntent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var digest string
	err := pool.QueryRow(t.Context(), `SELECT md5(jsonb_build_object(
 'accounts',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM accounts t),
 'projects',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM projects t),
 'environments',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM project_environments t),
 'tenants',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM platform_tenants t),
 'keys',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM api_keys t),
 'secrets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY app_id,key) FROM app_secrets t),
 'events',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM events t),
 'audit',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM audit_log t)
 )::text)`).Scan(&digest)
	if err != nil {
		t.Fatal(err)
	}
	return pgTrafficAppBindingIntent(t, pool) + digest
}

func TestPgTrafficAccountRetirement(t *testing.T) {
	for _, mode := range []string{"tenant", "domain", "global", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficAccountRetirement(t, store, account, app, mode, func(host, id string) {
				if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET app_id_redirect=$2::uuid WHERE domain=$1::citext`, host, id); err != nil {
					t.Fatal(err)
				}
			}, func(rule EdgeRule) { seedPgTrafficAccountRule(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
		})
	}
}

func TestPgTrafficAppCapturedOwner(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peer, _ := trafficTenantTransitionPeer(t, store)
	before := pgTrafficAccountIntent(t, pool)
	tx, err := store.beginAccountAppTrafficMutation(t.Context(), peer.ID, app.ID)
	if tx != nil {
		_ = tx.Rollback(context.WithoutCancel(t.Context()))
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale owner accepted: %v", err)
	}
	if before != pgTrafficAccountIntent(t, pool) {
		t.Fatal("stale owner changed intent")
	}
	tx, err = store.beginAccountAppTrafficMutation(t.Context(), account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	other, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := other.Exec(t.Context(), `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = other.Exec(bounded, `UPDATE apps SET account_id=$2::uuid WHERE id=$1::uuid`, app.ID, peer.ID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		t.Fatalf("app ownership row was not held: %v", err)
	}
}

func seedPgTrafficAccountRule(t *testing.T, pool *pgxpool.Pool, rule EdgeRule) {
	t.Helper()
	action, err := json.Marshal(rule.Action)
	if err != nil {
		t.Fatal(err)
	}
	var app any
	if rule.AppID != "" {
		app = rule.AppID
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,$4,'/',true,$5,$6::jsonb)`, rule.ID, rule.AccountID, app, rule.MatchHost, string(rule.Kind), action); err != nil {
		t.Fatal(err)
	}
}

func TestPgTrafficAccountForeignSurface(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	testTrafficAccountForeignSurface(t, store, account, app, func(surface, id string) {
		if _, err := pool.Exec(t.Context(), `UPDATE tenant_surfaces SET app_id=$2::uuid WHERE id=$1::uuid`, surface, id); err != nil {
			t.Fatal(err)
		}
	}, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
}
