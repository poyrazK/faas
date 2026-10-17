package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/state"
)

// stallingReachabilityStore answers every app except "stall", whose read
// blocks until its context ends.
type stallingReachabilityStore struct{}

func (stallingReachabilityStore) ListAllAccounts(context.Context) ([]state.Account, error) {
	return []state.Account{{ID: "acct"}}, nil
}

func (stallingReachabilityStore) ListApps(context.Context, string) ([]state.App, error) {
	return []state.App{{ID: "stall"}, {ID: "up"}, {ID: "down"}}, nil
}

func (stallingReachabilityStore) WasInvokedSuccessfullySince(ctx context.Context, _, appID string, _ time.Time) (bool, error) {
	// pgxpool refuses an acquire whose context has already ended; so does
	// the fake, which is what made every later app fail under one deadline.
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch appID {
	case "stall":
		<-ctx.Done()
		return false, ctx.Err()
	case "up":
		return true, nil
	default:
		return false, nil
	}
}

// TestSweepAPIReachabilityStallFailsOnlyItsApp pins H10-133: one shared walk
// deadline let a single stalled read fail every later app with an
// already-cancelled context, leaving their gauges stale.
func TestSweepAPIReachabilityStallFailsOnlyItsApp(t *testing.T) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_api_reachable"}, []string{"account_id", "app_id"})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	stamped, failed := sweepAPIReachability(context.Background(), stallingReachabilityStore{}, gauge, time.Now(), 50*time.Millisecond, log)

	if stamped != 2 || failed != 1 {
		t.Fatalf("stamped=%d failed=%d, want 2 and 1", stamped, failed)
	}
	if got := testutil.ToFloat64(gauge.WithLabelValues("acct", "up")); got != 1 {
		t.Errorf("up = %v, want 1", got)
	}
	if got := testutil.ToFloat64(gauge.WithLabelValues("acct", "down")); got != 0 {
		t.Errorf("down = %v, want 0", got)
	}
}

// TestSweepAPIReachabilityStopsWithDaemon keeps a cancelled daemon context
// from stamping anything.
func TestSweepAPIReachabilityStopsWithDaemon(t *testing.T) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_api_reachable_stop"}, []string{"account_id", "app_id"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stamped, failed := sweepAPIReachability(ctx, stallingReachabilityStore{}, gauge, time.Now(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if stamped != 0 || failed != 0 {
		t.Fatalf("stamped=%d failed=%d after daemon cancel, want 0 and 0", stamped, failed)
	}
}
