//go:build metal

// safe_release_drill_metal_test.go — ADR-911 safe-release drills through the
// real wire: apid stamps the default policy, meterd's canary progression
// evaluates the circuit breaker against live counters, schedd and vmmd run
// the revisions, and gatewayd-internal splits real requests between them.
//
// The circuit breaker reads its OOM, liveness-restart, and managed-dependency
// signals through PromQL. The harness has no Prometheus server, so
// safeReleasePromStub scrapes schedd's and the gateway's real /metrics and
// answers the breaker's instant queries from those samples. Label values,
// sentinel series, and counter increments therefore come from the daemons.
//
// Build tag: metal. Requires /dev/kvm, root, Firecracker, FAAS_TEST_KERNEL.
// Wall clock is about 25 minutes; run with -timeout 60m.

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	apicanary "github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const safeReleaseDrillBody = helloBody

func TestSafeReleaseDrillMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal safe-release drill")
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
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", safeReleaseDrillBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	v1Img, _ := e2etest.HelloImageAboveBase("library/sr-v1", safeReleaseDrillBody)
	v1 := registry.AddImage("library/sr-v1", v1Img)
	v2Img, _ := e2etest.HelloImageAboveBaseWithArgs("library/sr-v2", safeReleaseDrillBody, "-addr", ":8080")
	v2 := registry.AddImage("library/sr-v2", v2Img)
	badImg, _ := e2etest.FailingAPIImageAboveBase("library/sr-bad", safeReleaseDrillBody)
	bad := registry.AddImage("library/sr-bad", badImg)
	crashImg, _ := e2etest.FailingLivenessImageAboveBase("library/sr-crash", safeReleaseDrillBody)
	crash := registry.AddImage("library/sr-crash", crashImg)

	prom := newSafeReleasePromStub(t)
	operator := freeTCPAddr(t)
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Meterd,
		"FAAS_CANARY_PROGRESSION_TOKEN="+strings.Repeat("c", 40),
		"FAAS_SAFEDEPLOY_TOKEN="+strings.Repeat("s", 40),
		"FAAS_APID_METRICS_ADDR="+operator,
		"FAAS_APID_INTERNAL_BASE_URL=http://"+operator,
		"FAAS_PROMETHEUS_URL="+prom.URL(),
	)
	prom.setTargets(h.ScheddMetricsURL, h.GatewayControlURL+"/metrics")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	d := &safeReleaseDrill{t: t, h: h, store: state.NewPgStore(pool), key: h.SeedAccount(context.Background(), api.PlanHobby)}
	d.waitLease(true, 2*time.Minute)

	t.Run("flag-free release of a live app gets the safe default", func(t *testing.T) {
		d.createApp(t, "sr-default")
		first := d.deployLive(t, "sr-default", api.CreateDeploymentRequest{Image: v1})
		if first.CanaryTotalSteps != 0 || first.RollbackOn5xx {
			t.Fatalf("first deploy = preset:%q steps:%d rollback:%v, want immediate", first.CanaryPreset, first.CanaryTotalSteps, first.RollbackOn5xx)
		}
		second := d.deployLive(t, "sr-default", api.CreateDeploymentRequest{Image: v2})
		if second.CanaryPreset != api.DefaultReleaseCanaryPreset || !second.RollbackOn5xx || second.TrafficPercent != 1 {
			t.Fatalf("second deploy = preset:%q rollback:%v traffic:%d, want balanced at 1%% with rollback",
				second.CanaryPreset, second.RollbackOn5xx, second.TrafficPercent)
		}
	})

	t.Run("clean low-traffic canary advances past the bound and rollback recovers", func(t *testing.T) {
		d.createApp(t, "sr-quiet")
		stable := d.deployLive(t, "sr-quiet", api.CreateDeploymentRequest{Image: v1})
		candidate := d.deployLive(t, "sr-quiet", api.CreateDeploymentRequest{Image: v2, Canary: customCanary("1", "30s")})
		started := time.Now()
		// No traffic: the stage can only pass through the bounded hold.
		got := d.waitDeployment(t, candidate.ID, 30*time.Second+api.CanaryLowTrafficMaxHold+3*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "complete" || dep.RolloutState == "aborted"
		})
		if got.RolloutState != "complete" || got.TrafficPercent != 100 {
			t.Fatalf("quiet candidate = state:%q traffic:%d reason:%q, want complete at 100%%", got.RolloutState, got.TrafficPercent, got.RolloutAbortedReason)
		}
		t.Logf("DRILL low_traffic_advance seconds=%.0f", time.Since(started).Seconds())

		rollbackStarted := time.Now()
		if raw, status := doReq(t, d.h, d.key, http.MethodPost, "/v1/apps/sr-quiet/rollback", map[string]any{}); status/100 != 2 {
			t.Fatalf("rollback: status=%d body=%s", status, raw)
		}
		// Log the target's progress so a slow rollback is distinguishable from
		// a stuck one in the drill record.
		lastLogged := time.Time{}
		d.waitDeployment(t, stable.ID, 5*time.Minute, func(dep state.Deployment) bool {
			if time.Since(lastLogged) >= 30*time.Second {
				t.Logf("DRILL rollback_progress t=%.0fs status=%q rollout=%q traffic=%d error=%q",
					time.Since(rollbackStarted).Seconds(), dep.Status, dep.RolloutState, dep.TrafficPercent, dep.Error)
				lastLogged = time.Now()
			}
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})
		d.waitServing(t, "sr-quiet", time.Minute)
		recovery := time.Since(rollbackStarted)
		t.Logf("DRILL rollback_recovery seconds=%.1f", recovery.Seconds())
		if recovery > 60*time.Second {
			t.Errorf("rollback recovery %.1fs exceeds the 60s scorecard bar", recovery.Seconds())
		}
	})

	t.Run("bad release aborts and restores the predecessor", func(t *testing.T) {
		d.createApp(t, "sr-bad")
		stable := d.deployLive(t, "sr-bad", api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, "sr-bad", time.Minute)
		candidate := d.deployLive(t, "sr-bad", api.CreateDeploymentRequest{Image: bad, Canary: customCanary("50", "10m")})
		stop := d.sendTraffic("sr-bad", "/api", 500*time.Millisecond)
		defer stop()
		// Log the circuit breaker's own input (the request-telemetry summary
		// meterd reads) so a missed abort shows whether telemetry arrived.
		stageStart := time.Now().Add(-time.Minute)
		lastLogged := time.Time{}
		got := d.waitDeployment(t, candidate.ID, 4*time.Minute, func(dep state.Deployment) bool {
			if time.Since(lastLogged) >= 30*time.Second {
				d.logBreakerInput(t, dep.AppID, candidate.ID, stable.ID, stageStart)
				lastLogged = time.Now()
			}
			return dep.RolloutState == "aborted" || dep.Status != state.DeployLive || dep.TrafficPercent == 0
		})
		t.Logf("DRILL bad_release state=%q status=%q reason=%q", got.RolloutState, got.Status, got.RolloutAbortedReason)
		d.waitDeployment(t, stable.ID, time.Minute, func(dep state.Deployment) bool {
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})
	})

	t.Run("crash-looping release aborts on liveness restarts", func(t *testing.T) {
		liveness := &api.CreateDeploymentOverrides{LivenessProbe: &api.DeploymentLivenessProbe{
			Path: "/livez", IntervalS: 1, ConsecutiveFailures: 1, CooldownS: 10,
		}}
		d.createApp(t, "sr-crash")
		stable := d.deployLive(t, "sr-crash", api.CreateDeploymentRequest{Image: v1, Overrides: liveness})
		d.waitServing(t, "sr-crash", time.Minute)
		candidate := d.deployLive(t, "sr-crash", api.CreateDeploymentRequest{Image: crash, Overrides: liveness, Canary: customCanary("50", "10m")})
		// A trickle keeps waking the candidate; / stays 200, so only the
		// liveness restarts can stop this release.
		stop := d.sendTraffic("sr-crash", "/", 5*time.Second)
		defer stop()
		got := d.waitDeployment(t, candidate.ID, 5*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted"
		})
		if !strings.Contains(got.RolloutAbortedReason, "crash loop") {
			t.Fatalf("crash candidate abort reason = %q, want crash loop", got.RolloutAbortedReason)
		}
		t.Logf("DRILL crash_loop reason=%q", got.RolloutAbortedReason)
		d.waitDeployment(t, stable.ID, time.Minute, func(dep state.Deployment) bool {
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})
	})

	// Last: it stops meterd for the rest of the harness.
	t.Run("defaulted release falls back to immediate when the canary worker is down", func(t *testing.T) {
		d.createApp(t, "sr-fallback")
		d.deployLive(t, "sr-fallback", api.CreateDeploymentRequest{Image: v1})
		if err := d.h.KillMeterd(); err != nil {
			t.Fatalf("kill meterd: %v", err)
		}
		d.waitLease(false, 2*time.Minute)
		got := d.deployLive(t, "sr-fallback", api.CreateDeploymentRequest{Image: v2})
		got = d.waitDeployment(t, got.ID, time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
		if got.CanaryTotalSteps != 0 || !got.RollbackOn5xx {
			t.Fatalf("fallback deploy = steps:%d rollback:%v, want immediate cutover with rollback", got.CanaryTotalSteps, got.RollbackOn5xx)
		}
	})
}

func customCanary(percent, duration string) *api.CanaryPresetSpec {
	p, _ := strconv.Atoi(percent)
	return &api.CanaryPresetSpec{Preset: "custom", Stages: []apicanary.CustomStage{
		{Percent: p, Duration: duration},
		{Percent: 100, Duration: "0s"},
	}}
}

type safeReleaseDrill struct {
	t     *testing.T
	h     *e2etest.Harness
	store *state.PgStore
	key   string
}

func (d *safeReleaseDrill) createApp(t *testing.T, slug string) {
	t.Helper()
	falsy := false
	if got := postOK(t, d.h, d.key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app %s: status=%d", slug, got)
	}
}

// deployLive submits a deployment and waits until it is live. A canary
// candidate is live while it still serves only its first stage.
func (d *safeReleaseDrill) deployLive(t *testing.T, slug string, req api.CreateDeploymentRequest) state.Deployment {
	t.Helper()
	raw, status := doReq(t, d.h, d.key, http.MethodPost, "/v1/apps/"+slug+"/deployments", req)
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", slug, status, raw)
	}
	var resp api.DeploymentResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, raw)
	}
	return d.waitDeployment(t, resp.ID, 3*time.Minute, func(dep state.Deployment) bool { return dep.Status == state.DeployLive })
}

func (d *safeReleaseDrill) waitDeployment(t *testing.T, id string, timeout time.Duration, done func(state.Deployment) bool) state.Deployment {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last state.Deployment
	for {
		dep, err := d.store.DeploymentByID(context.Background(), id)
		if err == nil {
			last = dep
			if done(dep) {
				return dep
			}
			if dep.Status == state.DeployFailed {
				t.Fatalf("deployment %s failed: %s", id, dep.Error)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("deployment %s did not reach the expected state in %s: status=%s rollout=%q traffic=%d reason=%q err=%v",
				id, timeout, last.Status, last.RolloutState, last.TrafficPercent, last.RolloutAbortedReason, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func (d *safeReleaseDrill) logBreakerInput(t *testing.T, appID, candidateID, stableID string, since time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	cr, c5, cp95, _, _, _, _, cErr := d.store.RequestTelemetryCircuitBreakerSummary(ctx, appID, candidateID, since, now)
	sr, s5, sp95, _, _, _, _, sErr := d.store.RequestTelemetryCircuitBreakerSummary(ctx, appID, stableID, since, now)
	t.Logf("DRILL breaker_input candidate=%d/%d p95=%.0f stable=%d/%d p95=%.0f errs=%v/%v",
		c5, cr, cp95, s5, sr, sp95, cErr, sErr)
}

func (d *safeReleaseDrill) waitLease(ready bool, timeout time.Duration) {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got, err := d.store.SafeReleaseWorkerLeaseReady(context.Background())
		if err == nil && got == ready {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("safe-release worker lease ready=%v after %s, want %v (err=%v)", got, timeout, ready, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func (d *safeReleaseDrill) host(slug string) string { return slug + ".apps.test.example" }

func (d *safeReleaseDrill) get(slug, path string) int {
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(gatewayAppURL(d.h, slug), "/")+path, nil)
	if err != nil {
		return 0
	}
	req.Host = d.host(slug)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := d.h.HTTPClient().Do(req.WithContext(ctx))
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (d *safeReleaseDrill) waitServing(t *testing.T, slug string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if status := d.get(slug, "/"); status == http.StatusOK {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("%s not serving after %s: last status %d", slug, timeout, status)
		}
		time.Sleep(time.Second)
	}
}

// sendTraffic sends sequential requests to path until stop is called.
func (d *safeReleaseDrill) sendTraffic(slug, path string, every time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			d.get(slug, path)
			time.Sleep(every)
		}
	}()
	return func() { close(done); wg.Wait() }
}

// safeReleasePromStub answers the circuit breaker's PromQL from real scrapes.
// It supports exactly two query shapes; anything else gets an empty vector,
// which the PromQL client reports as no data.
type safeReleasePromStub struct {
	server  *httptest.Server
	mu      sync.Mutex
	targets []string
	scrapes []promScrape
}

type promScrape struct {
	at     time.Time
	series map[string]promSeries
}

type promSeries struct {
	name   string
	labels map[string]string
	value  float64
}

var (
	promCountQuery    = regexp.MustCompile(`^\(count\(\{__name__=~"\.\*_([a-z_]+)"\}\) or vector\(0\)\)$`)
	promIncreaseQuery = regexp.MustCompile(`^\(sum\(increase\(\{__name__=~"\.\*_([a-z_]+)",(.*)\}\[(\d+)s\]\)\) or vector\(0\)\)$`)
	promMatcher       = regexp.MustCompile(`([a-z_]+)="((?:[^"\\]|\\.)*)"`)
	promSampleLine    = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})?\s+(\S+)`)
	promFamilies      = []string{"_liveness_restarts_total", "_workload_oom_kills_total", "_service_dependency_calls_total"}
)

func newSafeReleasePromStub(t *testing.T) *safeReleasePromStub {
	s := &safeReleasePromStub{}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveQuery))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scrape()
			}
		}
	}()
	t.Cleanup(func() { cancel(); s.server.Close() })
	return s
}

func (s *safeReleasePromStub) URL() string { return s.server.URL }

func (s *safeReleasePromStub) setTargets(targets ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets = targets
}

func (s *safeReleasePromStub) scrape() {
	s.mu.Lock()
	targets := append([]string(nil), s.targets...)
	s.mu.Unlock()
	snapshot := promScrape{at: time.Now(), series: map[string]promSeries{}}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, target := range targets {
		resp, err := client.Get(target)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		for _, line := range strings.Split(string(body), "\n") {
			series, ok := parsePromSample(line)
			if ok {
				snapshot.series[series.key()] = series
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scrapes = append(s.scrapes, snapshot)
	if cutoff := time.Now().Add(-15 * time.Minute); len(s.scrapes) > 0 && s.scrapes[0].at.Before(cutoff) {
		s.scrapes = s.scrapes[1:]
	}
}

func parsePromSample(line string) (promSeries, bool) {
	m := promSampleLine.FindStringSubmatch(line)
	if m == nil {
		return promSeries{}, false
	}
	tracked := false
	for _, family := range promFamilies {
		tracked = tracked || strings.HasSuffix(m[1], family)
	}
	value, err := strconv.ParseFloat(m[3], 64)
	if !tracked || err != nil {
		return promSeries{}, false
	}
	labels := map[string]string{}
	for _, lm := range promMatcher.FindAllStringSubmatch(m[2], -1) {
		labels[lm[1]] = lm[2]
	}
	return promSeries{name: m[1], labels: labels, value: value}, true
}

func (p promSeries) key() string {
	keys := make([]string, 0, len(p.labels))
	for k := range p.labels {
		keys = append(keys, k+"="+p.labels[k])
	}
	sort.Strings(keys)
	return p.name + "{" + strings.Join(keys, ",") + "}"
}

func (s *safeReleasePromStub) serveQuery(w http.ResponseWriter, r *http.Request) {
	value, ok := s.evaluate(r.URL.Query().Get("query"))
	result := "[]"
	if ok {
		result = fmt.Sprintf(`[{"metric":{},"value":[%d,"%g"]}]`, time.Now().Unix(), value)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
}

func (s *safeReleasePromStub) evaluate(query string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.scrapes) == 0 {
		return 0, false
	}
	latest := s.scrapes[len(s.scrapes)-1]
	if m := promCountQuery.FindStringSubmatch(query); m != nil {
		count := 0
		for _, series := range latest.series {
			if strings.HasSuffix(series.name, "_"+m[1]) {
				count++
			}
		}
		return float64(count), true
	}
	m := promIncreaseQuery.FindStringSubmatch(query)
	if m == nil {
		return 0, false
	}
	want := map[string]string{}
	for _, lm := range promMatcher.FindAllStringSubmatch(m[2], -1) {
		want[lm[1]] = lm[2]
	}
	windowSeconds, _ := strconv.Atoi(m[3])
	since := latest.at.Add(-time.Duration(windowSeconds) * time.Second)
	base := latest
	for _, scrape := range s.scrapes {
		if !scrape.at.Before(since) {
			base = scrape
			break
		}
	}
	total := 0.0
	for key, series := range latest.series {
		if !strings.HasSuffix(series.name, "_"+m[1]) || !promLabelsMatch(series.labels, want) {
			continue
		}
		start := 0.0
		if before, ok := base.series[key]; ok {
			start = before.value
		}
		if delta := series.value - start; delta > 0 {
			total += delta
		}
	}
	return total, true
}

func promLabelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}
