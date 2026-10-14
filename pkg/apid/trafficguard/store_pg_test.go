package trafficguard_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgMCPAccessExpectation(t *testing.T, s *state.PgStore, ctx context.Context, account string) api.MCPAccessPolicyExpectation {
	t.Helper()
	rules, err := s.ListEdgeRulesForAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	responses := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, r := range rules {
		responses = append(responses, edgerules.EdgeRuleResponse(r))
	}
	fingerprint, err := api.MCPAccessPolicyFingerprint("https://reports.example/mcp", responses, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return api.MCPAccessPolicyExpectation{Resource: "https://reports.example/mcp", Fingerprint: fingerprint}
}

func waitMCPAccountLock(t *testing.T, pool *pgxpool.Pool, account string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=hashtext('mcp_access_policy')::oid AND objid=hashtext($1)::oid)`, account).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("account lock waiter not observed")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitMCPTransactionBlockedBy(t *testing.T, pool *pgxpool.Pool, pid uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::integer=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("promotion did not wait for the in-flight policy transaction")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPgMCPAccessPolicyTrafficFence(t *testing.T) {
	s, pool, ctx := openMCPAccessStore(t)
	account, app, stable := seedMCPAccessLive(t, s, ctx, "mcp-access")
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: strings.Repeat("d", 64), Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: account, Slug: "wildcard-owner", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	expectation := pgMCPAccessExpectation(t, s, ctx, account)
	// An in-flight rule transaction wins the account lock before promotion.
	// Promotion must wait and then compare against the newly committed wildcard.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO edge_rules(account_id,app_id,kind,match_host,match_path,action) VALUES($1,$2,'jwt','*.example','/**','{"kind":"jwt","jwt":{"issuer":"https://issuer.example","jwks_url":"https://issuer.example/jwks","algorithms":["RS256"]}}')`, account, otherApp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('mcp_access_policy'),hashtext($1))`, account); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, expectation), candidate.ID, 100, stable)
		result <- err
	}()
	waitMCPTransactionBlockedBy(t, pool, tx.Conn().PgConn().PID())
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, state.ErrMCPAccessPolicyChanged) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promotion did not finish")
	}
	before, _ := s.DeploymentByID(ctx, stable)
	after, _ := s.DeploymentByID(ctx, candidate.ID)
	if before.TrafficPercent != 100 || after.TrafficPercent != 0 {
		t.Fatal("stale policy promotion changed traffic")
	}
	fresh := pgMCPAccessExpectation(t, s, ctx, account)
	if _, err = s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, fresh), candidate.ID, 100, stable); err != nil {
		t.Fatal(err)
	}
}

func TestPgMCPAccessPolicyEdgeWriteCommitFence(t *testing.T) {
	s, pool, ctx := openMCPAccessStore(t)
	account, app, _ := seedMCPAccessLive(t, s, ctx, "mcp-write-fence")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('mcp_access_policy'),hashtext($1))`, account); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account, AppID: app, Kind: state.EdgeRuleKindJWT, Enabled: true, MatchHost: "*.example", MatchPath: "/**", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindJWT, JWT: &state.EdgeRuleJWTAction{Issuer: "https://issuer.example"}}})
		result <- err
	}()
	waitMCPAccountLock(t, pool, account)
	select {
	case err := <-result:
		t.Fatalf("writer finished before account fence released: %v", err)
	default:
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not finish")
	}
}

func TestPgMCPFleetRevisionSurvivesPruning(t *testing.T) {
	s, pool, ctx := openMCPAccessStore(t)
	_, app, rule := seedMCPAccessLive(t, s, ctx, "mcp-retention")
	var oldest, newest int64
	for _, dest := range []*int64{&oldest, &newest} {
		if err := pool.QueryRow(ctx, `INSERT INTO edge_rule_change_log (app_id,rule_id,operation,created_at) VALUES ($1,$2,'updated',now()-interval '40 days') RETURNING id`, app, rule).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if removed, err := s.PruneEdgeRuleChangeLog(ctx, cutoff); err != nil || removed != 1 {
		t.Fatalf("prune = %d %v", removed, err)
	}
	if got, err := s.LatestEdgeRuleChangeID(ctx); err != nil || got != newest {
		t.Fatalf("desired revision lost: %d %v, want %d", got, err, newest)
	}
	if removed, err := s.PruneEdgeRuleChangeLog(ctx, cutoff); err != nil || removed != 0 {
		t.Fatalf("latest revision pruned: %d %v", removed, err)
	}
	var retained bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM edge_rule_change_log WHERE id=$1)`, oldest).Scan(&retained); err != nil || retained {
		t.Fatalf("old history not pruned: %v %v", retained, err)
	}
}
