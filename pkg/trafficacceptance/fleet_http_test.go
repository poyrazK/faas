// adr: 570
package trafficacceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only VM placement is a fixture. Each process serves a real gateway Handler,
// real atomic Postgres counters and a real HTTP guest fixture.
type fleetHTTPBackend struct{ app gateway.App }

func (b *fleetHTTPBackend) Lookup(context.Context, string) (gateway.App, bool) { return b.app, true }
func (b *fleetHTTPBackend) Pick(string) gateway.PickResult {
	return gateway.PickResult{OK: true, Target: gateway.Target{AppID: b.app.ID, InstanceID: "fleet-guest", AddedAt: time.Now()}}
}
func (*fleetHTTPBackend) HealthyCount(string) int { return 1 }
func (*fleetHTTPBackend) Admit(context.Context, string, string, string, string, int) (string, gateway.WakeMethod, bool, error) {
	return "", gateway.WakeMethodUnspecified, false, errors.New("warm fixture must not wake")
}
func (*fleetHTTPBackend) LookupMirrorRules(context.Context, string) ([]gateway.MirrorRuleRow, bool) {
	return nil, false
}
func (*fleetHTTPBackend) ScheduleMirror(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}

func TestTrafficHTTPGatewayProcess(t *testing.T) {
	if os.Getenv("GREGALE_TRAFFIC_HTTP_CHILD") == "" {
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
	appID := os.Getenv("GREGALE_TRAFFIC_COUNTER_APP")
	stored, err := state.NewPgStore(pool).AppByID(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fleetHTTPBackend{app: gateway.App{ID: appID, AccountID: stored.AccountID, Plan: api.PlanPro, PublicAuth: gateway.PublicAuthConfig{Mode: "open"}, RequestRateLimitRPS: 1, RequestRateLimitBurst: 4}}
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "guest served") }))
	defer guest.Close()
	guestURL, err := url.Parse(guest.URL)
	if err != nil {
		t.Fatal(err)
	}
	h := gateway.NewHandlerWith(backend, gateway.NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithCentralBackend(state.NewPGRateLimitBackend(pool))
	h.WithForwarding(func(gateway.Target) http.Handler { return httputil.NewSingleHostReverseProxy(guestURL) })
	server := httptest.NewServer(h)
	defer server.Close()
	if err := json.NewEncoder(os.Stdout).Encode(server.URL); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func startFleetHTTPGateway(t *testing.T, pool *pgxpool.Pool, appID string) (string, io.WriteCloser) {
	t.Helper()
	c := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTrafficHTTPGatewayProcess$")
	c.Env = append(os.Environ(), "GREGALE_TRAFFIC_HTTP_CHILD=1", "GREGALE_TRAFFIC_COUNTER_DATABASE="+pool.Config().ConnConfig.Database,
		"GREGALE_TRAFFIC_COUNTER_SEARCH_PATH="+pool.Config().ConnConfig.RuntimeParams["search_path"], "GREGALE_TRAFFIC_COUNTER_APP="+appID)
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
	t.Cleanup(func() { _ = stdin.Close(); _ = c.Wait() })
	var endpoint string
	if err := json.NewDecoder(stdout).Decode(&endpoint); err != nil {
		t.Fatal(err)
	}
	return endpoint, stdin
}

type fleetHTTPResult struct {
	status   int
	body     string
	duration time.Duration
	err      error
}

func callFleetHTTP(endpoint string) fleetHTTPResult {
	start := time.Now()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(endpoint + "/work")
	if err != nil {
		return fleetHTTPResult{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return fleetHTTPResult{resp.StatusCode, string(body), time.Since(start), err}
}

func TestTwoHTTPGatewayProcessesShareRateAdmissionAndRecover(t *testing.T) {
	s, pool, ctx := fleetCounterStore(t)
	appID := seedFleetCounterApp(t, s, ctx, "http-fleet")
	first, stopFirst := startFleetHTTPGateway(t, pool, appID)
	second, _ := startFleetHTTPGateway(t, pool, appID)
	results := make(chan fleetHTTPResult, 16)
	var wg sync.WaitGroup
	started := time.Now()
	for i := range 16 {
		wg.Go(func() { results <- callFleetHTTP([]string{first, second}[i%2]) })
	}
	wg.Wait()
	close(results)
	allowed := 0
	latencies := make([]time.Duration, 0, 16)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		latencies = append(latencies, result.duration)
		if result.status == http.StatusOK {
			allowed++
			if result.body != "guest served" {
				t.Fatalf("guest response=%q", result.body)
			}
		} else if result.status != http.StatusTooManyRequests {
			t.Fatalf("unexpected status=%d body=%s", result.status, result.body)
		}
	}
	// Refill can legitimately admit more than the initial burst on a slow host.
	bound := 4 + int(time.Since(started)/time.Second)
	if allowed < 4 || allowed > bound {
		t.Fatalf("fleet admissions=%d bound=%d", allowed, bound)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("two HTTP gateways: admitted=%d/16 p50=%s p95=%s", allowed, latencies[8], latencies[15])
	// Future refill timestamps model statements arriving out of clock order.
	// They must preserve zero debt, and process replacement cannot reset it.
	if _, err := pool.Exec(ctx, "UPDATE pg_ratelimit_counters SET tokens=0,last_refill=now()+interval '1 minute' WHERE scope='app' AND subject_id=$1", appID); err != nil {
		t.Fatal(err)
	}
	_ = stopFirst.Close()
	third, _ := startFleetHTTPGateway(t, pool, appID)
	if result := callFleetHTTP(third); result.err != nil || result.status != http.StatusTooManyRequests {
		t.Fatalf("replacement admission=%+v", result)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE pg_ratelimit_counters RENAME TO pg_ratelimit_counters_offline"); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{second, third} {
		if result := callFleetHTTP(endpoint); result.err != nil || result.status != http.StatusServiceUnavailable || !strings.Contains(result.body, "rate_limit_unavailable") {
			t.Fatalf("outage admission=%+v", result)
		}
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE pg_ratelimit_counters_offline RENAME TO pg_ratelimit_counters"); err != nil {
		t.Fatal(err)
	}
	if result := callFleetHTTP(third); result.err != nil || result.status != http.StatusTooManyRequests {
		t.Fatalf("recovery reset balance=%+v", result)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT subject_id FROM pg_ratelimit_counters WHERE scope='app' AND subject_id=$1 FOR UPDATE", appID); err != nil {
		t.Fatal(err)
	}
	if result := callFleetHTTP(second); result.err != nil || result.status != http.StatusServiceUnavailable || result.duration > time.Second {
		t.Fatalf("locked-store admission=%+v", result)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if result := callFleetHTTP(second); result.err != nil || result.status != http.StatusTooManyRequests {
		t.Fatalf("lock recovery=%+v", result)
	}
}
