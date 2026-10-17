// adr: 570
package trafficacceptance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type fleetCounterCommand struct {
	Kind                    string
	Count, Percent, Minimum int
}
type fleetCounterResult struct{ Allowed, Errors int }

// This child runs the production counter adapters in an independent process,
// connected to the parent's isolated database, without any shared Go memory.
func TestTrafficCounterProcess(t *testing.T) {
	if os.Getenv("GREGALE_TRAFFIC_COUNTER_CHILD") == "" {
		t.Skip("subprocess helper")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = os.Getenv("GREGALE_TRAFFIC_COUNTER_DATABASE")
	if path := os.Getenv("GREGALE_TRAFFIC_COUNTER_SEARCH_PATH"); path != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = path
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	budget, err := gateway.NewSharedRetryBudget(state.NewPGTrafficRetryBackend(pool))
	if err != nil {
		t.Fatal(err)
	}
	rate := state.NewPGRateLimitBackend(pool)
	appID := os.Getenv("GREGALE_TRAFFIC_COUNTER_APP")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var cmd fleetCounterCommand
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			t.Fatal(err)
		}
		result := fleetCounterResult{}
		for range cmd.Count {
			switch cmd.Kind {
			case "observe":
				if budget.ObserveOriginal(t.Context(), appID) {
					result.Allowed++
				} else {
					result.Errors++
				}
			case "retry":
				if budget.AllowRetry(t.Context(), appID, cmd.Percent, cmd.Minimum) {
					result.Allowed++
				}
			case "rate":
				_, ok, err := rate.ConsumeToken(t.Context(), "rule", appID, "pro", 0.01, 4)
				if err != nil {
					result.Errors++
				} else if ok {
					result.Allowed++
				}
			default:
				t.Fatalf("unknown command %q", cmd.Kind)
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

type fleetCounterProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *json.Decoder
}

func startFleetCounterProcess(t *testing.T, pool *pgxpool.Pool, appID string) *fleetCounterProcess {
	t.Helper()
	c := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTrafficCounterProcess$")
	c.Env = append(os.Environ(), "GREGALE_TRAFFIC_COUNTER_CHILD=1",
		"GREGALE_TRAFFIC_COUNTER_DATABASE="+pool.Config().ConnConfig.Database,
		"GREGALE_TRAFFIC_COUNTER_SEARCH_PATH="+pool.Config().ConnConfig.RuntimeParams["search_path"],
		"GREGALE_TRAFFIC_COUNTER_APP="+appID)
	stdin, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	p := &fleetCounterProcess{c, stdin, json.NewDecoder(stdout)}
	t.Cleanup(func() { _ = stdin.Close(); _ = c.Wait() })
	return p
}

func (p *fleetCounterProcess) exchange(cmd fleetCounterCommand) (fleetCounterResult, error) {
	var result fleetCounterResult
	if err := json.NewEncoder(p.stdin).Encode(cmd); err != nil {
		return result, err
	}
	err := p.output.Decode(&result)
	return result, err
}

func TestPGTrafficCountersShareAcrossProcessesAndReplacement(t *testing.T) {
	s, pool, ctx := fleetCounterStore(t)
	appID := seedFleetCounterApp(t, s, ctx, "fleet-counters")
	first := startFleetCounterProcess(t, pool, appID)
	second := startFleetCounterProcess(t, pool, appID)
	for _, p := range []*fleetCounterProcess{first, second} {
		if got, err := p.exchange(fleetCounterCommand{Kind: "observe", Count: 10}); err != nil || got.Allowed != 10 || got.Errors != 0 {
			t.Fatalf("observe=(%v,%v)", got, err)
		}
	}
	for _, test := range []struct {
		kind string
		want int
	}{{"retry", 2}, {"rate", 4}} {
		results := make(chan fleetCounterResult, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		started := time.Now()
		for _, p := range []*fleetCounterProcess{first, second} {
			wg.Go(func() {
				got, err := p.exchange(fleetCounterCommand{Kind: test.kind, Count: 20, Percent: 10, Minimum: 1})
				results <- got
				errs <- err
			})
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		allowed := 0
		for result := range results {
			if result.Errors != 0 {
				t.Fatalf("store errors: %v", result)
			}
			allowed += result.Allowed
		}
		if allowed != test.want {
			t.Fatalf("%s admitted=%d want=%d", test.kind, allowed, test.want)
		}
		t.Logf("two process %s: 40 operations in %s", test.kind, time.Since(started))
	}
	// Process replacement and a third replica cannot mint a fresh minimum.
	_ = first.stdin.Close()
	third := startFleetCounterProcess(t, pool, appID)
	for _, kind := range []string{"retry", "rate"} {
		if got, err := third.exchange(fleetCounterCommand{Kind: kind, Count: 1, Percent: 10, Minimum: 1}); err != nil || got.Allowed != 0 || got.Errors != 0 {
			t.Fatalf("replacement %s=(%v,%v)", kind, got, err)
		}
	}
}

func TestPGTrafficRetryExpiryOutageAndBoundedWait(t *testing.T) {
	s, pool, ctx := fleetCounterStore(t)
	appID := seedFleetCounterApp(t, s, ctx, "retry-recovery")
	backend := state.NewPGTrafficRetryBackend(pool)
	budget, err := gateway.NewSharedRetryBudget(backend)
	if err != nil {
		t.Fatal(err)
	}
	if !budget.ObserveOriginal(ctx, appID) || !budget.AllowRetry(ctx, appID, 10, 1) {
		t.Fatal("initial shared allowance denied")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE traffic_retry_counters RENAME TO traffic_retry_counters_offline"); err != nil {
		t.Fatal(err)
	}
	if budget.ObserveOriginal(ctx, appID) || budget.AllowRetry(ctx, appID, 100, 32) {
		t.Fatal("unavailable store granted replay allowance")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE traffic_retry_counters_offline RENAME TO traffic_retry_counters"); err != nil {
		t.Fatal(err)
	}
	if budget.AllowRetry(ctx, appID, 10, 1) {
		t.Fatal("recovery reset the spent window")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT app_id FROM traffic_retry_counters WHERE app_id=$1 FOR UPDATE", appID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if budget.AllowRetry(ctx, appID, 100, 32) {
		t.Fatal("locked counter granted replay")
	}
	if elapsed := time.Since(started); elapsed > 5*api.TrafficCounterOperationTimeout {
		t.Fatalf("counter wait was not bounded: %s", elapsed)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE traffic_retry_counters SET expires_at=now()-interval '1 second' WHERE app_id=$1", appID); err != nil {
		t.Fatal(err)
	}
	if budget.AllowRetry(ctx, appID, 100, 32) {
		t.Fatal("expired window granted retry without an original")
	}
	if !budget.ObserveOriginal(ctx, appID) || !budget.AllowRetry(ctx, appID, 10, 1) || budget.AllowRetry(ctx, appID, 10, 1) {
		t.Fatal("new window did not restore exactly one minimum")
	}
	if backend.BackendID() == "" || backend.BackendID() != state.NewPGTrafficRetryBackend(pool).BackendID() {
		t.Fatal("unstable shared backend identity")
	}
	if budget.ObserveOriginal(ctx, uuid.NewString()) {
		t.Fatal("missing app gained a retry counter")
	}
}

func fleetCounterStore(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool, t.Context()
}

func seedFleetCounterApp(t *testing.T, s *state.PgStore, ctx context.Context, suffix string) string {
	t.Helper()
	account, err := s.CreateAccount(ctx, suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: suffix, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}
