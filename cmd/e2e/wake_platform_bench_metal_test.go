//go:build metal

// wake_platform_bench_metal_test.go — end-to-end platform wake benchmark.
//
// TestDeployWakeMetal's latency loop waits for the idle reaper on every
// cycle (~20 s) and reports only the gateway histogram. This harness parks
// through the API instead, so a cycle costs about one second, and it reads
// each wake's own timeline from the events table: the gateway-admitted →
// first-byte interval that gateway_platform_wake_latency_seconds measures,
// split into scheduler, vmmd and post-boot segments. It is the A/B harness
// for request-path latency work across gatewayd, schedd and vmmd at once.
//
// Opt-in: FAAS_WAKE_PLATFORM_BENCH_CYCLES=N (skips when unset).
//
//	FAAS_WAKE_PLATFORM_BENCH_CYCLES=40 make test-metal PKGS=./cmd/e2e \
//	  RUN_REGEX=TestWakePlatformBenchMetal RUN_ARGS=-v

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

func TestWakePlatformBenchMetal(t *testing.T) {
	raw := os.Getenv("FAAS_WAKE_PLATFORM_BENCH_CYCLES")
	if raw == "" {
		t.Skip("set FAAS_WAKE_PLATFORM_BENCH_CYCLES=N to run the platform wake benchmark")
	}
	cycles, err := strconv.Atoi(raw)
	if err != nil || cycles < 1 || cycles > 500 {
		t.Fatalf("FAAS_WAKE_PLATFORM_BENCH_CYCLES=%q must be an integer in [1,500]", raw)
	}
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	rawDep, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, rawDep)
	}
	var depResp api.DeploymentResponse
	if err := json.Unmarshal(rawDep, &depResp); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 120*time.Second); err != nil {
		h.DumpLogs(t)
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForAppParked(ctx, t, pool, appID, 60*time.Second); err != nil {
		t.Fatalf("not parked after deploy: %v", err)
	}

	url := gatewayAppURL(h, "hello")
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	// One unmeasured wake warms page cache, route caches and the layer
	// attestation the same way a long-running node already is.
	if body, status := doGetWithHost(t, client, url, "hello.apps.test.example", 30*time.Second); status != http.StatusOK {
		t.Fatalf("warm-up wake: status=%d body=%s", status, body)
	}

	// FAAS_WAKE_PLATFORM_BENCH_PARK=api parks through POST /park, which marks
	// the app evicted_cold and so exercises ensureWake's lifecycle
	// transition. The default lets the idle reaper park the instance, the
	// path an ordinary scale-to-zero app takes (~20 s per cycle).
	apiPark := os.Getenv("FAAS_WAKE_PLATFORM_BENCH_PARK") == "api"
	if !apiPark {
		setAppIdleTimeout(t, h, key, "hello", api.IdleTimeoutFloorSeconds)
	}
	// FAAS_WAKE_PLATFORM_BENCH_WARM_POOL=N keeps N paused, already-restored
	// VMs, so a wake measures the in-place resume path instead of a restore.
	if raw := os.Getenv("FAAS_WAKE_PLATFORM_BENCH_WARM_POOL"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			t.Fatalf("FAAS_WAKE_PLATFORM_BENCH_WARM_POOL=%q must be a non-negative integer", raw)
		}
		if raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/hello", api.UpdateAppRequest{WarmPoolSize: &n}); status != http.StatusOK {
			t.Fatalf("set warm pool size: status=%d body=%s", status, raw)
		}
	}
	wakeIDs := make([]string, 0, cycles)
	clientMs := make(map[string]int64, cycles)
	for i := 0; i < cycles; i++ {
		if apiPark {
			if raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/park", nil); status/100 != 2 {
				t.Fatalf("cycle %d: park: status=%d body=%s", i, status, raw)
			}
		}
		pctx, pcancel := context.WithTimeout(context.Background(), 45*time.Second)
		_, err := e2etest.WaitForAppParked(pctx, t, pool, appID, 40*time.Second)
		pcancel()
		if err != nil {
			t.Fatalf("cycle %d: not parked: %v", i, err)
		}
		// Let the park's own writes (snapshot reuse, notifications) settle so
		// the wake measures a quiet node, as a production idle app sees.
		time.Sleep(time.Second)
		started := time.Now()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, "hello.apps.test.example", 30*time.Second)
		elapsed := time.Since(started).Milliseconds()
		if status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
			t.Fatalf("cycle %d: status=%d body=%q", i, status, body)
		}
		if wakeID == "" {
			t.Fatalf("cycle %d: response carried no x-faas-wake-id (served hot?)", i)
		}
		wakeIDs = append(wakeIDs, wakeID)
		clientMs[wakeID] = elapsed
	}
	// The first-byte event is written asynchronously after the response.
	time.Sleep(3 * time.Second)
	reportWakePlatformBench(t, pool, wakeIDs, clientMs)
}

type wakeBenchRow map[string]float64

func reportWakePlatformBench(t *testing.T, pool *pgxpool.Pool, wakeIDs []string, clientMs map[string]int64) {
	t.Helper()
	rows := make([]wakeBenchRow, 0, len(wakeIDs))
	methods := map[string]int{}
	for _, id := range wakeIDs {
		r, method, err := loadWakeBenchRow(pool, id)
		if err != nil {
			t.Fatalf("wake %s: %v", id, err)
		}
		r["client_ms"] = float64(clientMs[id])
		methods[method]++
		rows = append(rows, r)
	}
	t.Logf("platform wake benchmark over %d wakes (methods: %v)", len(rows), methods)
	t.Logf("%-34s %7s %7s %7s %7s %7s", "segment (ms)", "min", "p50", "p90", "p95", "max")
	for _, k := range []string{
		"client_ms",
		"gateway_latency_ms",
		"admitted_to_first_byte",
		"admitted_to_boot_started",
		"boot_started_to_readiness",
		"readiness_to_boot_completed",
		"boot_completed_to_first_byte",
		"restore_total_ms",
		"setup_network_ms",
		"stage_pre_boot_files_ms",
		"resume_hook_ms",
		"wait_ready_ms",
	} {
		vals := make([]float64, 0, len(rows))
		for _, r := range rows {
			if v, ok := r[k]; ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			continue
		}
		sort.Float64s(vals)
		q := func(p float64) float64 {
			i := int(p*float64(len(vals))+0.999999) - 1
			if i < 0 {
				i = 0
			}
			if i >= len(vals) {
				i = len(vals) - 1
			}
			return vals[i]
		}
		t.Logf("%-34s %7.0f %7.0f %7.0f %7.0f %7.0f", k, vals[0], q(0.5), q(0.9), q(0.95), vals[len(vals)-1])
	}
	if path := os.Getenv("FAAS_WAKE_PLATFORM_BENCH_JSON"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		defer func() { _ = f.Close() }()
		enc := json.NewEncoder(f)
		for _, r := range rows {
			_ = enc.Encode(r)
		}
	}
}

func loadWakeBenchRow(pool *pgxpool.Pool, wakeID string) (wakeBenchRow, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rs, err := pool.Query(ctx, `select kind, at, coalesce(data, '{}'::jsonb) from events
		where data->>'wake_id' = $1 and kind in ('wake.admitted','wake.boot_started','wake.readiness_200',
		  'wake.boot_completed','wake.proxy_first_byte','wake.restore_breakdown') order by at`, wakeID)
	if err != nil {
		return nil, "", err
	}
	defer rs.Close()
	at := map[string]time.Time{}
	data := map[string]map[string]any{}
	for rs.Next() {
		var kind string
		var ts time.Time
		var raw []byte
		if err := rs.Scan(&kind, &ts, &raw); err != nil {
			return nil, "", err
		}
		if _, seen := at[kind]; !seen {
			at[kind] = ts
			m := map[string]any{}
			_ = json.Unmarshal(raw, &m)
			data[kind] = m
		}
	}
	if err := rs.Err(); err != nil {
		return nil, "", err
	}
	r := wakeBenchRow{}
	span := func(name, from, to string) {
		a, okA := at[from]
		b, okB := at[to]
		if okA && okB {
			r[name] = float64(b.Sub(a).Microseconds()) / 1000
		}
	}
	span("admitted_to_first_byte", "wake.admitted", "wake.proxy_first_byte")
	span("admitted_to_boot_started", "wake.admitted", "wake.boot_started")
	span("boot_started_to_readiness", "wake.boot_started", "wake.readiness_200")
	span("readiness_to_boot_completed", "wake.readiness_200", "wake.boot_completed")
	span("boot_completed_to_first_byte", "wake.boot_completed", "wake.proxy_first_byte")
	num := func(kind, field, name string) {
		if v, ok := data[kind][field].(float64); ok {
			r[name] = v
		}
	}
	num("wake.proxy_first_byte", "latency_ms", "gateway_latency_ms")
	num("wake.restore_breakdown", "total_ms", "restore_total_ms")
	num("wake.restore_breakdown", "setup_network_ms", "setup_network_ms")
	num("wake.restore_breakdown", "stage_pre_boot_files_ms", "stage_pre_boot_files_ms")
	num("wake.restore_breakdown", "resume_hook_ms", "resume_hook_ms")
	num("wake.restore_breakdown", "wait_ready_ms", "wait_ready_ms")
	method := fmt.Sprint(data["wake.boot_completed"]["method"])
	if _, ok := at["wake.proxy_first_byte"]; !ok {
		return nil, method, fmt.Errorf("no wake.proxy_first_byte event")
	}
	return r, method, nil
}
