package e2e_test

// sched_density_e2e_test.go — scheduler density and wake-burst benchmark.
//
// Real apid, schedd, gatewayd-internal and gatewayd-public processes run
// against a private Postgres with the KVM-free fake vmmd, so every wake is
// instant on the VM side and the numbers measure the control plane: schedd's
// wake path, its select loop and its database load with many apps on one
// node. A real node cannot hold this many VMs on the 16 GB test host; the
// fake isolates what the scheduler reviews changed.
//
// Opt-in: FAAS_E2E_SCHED_DENSITY_APPS=N (skips when unset).
//
//	FAAS_E2E_SCHED_DENSITY_APPS=1000            apps on the node
//	FAAS_E2E_SCHED_DENSITY_BURSTS=25,50,100,150 concurrent wakes per burst
//	FAAS_E2E_SCHED_DENSITY_ROUNDS=3             repeats of each burst size
//	FAAS_E2E_SCHED_DENSITY_CRONS=300            per-minute crons for the cron phase (0 skips)
//	FAAS_E2E_SCHED_DENSITY_OUT=path.json        machine-readable results
//
// Phases: idle with N parked apps, wake bursts of distinct parked apps
// (each burst waits for the idle reaper to park everything again), then a
// minute boundary where the cron sweep and a wake burst coincide. Each phase
// reports wake latency through the public edge, errors, the longest main-loop
// beat gap schedd published, work-pool drops and Postgres transactions.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// densityAppsPerAccount spreads apps over accounts: the gateway lets one account
// hold 2 running and 8 queued cold wakes (api.GatewayWakeAdmission*PerAccount)
// and refuses the rest, so bursts measure the scheduler, not that fairness cap.
const densityAppsPerAccount = 5

type densityApp struct {
	id, host string
}

type densityPhase struct {
	Name          string         `json:"name"`
	Requests      int            `json:"requests"`
	Errors        int            `json:"errors"`
	ErrorStatuses map[string]int `json:"error_statuses,omitempty"`
	P50MS         float64        `json:"p50_ms"`
	P95MS         float64        `json:"p95_ms"`
	P99MS         float64        `json:"p99_ms"`
	MaxMS         float64        `json:"max_ms"`
	WallMS        float64        `json:"wall_ms"`
	LoopMaxAgeS   float64        `json:"loop_max_beat_age_s"`
	PoolDropped   float64        `json:"pool_dropped"`
	PGXacts       int64          `json:"pg_xacts"`
	PGXactsPerSec float64        `json:"pg_xacts_per_s"`
}

func densityEnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		t.Fatalf("%s=%q must be a non-negative integer", key, raw)
	}
	return v
}

func TestE2E_SchedDensity(t *testing.T) {
	raw := os.Getenv("FAAS_E2E_SCHED_DENSITY_APPS")
	if raw == "" {
		t.Skip("set FAAS_E2E_SCHED_DENSITY_APPS=N to run the scheduler density benchmark")
	}
	n := densityEnvInt(t, "FAAS_E2E_SCHED_DENSITY_APPS", 0)
	if n < 1 || n > 5000 {
		t.Fatalf("FAAS_E2E_SCHED_DENSITY_APPS=%d must be in [1,5000]", n)
	}
	rounds := densityEnvInt(t, "FAAS_E2E_SCHED_DENSITY_ROUNDS", 3)
	cronCount := densityEnvInt(t, "FAAS_E2E_SCHED_DENSITY_CRONS", 300)
	bursts := []int{25, 50, 100, 150}
	if raw := os.Getenv("FAAS_E2E_SCHED_DENSITY_BURSTS"); raw != "" {
		bursts = nil
		for _, part := range strings.Split(raw, ",") {
			v, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || v < 1 {
				t.Fatalf("FAAS_E2E_SCHED_DENSITY_BURSTS=%q: bad size %q", raw, part)
			}
			bursts = append(bursts, v)
		}
	}

	var pool *pgxpool.Pool
	f := newNormalPathFixtureWithSetup(t, "density-0", api.PlanScale, 0,
		api.CreateAppRequest{Slug: "density-0", Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)},
		func(p *pgxpool.Pool) []string {
			pool = p
			// The harness node otherwise reports the test host (2 vCPUs),
			// so CPU admission holds about ten instances. Fake VMs use no
			// host resources; give the node a large production shape so
			// the scheduler, not the test host, is what the bursts measure.
			if _, err := p.Exec(context.Background(), `
				update compute_nodes set vpcpus = 64, mem_mb = 65536, admission_ceiling_mb = 47600, vcpu_budget = 640
				 where name = 'default-local'`); err != nil {
				t.Fatalf("size the density node: %v", err)
			}
			return nil
		})
	if f == nil {
		return
	}
	ctx := f.ctx
	// The fake VM answers every instance it boots or restores.
	f.vmmd.SetDefaultVersion("density")

	setupStart := time.Now()
	apps := seedDensityApps(t, f, pool, n)
	t.Logf("density: seeded %d apps in %s", len(apps), time.Since(setupStart).Round(time.Second))
	waitDensityAllParked(t, pool, 5*time.Minute)

	var results []densityPhase
	sampler := newDensityLoopSampler(f.h.ScheddMetricsURL)
	defer sampler.stop()

	// Phase 1: idle with every app parked. The cost of N apps with no traffic.
	results = append(results, runDensityPhase(t, ctx, pool, sampler, f.h.ScheddMetricsURL, "idle-30s", func() ([]time.Duration, map[string]int) {
		time.Sleep(30 * time.Second)
		return nil, nil
	}))

	// Phase 2: wake bursts of distinct parked apps.
	next := 0
	for _, size := range bursts {
		if size > len(apps) {
			size = len(apps)
		}
		for r := 0; r < rounds; r++ {
			batch := make([]densityApp, 0, size)
			for len(batch) < size {
				batch = append(batch, apps[next%len(apps)])
				next++
			}
			name := fmt.Sprintf("burst-%d-r%d", size, r+1)
			results = append(results, runDensityPhase(t, ctx, pool, sampler, f.h.ScheddMetricsURL, name, func() ([]time.Duration, map[string]int) {
				return densityWake(f, batch)
			}))
			waitDensityAllParked(t, pool, 3*time.Minute)
		}
	}

	// Phase 3: a minute boundary where every cron fires and a wake burst lands.
	if cronCount > 0 {
		if cronCount > len(apps) {
			cronCount = len(apps)
		}
		for i := 0; i < cronCount; i++ {
			if _, err := f.store.CreateCron(ctx, apps[i].id, "* * * * *", "/cron", true); err != nil {
				t.Fatalf("create cron %d: %v", i, err)
			}
		}
		// Let the sweep pick the crons up, then align to just before a minute.
		time.Sleep(time.Until(time.Now().Truncate(time.Minute).Add(time.Minute + 58*time.Second)))
		size := bursts[len(bursts)-1]
		batch := make([]densityApp, 0, size)
		for i := len(apps) - 1; i >= 0 && len(batch) < size; i-- {
			batch = append(batch, apps[i])
		}
		results = append(results, runDensityPhase(t, ctx, pool, sampler, f.h.ScheddMetricsURL, fmt.Sprintf("cron-%d+burst-%d", cronCount, size), func() ([]time.Duration, map[string]int) {
			time.Sleep(2 * time.Second) // the minute turns; the sweep starts
			lat, errs := densityWake(f, batch)
			time.Sleep(20 * time.Second) // the cron fires drain and wake their apps
			return lat, errs
		}))
	}

	t.Logf("density: %d apps on one node", n)
	t.Logf("%-22s %6s %5s %8s %8s %8s %8s %9s %9s %8s %9s", "phase", "reqs", "errs", "p50ms", "p95ms", "p99ms", "maxms", "loopmax_s", "pooldrop", "pgxact", "pgxact/s")
	for _, r := range results {
		t.Logf("%-22s %6d %5d %8.1f %8.1f %8.1f %8.1f %9.2f %9.0f %8d %9.1f", r.Name, r.Requests, r.Errors, r.P50MS, r.P95MS, r.P99MS, r.MaxMS, r.LoopMaxAgeS, r.PoolDropped, r.PGXacts, r.PGXactsPerSec)
		if len(r.ErrorStatuses) > 0 {
			t.Logf("    %s errors: %v", r.Name, r.ErrorStatuses)
		}
	}
	if out := os.Getenv("FAAS_E2E_SCHED_DENSITY_OUT"); out != "" {
		data, _ := json.MarshalIndent(map[string]any{"apps": n, "phases": results}, "", "  ")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Errorf("write %s: %v", out, err)
		}
	}
}

// seedDensityApps creates n apps across Scale accounts (5 apps each), each
// with a live deployment, a signed rootfs layer and an init snapshot, so a
// wake restores. Apps park after 10 idle seconds.
func seedDensityApps(t *testing.T, f *normalPathFixture, pool *pgxpool.Pool, n int) []densityApp {
	t.Helper()
	ctx := f.ctx
	apps := make([]densityApp, 0, n)
	key := ""
	for i := 0; i < n; i++ {
		if i%densityAppsPerAccount == 0 {
			key = f.h.SeedAccount(ctx, api.PlanScale, fmt.Sprintf("density-acct-%d", i/densityAppsPerAccount))
		}
		slug := fmt.Sprintf("dens-%05d", i)
		body, status := doReq(t, f.h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
			Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false), RAMMB: 256,
		})
		if status != http.StatusCreated {
			t.Fatalf("create app %s: status=%d body=%s", slug, status, body)
		}
		var app api.AppResponse
		if err := json.Unmarshal(body, &app); err != nil {
			t.Fatalf("decode app %s: %v", slug, err)
		}
		dep, err := f.store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64),
		})
		if err != nil {
			t.Fatalf("create deployment %s: %v", slug, err)
		}
		if err := f.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatalf("mark deployment live %s: %v", slug, err)
		}
		publishNormalPathLayer(t, f, dep.ID)
		seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{memBytes: int64(app.RAMMB) << 20})
		apps = append(apps, densityApp{id: app.ID, host: slug + ".apps.test.example"})
		if (i+1)%100 == 0 {
			t.Logf("density: %d/%d apps seeded", i+1, n)
		}
	}
	// 10 s is the schema's minimum (apps_idle_timeout_s_check). Scale apps
	// default to bearer public auth; the benchmark sends anonymous requests.
	if _, err := pool.Exec(ctx, `update apps set idle_timeout_s = 10, public_auth_mode = 'open' where slug like 'dens-%'`); err != nil {
		t.Fatalf("set idle timeouts: %v", err)
	}
	return apps
}

// densityWake sends one request to each app at once through the public edge.
func densityWake(f *normalPathFixture, batch []densityApp) ([]time.Duration, map[string]int) {
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: len(batch), MaxConnsPerHost: 0}}
	lat := make([]time.Duration, len(batch))
	statuses := make([]string, len(batch))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, app := range batch {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, err := http.NewRequest(http.MethodGet, f.h.EdgeURL()+"/", nil)
			if err != nil {
				statuses[i] = "request: " + err.Error()
				return
			}
			req.Host = app.host
			began := time.Now()
			resp, err := client.Do(req)
			lat[i] = time.Since(began)
			if err != nil {
				statuses[i] = "transport"
				return
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				// The problem's code says which gate refused the wake.
				var problem struct {
					Code   string `json:"code"`
					Detail string `json:"detail"`
				}
				_ = json.Unmarshal(body, &problem)
				statuses[i] = strconv.Itoa(resp.StatusCode) + " " + problem.Code
				if os.Getenv("FAAS_E2E_SCHED_DENSITY_DETAIL") != "" {
					statuses[i] += ": " + problem.Detail
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	errs := map[string]int{}
	for _, s := range statuses {
		if s != "" {
			errs[s]++
		}
	}
	return lat, errs
}

func runDensityPhase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sampler *densityLoopSampler, metricsURL, name string, run func() ([]time.Duration, map[string]int)) densityPhase {
	t.Helper()
	droppedBefore := densityPoolDropped(metricsURL)
	xactsBefore := densityPGXacts(ctx, pool)
	sampler.reset()
	began := time.Now()
	lat, errs := run()
	wall := time.Since(began)
	phase := densityPhase{
		Name:        name,
		Requests:    len(lat),
		WallMS:      float64(wall.Microseconds()) / 1000,
		LoopMaxAgeS: sampler.max(),
		PoolDropped: densityPoolDropped(metricsURL) - droppedBefore,
		PGXacts:     densityPGXacts(ctx, pool) - xactsBefore,
	}
	phase.PGXactsPerSec = float64(phase.PGXacts) / wall.Seconds()
	for _, c := range errs {
		phase.Errors += c
	}
	if len(errs) > 0 {
		phase.ErrorStatuses = errs
	}
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		pct := func(p float64) float64 {
			idx := int(math.Ceil(p*float64(len(lat)))) - 1
			idx = max(0, min(idx, len(lat)-1))
			return float64(lat[idx].Microseconds()) / 1000
		}
		phase.P50MS, phase.P95MS, phase.P99MS, phase.MaxMS = pct(0.50), pct(0.95), pct(0.99), pct(1)
	}
	t.Logf("density: %s done: %d reqs, %d errors, p95 %.1f ms, loop max beat age %.2f s", name, phase.Requests, phase.Errors, phase.P95MS, phase.LoopMaxAgeS)
	return phase
}

// waitDensityAllParked waits until no density app has a live instance.
func waitDensityAllParked(t *testing.T, pool *pgxpool.Pool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var live int
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(), `
			select count(*) from instances i join apps a on a.id = i.app_id
			 where a.slug like 'dens-%'
			   and i.state in ('waking','cold_booting','running','draining','snapshotting','warm','frozen','migrating')`).Scan(&live)
		if err != nil {
			t.Fatalf("count live density instances: %v", err)
		}
		if live == 0 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%d density instances still live after %s", live, timeout)
}

func densityPGXacts(ctx context.Context, pool *pgxpool.Pool) int64 {
	var n int64
	_ = pool.QueryRow(ctx, `select xact_commit + xact_rollback from pg_stat_database where datname = current_database()`).Scan(&n)
	return n
}

// densityMetric sums every sample of family whose labels contain match.
func densityMetric(metricsURL, family, match string) float64 {
	resp, err := http.Get(metricsURL) //nolint:noctx // test scrape of a local daemon
	if err != nil {
		return math.NaN()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return math.NaN()
	}
	sum := 0.0
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, family+"{") || !strings.Contains(line, match) {
			continue
		}
		fields := strings.Fields(line)
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			sum += v
		}
	}
	return sum
}

func densityPoolDropped(metricsURL string) float64 {
	return densityMetric(metricsURL, "schedd_loop_work_total", `outcome="dropped"`)
}

// densityLoopSampler scrapes schedd's main-loop beat age (published once a
// second) and keeps the largest value seen since the last reset.
type densityLoopSampler struct {
	mu      sync.Mutex
	maxAge  float64
	done    chan struct{}
	stopped sync.Once
}

func newDensityLoopSampler(metricsURL string) *densityLoopSampler {
	s := &densityLoopSampler{done: make(chan struct{})}
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-tick.C:
				age := densityMetric(metricsURL, "schedd_loop_last_beat_age_seconds", `loop="main"`)
				if math.IsNaN(age) {
					continue
				}
				s.mu.Lock()
				s.maxAge = max(s.maxAge, age)
				s.mu.Unlock()
			}
		}
	}()
	return s
}

func (s *densityLoopSampler) reset() {
	s.mu.Lock()
	s.maxAge = 0
	s.mu.Unlock()
}

func (s *densityLoopSampler) max() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxAge
}

func (s *densityLoopSampler) stop() {
	s.stopped.Do(func() { close(s.done) })
}
