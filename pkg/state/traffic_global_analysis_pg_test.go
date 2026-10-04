//go:build !no_pg

// adr: 531
package state

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func globalTrafficPeer(t *testing.T, store *PgStore, name string) (Account, App) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), name+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: name, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return account, app
}

func globalTrafficRule(account Account, app App, host string, numbers int) CreateEdgeRuleParams {
	in := trafficHostRule(account, app, host)
	if numbers > 0 {
		in.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", numbers-1) + "1e130000]")}
	}
	return in
}

func requireGlobalTrafficAggregate(t *testing.T, err error) {
	t.Helper()
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "global_route_rule_projection" || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("expected global route projection refusal: %v", err)
	}
}

func TestPgTrafficGlobalRouteConcurrentAccountsShareAllowance(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "global-route-peer")
	before, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, in := range []CreateEdgeRuleParams{globalTrafficRule(account, app, "*", 260), globalTrafficRule(peerAccount, peer, "*", 260)} {
		go func() {
			<-start
			_, err := store.CreateEdgeRuleIfUnderQuota(t.Context(), in, api.MustLimitsFor(account.Plan))
			results <- err
		}()
	}
	close(start)
	accepted := 0
	for range 2 {
		if err := <-results; err != nil {
			requireGlobalTrafficAggregate(t, err)
		} else {
			accepted++
		}
	}
	var rules, changes int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM edge_rules), (SELECT count(*) FROM edge_rule_change_log WHERE id>$1)`, before).Scan(&rules, &changes); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || rules != 1 || changes != 1 {
		t.Fatalf("cross-account allowance: accepted=%d rules=%d changes=%d", accepted, rules, changes)
	}
	row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), pool, sqlc.ReadTrafficHostAnalysisParams{MaxInputs: api.TrafficPolicyMaxAnalysisInputs,
		MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes, Defaults: mustTrafficDefaults(t), AppsSuffix: store.trafficAppsSuffix})
	if err != nil || row.Inputs != 3 || len(row.Data) > 2048 || strings.Contains(string(row.Data), "1e130000") {
		t.Fatalf("global metadata body transfer: inputs=%d bytes=%d err=%v", row.Inputs, len(row.Data), err)
	}
}

func mustTrafficDefaults(t *testing.T) []byte {
	t.Helper()
	defaults, err := trafficHostActionDefaults()
	if err != nil {
		t.Fatal(err)
	}
	return defaults
}

func TestPgTrafficSessionLocksPrecedeSnapshot(t *testing.T) {
	for _, global := range []bool{false, true} {
		name := "owned"
		if global {
			name = "global"
		}
		t.Run(name, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			conn, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), global)
			if err != nil || busy {
				t.Fatalf("session serialization: busy=%v err=%v", busy, err)
			}
			defer release(t.Context())
			if status := conn.Conn().PgConn().TxStatus(); status != 'I' {
				t.Fatalf("serialization froze a transaction view before locking: status=%c", status)
			}
			// Emulate a previous writer's commit before the guarded view starts.
			// The locks themselves must not freeze a repeatable-read snapshot.
			if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,'snapshot.example.test','/',true,'route','{}')`, account.ID, app.ID); err != nil {
				t.Fatal(err)
			}
			tx, err := conn.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			viewAccount := uuidToPgtype(account.ID)
			if global {
				viewAccount = pgtype.UUID{}
			}
			view, err := readTrafficHostAnalysis(t.Context(), tx, viewAccount, store.trafficAppsSuffix)
			if err != nil || len(view.Groups) != 1 {
				t.Fatalf("first guarded view missed preceding commit: groups=%d err=%v", len(view.Groups), err)
			}
		})
	}
}

func TestPgTrafficSessionCleanupAfterCanceledRollbackAndFailedUnlock(t *testing.T) {
	for _, failedUnlock := range []bool{false, true} {
		name := "canceled_rollback"
		if failedUnlock {
			name = "failed_unlock"
		}
		t.Run(name, func(t *testing.T) {
			_, pool, account, _ := trafficHostPGFixture(t)
			config := pool.Config().Copy()
			config.MaxConns = 1
			small, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer small.Close()
			guarded := NewPgStore(small)
			tx, err := guarded.beginRuleTrafficPolicyMutation(t.Context(), uuidToPgtype(account.ID), EdgeRuleKindRoute)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			pid := tx.Conn().PgConn().PID()
			if failedUnlock {
				if _, err := tx.Exec(t.Context(), `SELECT pg_advisory_unlock_all()`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_ = tx.Rollback(ctx)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			next, err := guarded.beginRuleTrafficPolicyMutation(ctx, uuidToPgtype(account.ID), EdgeRuleKindRoute)
			if err != nil {
				t.Fatalf("cleanup orphaned session locks or pool connection: %v", err)
			}
			if next.Conn().PgConn().PID() == pid {
				t.Fatal("uncertain session was returned to the pool")
			}
			if err := next.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPgTrafficSessionFitsAPIDDirectPoolWithOuterLock(t *testing.T) {
	_, pool, account, app := trafficHostPGFixture(t)
	dsn := trafficSessionTestDSN(t, pool)
	t.Setenv(db.DirectDSNEnv, dsn)
	t.Setenv(db.NotifyHubEnv, "1")
	ordinary, err := db.OpenWithAppName(t.Context(), dsn, "faas-apid")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ordinary)
	direct := db.DirectPool(ordinary)
	if direct == ordinary || direct.Config().MaxConns != 3 {
		t.Fatal("API direct pool does not reserve nested mutation capacity")
	}
	// Occupy the notification hub's slot, then the actual outer mutation lock.
	hub, err := direct.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Release()
	store := NewPgStore(ordinary)
	release, err := store.AcquireEdgeRuleMutationLock(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release(t.Context())
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tx, err := store.beginRuleTrafficPolicyMutation(ctx, uuidToPgtype(account.ID), EdgeRuleKindRoute)
	if err != nil {
		t.Fatalf("API guard starved behind hub and convergence lock: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func trafficSessionTestDSN(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	config := pool.Config().ConnConfig
	dsn := config.ConnString()
	// ConnString preserves the original input. The clone/schema harness edits
	// Config afterwards, so carry its actual database and search path forward.
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("database", config.Database)
		if path := config.RuntimeParams["search_path"]; path != "" {
			query.Set("search_path", path)
		}
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	escape := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	dsn += " dbname='" + escape.Replace(config.Database) + "'"
	if path := config.RuntimeParams["search_path"]; path != "" {
		dsn += " search_path='" + escape.Replace(path) + "'"
	}
	return dsn
}

func TestPgTrafficGlobalRouteDisjointHostsAndRetargetRollback(t *testing.T) {
	store, _, account, app := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "global-route-disjoint")
	if _, err := store.CreateEdgeRule(t.Context(), globalTrafficRule(account, app, "api.*", 260)); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateEdgeRule(t.Context(), globalTrafficRule(peerAccount, peer, "web.*", 260))
	if err != nil {
		t.Fatalf("disjoint hosts became a flat global quota: %v", err)
	}
	bridgeAccount, bridge := globalTrafficPeer(t, store, "global-route-bridge")
	if _, err := store.CreateEdgeRule(t.Context(), globalTrafficRule(bridgeAccount, bridge, "*", 3)); err != nil {
		t.Fatalf("connected selectors became a flat global quota: %v", err)
	}
	before, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	host := "*.test"
	_, err = store.UpdateEdgeRule(t.Context(), second.ID, UpdateEdgeRuleParams{MatchHost: &host})
	requireGlobalTrafficAggregate(t, err)
	saved, err := store.GetEdgeRuleByID(t.Context(), second.ID)
	if err != nil || saved.MatchHost != second.MatchHost || !saved.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatal("rejected global retarget changed intent")
	}
	after, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil || after != before {
		t.Fatalf("rejected global retarget emitted change: before=%d after=%d err=%v", before, after, err)
	}
}

func TestPgTrafficGlobalSnapshotDoesNotCreditConcurrentDeletion(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "global-route-deleted")
	for _, in := range []CreateEdgeRuleParams{globalTrafficRule(account, app, "*", 257), globalTrafficRule(peerAccount, peer, "*", 260)} {
		encoded, err := json.Marshal(in.Action)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,'*','/',true,'route',$3::jsonb)`, in.AccountID, in.AppID, encoded); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := store.beginRuleTrafficPolicyMutation(t.Context(), uuidToPgtype(account.ID), EdgeRuleKindRoute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	// A shrinking writer/cascade need not take the global lock. Its commit
	// must remain invisible to this write's before/after projection.
	if _, err := pool.Exec(t.Context(), `DELETE FROM edge_rules WHERE account_id=$1`, peerAccount.ID); err != nil {
		t.Fatal(err)
	}
	ledger, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	in := globalTrafficRule(account, app, "credit.example.test", 3)
	encoded, err := json.Marshal(in.Action)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,'/',true,'route',$4::jsonb)`, account.ID, app.ID, in.MatchHost, encoded); err != nil {
		t.Fatal(err)
	}
	requireGlobalTrafficAggregate(t, tx.Commit(t.Context()))
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil || after != ledger {
		t.Fatalf("rejected growth retained ledger: before=%d after=%d err=%v", ledger, after, err)
	}
	if _, err := store.CreateEdgeRule(t.Context(), in); err != nil {
		t.Fatalf("fresh snapshot after repair: %v", err)
	}
}

func TestPgTrafficGlobalLockWaitersReleasePoolAndAppLocks(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.WithoutCancel(t.Context())) }()
	var locked bool
	if err := lock.QueryRow(t.Context(), `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, globalTrafficRoutesLock).Scan(&locked); err != nil || !locked {
		t.Fatalf("hold global lock: locked=%v err=%v", locked, err)
	}
	config := pool.Config().Copy()
	config.MaxConns = 3
	smallPool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer smallPool.Close()
	waiting := NewPgStore(smallPool)
	results := make(chan error, 6)
	for range 6 {
		go func() {
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			_, err := waiting.CreateEdgeRuleIfUnderQuota(ctx, globalTrafficRule(account, app, "waiting.example.test", 0), api.MustLimitsFor(account.Plan))
			results <- err
		}()
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := smallPool.Ping(ctx); err != nil {
		t.Fatalf("global waiters starved ordinary reads: %v", err)
	}
	if _, err := smallPool.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE NOWAIT`, app.ID); err != nil {
		t.Fatalf("global waiter retained an app lock: %v", err)
	}
	in := globalTrafficRule(account, app, "headers.example.test", 0)
	in.Kind, in.Action = EdgeRuleKindHeaders, EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}}
	if _, err := waiting.CreateEdgeRule(ctx, in); err != nil {
		t.Fatalf("global lock blocked a non-route mutation: %v", err)
	}
	for range 6 {
		if err := <-results; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("global waiter bypassed lock: %v", err)
		}
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRule(t.Context(), globalTrafficRule(account, app, "after-wait.example.test", 0)); err != nil {
		t.Fatalf("canceled global waiter leaked a lock: %v", err)
	}
}

func TestPgTrafficGlobalReadMatchesRouteOnlyRuntime(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "global-route-reader")
	for _, in := range []CreateEdgeRuleParams{globalTrafficRule(account, app, "*", 0), globalTrafficRule(peerAccount, peer, "*", 0)} {
		if _, err := store.CreateEdgeRule(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	in := globalTrafficRule(account, app, "*", 0)
	in.Kind, in.Action = EdgeRuleKindHeaders, EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}}
	if _, err := store.CreateEdgeRule(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	view, err := readTrafficHostAnalysis(t.Context(), tx, pgtype.UUID{}, store.trafficAppsSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Environments) != 0 || len(view.Assets) != 0 || len(view.Groups) != 2 {
		t.Fatalf("global read retained non-route inputs: %+v", view)
	}
	accepted := map[int]bool{0: true, 1: true}
	totals := hostTotals(view, accepted)
	row, err := sqlc.New().ReadPublicHostEdgeRules(t.Context(), tx, sqlc.ReadPublicHostEdgeRulesParams{Host: "unclaimed.example.test", RouteOnly: true,
		MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
	if err != nil || row.Oversized || totals.rows != 2 || totals.canonical < int64(len(row.Data)) {
		t.Fatalf("global/runtime route read mismatch: totals=%+v bytes=%d err=%v", totals, len(row.Data), err)
	}
	var rules []EdgeRule
	if err := json.Unmarshal(row.Data, &rules); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rules)
	if err != nil || totals.compiled < int64(len(encoded)) {
		t.Fatalf("global compiler underestimate: compiled=%d bytes=%d err=%v", totals.compiled, len(encoded), err)
	}
}
