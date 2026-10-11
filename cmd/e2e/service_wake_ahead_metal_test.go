//go:build metal

// adr: 950 — opt-in depends_on wake-ahead through the real stack. Three real
// guests form a chain hop1 → hop2 → hop3. Each cycle parks all three, then
// calls them one after another through the node-local service proxy, which is
// the order a real request through the chain would reach them. With
// wake-ahead off every hop pays its own restore; with it on, hop1's wake
// restores hop2 and hop2's restores hop3 in parallel, so later hops are warm
// (or already restoring) when they are called.
//
// Opt-in: FAAS_SERVICE_WAKE_AHEAD_CYCLES=N (default skip). Cycles alternate
// off/on so node drift affects both modes equally.
package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

var wakeAheadHops = []string{"hop1", "hop2", "hop3"}

func TestServiceWakeAheadChainMetal(t *testing.T) {
	raw := os.Getenv("FAAS_SERVICE_WAKE_AHEAD_CYCLES")
	if raw == "" {
		t.Skip("set FAAS_SERVICE_WAKE_AHEAD_CYCLES=N to run the wake-ahead chain benchmark")
	}
	cycles, err := strconv.Atoi(raw)
	if err != nil || cycles < 1 || cycles > 100 {
		t.Fatalf("FAAS_SERVICE_WAKE_AHEAD_CYCLES=%q must be an integer in [1,100]", raw)
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

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_GATEWAY_SERVICE_WAKE_AHEAD=true")
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false

	// The caller of hop1 is a plain app; the control listener authenticates it
	// by app id, so it needs no deployment of its own.
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "edge", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create edge: status=%d", got)
	}
	edgeID := mustGetAppID(t, h, key, "edge")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ids := make(map[string]string, len(wakeAheadHops))
	for _, slug := range wakeAheadHops {
		if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
			t.Fatalf("create %s: status=%d", slug, got)
		}
		ids[slug] = mustGetAppID(t, h, key, slug)
		rawDep, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
		if status != http.StatusAccepted {
			t.Fatalf("deploy %s: status=%d body=%s", slug, status, rawDep)
		}
		var dep api.DeploymentResponse
		if err := json.Unmarshal(rawDep, &dep); err != nil {
			t.Fatalf("decode deployment: %v", err)
		}
		if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 180*time.Second); err != nil {
			h.DumpLogs(t)
			t.Fatalf("%s deployment did not reach live: %v", slug, err)
		}
	}
	t.Cleanup(func() { parkWakeAheadHops(t, h, pool, key, ids) })

	// One unmeasured pass warms page cache and route caches for every hop.
	for _, slug := range wakeAheadHops {
		if status, _, body := pollServiceCall(t, h, edgeID, slug, "/", http.StatusOK, 60*time.Second); status != http.StatusOK {
			t.Fatalf("warm-up %s: status=%d body=%s", slug, status, body)
		}
	}

	results := map[api.ServiceWakeAhead][]float64{}
	for i := 0; i < 2*cycles; i++ {
		mode := api.ServiceWakeAheadOff
		if i%2 == 1 {
			mode = api.ServiceWakeAheadDeclared
		}
		setWakeAheadChain(t, pool, ids, mode)
		parkWakeAheadHops(t, h, pool, key, ids)
		// Outlive the service proxy's endpoint lease (5 s). A production app
		// parks only after its idle timeout (>= 30 s), so its cached endpoint
		// has always expired; calling within the lease of an API park routes
		// to the parked instance and 503s, which is not what this measures.
		time.Sleep(6 * time.Second)
		started := time.Now()
		for _, slug := range wakeAheadHops {
			status, header, body := serviceCall(t, h, edgeID, slug, "/")
			if status != http.StatusOK {
				time.Sleep(3 * time.Second)
				logWakeAheadBoots(t, pool, ids, started)
				t.Fatalf("cycle %d (%s): call %s: status=%d headers=%v body=%s", i, mode, slug, status, header, body)
			}
		}
		ms := float64(time.Since(started).Milliseconds())
		logWakeAheadBoots(t, pool, ids, started)
		results[mode] = append(results[mode], ms)
		t.Logf("cycle %d wake_ahead=%s chain_ms=%.0f", i, mode, ms)
	}

	off := wakeAheadMedian(results[api.ServiceWakeAheadOff])
	on := wakeAheadMedian(results[api.ServiceWakeAheadDeclared])
	t.Logf("cold 3-hop chain over %d cycles per mode: off p50=%.0f ms, declared p50=%.0f ms (%.0f%% of off)",
		cycles, off, on, 100*on/off)
	for _, slug := range wakeAheadHops[1:] {
		var n int
		if err := pool.QueryRow(context.Background(), `select count(*) from events
			where kind = 'wake.boot_started' and data->>'app_id' = $1 and data->>'trigger' = 'service.wake_ahead'`, ids[slug]).Scan(&n); err != nil {
			t.Fatalf("count %s wake-ahead boots: %v", slug, err)
		}
		t.Logf("%s wake-ahead boots: %d", slug, n)
		if n == 0 {
			t.Errorf("%s was never restored by wake-ahead", slug)
		}
	}
	if on >= off {
		t.Errorf("declared chain p50 %.0f ms is not faster than off %.0f ms", on, off)
	}
}

// setWakeAheadChain writes hop1 → hop2 → hop3 bindings with the given mode
// directly into the manifest, the shape apid persists from Compose.
func setWakeAheadChain(t *testing.T, pool *pgxpool.Pool, ids map[string]string, mode api.ServiceWakeAhead) {
	t.Helper()
	for i, slug := range wakeAheadHops {
		bindings := []api.AppServiceBinding{}
		if i+1 < len(wakeAheadHops) {
			next := wakeAheadHops[i+1]
			bindings = append(bindings, api.AppServiceBinding{Binding: next, Service: next})
		}
		rawBindings, err := json.Marshal(bindings)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `update apps set manifest = manifest
			|| jsonb_build_object('service_wake_ahead', $2::text, 'service_bindings', $3::jsonb) where id = $1`,
			ids[slug], string(mode), string(rawBindings)); err != nil {
			t.Fatalf("set %s manifest: %v", slug, err)
		}
	}
}

func parkWakeAheadHops(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key string, ids map[string]string) {
	t.Helper()
	for _, slug := range wakeAheadHops {
		if raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status/100 != 2 {
			t.Fatalf("park %s: status=%d body=%s", slug, status, raw)
		}
	}
	for _, slug := range wakeAheadHops {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		_, err := e2etest.WaitForAppParked(ctx, t, pool, ids[slug], 40*time.Second)
		cancel()
		if err != nil {
			t.Fatal(fmt.Errorf("%s not parked: %w", slug, err))
		}
	}
}

// logWakeAheadBoots prints every boot of a hop since `since`, with its
// trigger, so a cycle's restore order is visible in the run log.
func logWakeAheadBoots(t *testing.T, pool *pgxpool.Pool, ids map[string]string, since time.Time) {
	t.Helper()
	slugByID := make(map[string]string, len(ids))
	for slug, id := range ids {
		slugByID[id] = slug
	}
	rows, err := pool.Query(context.Background(), `select data->>'app_id', coalesce(data->>'trigger',''), at
		from events where kind = 'wake.boot_started' and at >= $1 order by at`, since)
	if err != nil {
		t.Logf("list boots: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var appID, trigger string
		var at time.Time
		if err := rows.Scan(&appID, &trigger, &at); err != nil {
			t.Logf("scan boot: %v", err)
			return
		}
		t.Logf("  boot %-5s trigger=%-18s +%dms", slugByID[appID], trigger, at.Sub(since).Milliseconds())
	}
}

func wakeAheadMedian(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}
