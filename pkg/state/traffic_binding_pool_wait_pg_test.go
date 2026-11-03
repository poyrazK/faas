//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

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
