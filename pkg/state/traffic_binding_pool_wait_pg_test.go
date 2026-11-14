//go:build !no_pg

package state_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgTrafficBindingWaitsForAConnectionOutsideItsAnalysisBound reproduces
// Hunt 31 H31-11: with apid's pool briefly empty, a deploy was refused
// traffic_policy_too_complex at 2001 ms because the policy analysis bound
// also timed the wait for a database connection. The wait now happens first.
func TestPgTrafficBindingWaitsForAConnectionOutsideItsAnalysisBound(t *testing.T) {
	_, base, ctx := pgStoreWithPool(t)
	cfg := base.Config()
	cfg.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := state.NewPgStore(pool)
	acct, err := s.CreateAccount(ctx, "traffic-pool-wait@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "traffic-pool-wait", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}

	// Every connection is busy for longer than the analysis bound.
	held := make([]*pgxpool.Conn, 0, cfg.MaxConns)
	for i := int32(0); i < cfg.MaxConns; i++ {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := s.BeginTrafficBindingForTest(ctx, acct.ID, app.ID)
		if err == nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
		done <- err
	}()
	time.Sleep(api.TrafficPolicyAnalysisTimeout + 500*time.Millisecond)
	for _, conn := range held {
		conn.Release()
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("traffic binding after a %s pool wait = %v, want it to begin", api.TrafficPolicyAnalysisTimeout+500*time.Millisecond, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("traffic binding did not begin")
	}
}

// TestPgTrafficBindingSlowPoolAcquiresStayOutsideItsAnalysisBound reproduces
// the rc.322 node canary: with apid's pool starved, a deploy waited for a
// second connection for its session locks inside the analysis bound and was
// refused traffic_policy_too_complex although it had analysed nothing. Every
// acquisition here takes longer than the bound; the write must still begin.
func TestPgTrafficBindingSlowPoolAcquiresStayOutsideItsAnalysisBound(t *testing.T) {
	_, base, ctx := pgStoreWithPool(t)
	cfg := base.Config()
	cfg.MaxConns = 3
	slow := api.TrafficPolicyAnalysisTimeout + 300*time.Millisecond
	var armed atomic.Bool
	cfg.BeforeAcquire = func(ctx context.Context, _ *pgx.Conn) bool {
		if armed.Load() {
			time.Sleep(slow)
		}
		return true
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := state.NewPgStore(pool)
	acct, err := s.CreateAccount(ctx, "traffic-slow-acquire@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "traffic-slow-acquire", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}

	armed.Store(true)
	tx, err := s.BeginTrafficBindingForTest(ctx, acct.ID, app.ID)
	armed.Store(false)
	if err != nil {
		t.Fatalf("traffic binding with %s pool acquisitions = %v, want it to begin", slow, err)
	}
	_ = tx.Rollback(context.WithoutCancel(ctx))
}

// TestPgTrafficBindingWaitsForTheAccessFenceOutsideItsAnalysisBound: the
// request's membership fence is a FOR SHARE that waits for a membership or
// credential change in flight. That wait is not analysis, so it must not
// refuse the write as traffic_policy_too_complex.
func TestPgTrafficBindingWaitsForTheAccessFenceOutsideItsAnalysisBound(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	f := setupOffboarding(t, s)
	snapshot, err := s.OrgAccessSnapshot(ctx, f.org.ID, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	fenced, cancel := state.WithOrgAccessFence(ctx, state.OrgAccessFence{OrgID: f.org.ID, AccountID: f.actor.ID, Revision: snapshot.Revision})
	defer cancel(nil)

	// A membership change in flight holds the row for longer than the bound.
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := holder.Exec(ctx, "SELECT 1 FROM org_memberships WHERE org_id = $1 AND account_id = $2 FOR NO KEY UPDATE", f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := s.BeginTrafficBindingForTest(fenced, f.actor.ID, f.app.ID)
		if err == nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
		done <- err
	}()
	time.Sleep(api.TrafficPolicyAnalysisTimeout + 500*time.Millisecond)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("traffic binding after a %s fence wait = %v, want it to begin", api.TrafficPolicyAnalysisTimeout+500*time.Millisecond, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("traffic binding did not begin")
	}
}
