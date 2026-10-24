//go:build !no_pg

package e2e_test

// TestEdgeWAFInlineLoad_E2E measures the in-path WAF check inside the real
// gatewayd-internal for the ADR-831 preview gate: gateway_waf_inline_seconds
// p95 against 2 ms, the skipped fraction, and gatewayd-internal's CPU per
// request. Each app is a preview app with a kind=waf warn rule and a
// kind=respond rule on the same host, so every request is checked in-path
// and then answered at the edge without waking a VM; the run needs
// Postgres, apid and gatewayd-internal, and no KVM.
//
// Opt-in (FAAS_E2E_WAF_LOAD=1) because it saturates the CPU on purpose.
// FAAS_E2E_WAF_LOAD_RATES (comma-separated mean req/s, default
// 100,250,500,1000), FAAS_E2E_WAF_LOAD_SECONDS per rate (default 15) and
// FAAS_E2E_WAF_LOAD_APPS (default 4) tune it. FAAS_E2E_WAF_LOAD_MODE sets
// the WAF rule's mode (warn by default; observe, or none for no WAF rule) to
// measure the same path without the in-path check. Run it on the node being
// measured, with any competing load started from a separate process.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type wafLoadRequest struct {
	path   string
	header map[string]string
	attack bool
}

// wafLoadCorpus is header and URI traffic shaped like real requests: browser
// page loads with cookies, API calls with bearer tokens, and four attacks
// the in-path rule set catches.
func wafLoadCorpus() (benign, attacks []wafLoadRequest) {
	browser := map[string]string{
		"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-GB,en;q=0.9",
		"Cookie":          "_ga=GA1.1.1234567890.1697040000; session=eyJ1aWQiOjQyfQ.c2lnbmF0dXJl; theme=dark",
		"Referer":         "https://app.example.test/dashboard?tab=overview",
	}
	client := map[string]string{
		"User-Agent":    "Go-http-client/1.1",
		"Accept":        "application/json",
		"Authorization": "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyXzQyIn0.dGhpc2lzYXNpZ25hdHVyZQ",
	}
	benign = []wafLoadRequest{
		{path: "/", header: browser},
		{path: "/static/app.3f9a1c.js", header: browser},
		{path: "/search?q=O%27Brien%27s+pub+%26+grill", header: browser},
		{path: "/auth/callback?code=4%2F0AX4XfWhq9z&state=eyJyZXR1cm4iOiIvZGFzaGJvYXJkIn0&scope=email+profile+openid", header: browser},
		{path: "/api/v1/users/42?include=profile,settings", header: client},
		{path: "/api/items?filter=price%3E10+and+price%3C100&sort=-created_at", header: client},
		{path: "/reports?from=2026-01-01T00:00:00Z&to=2026-03-31T23:59:59Z", header: client},
	}
	attacks = []wafLoadRequest{
		{path: "/api/items?q=1%27%20OR%201%3D1--", header: client, attack: true},
		{path: "/search?q=%3Cscript%3Ealert(document.cookie)%3C%2Fscript%3E", header: browser, attack: true},
		{path: "/download?file=..%2F..%2F..%2Fetc%2Fpasswd", header: client, attack: true},
		{path: "/api/items?id=1", header: map[string]string{"User-Agent": "sqlmap/1.7.2#stable (https://sqlmap.org)"}, attack: true},
	}
	return benign, attacks
}

func TestEdgeWAFInlineLoad_E2E(t *testing.T) {
	if os.Getenv("FAAS_E2E_WAF_LOAD") != "1" {
		t.Skip("set FAAS_E2E_WAF_LOAD=1 to load gatewayd-internal's in-path WAF check")
	}
	rates := envInts(t, "FAAS_E2E_WAF_LOAD_RATES", []int{100, 250, 500, 1000})
	seconds := envInts(t, "FAAS_E2E_WAF_LOAD_SECONDS", []int{15})[0]
	apps := envInts(t, "FAAS_E2E_WAF_LOAD_APPS", []int{4})[0]
	mode := os.Getenv("FAAS_E2E_WAF_LOAD_MODE")
	if mode == "" {
		mode = api.EdgeWAFModeWarn
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	releaseEdgeFeatures(t, pool)
	key := h.SeedAccount(ctx, api.PlanPro)
	hosts := seedWAFLoadApps(t, h, pool, key, apps, mode)
	resetEdgeRuleCache(t, h)

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns: 1024, MaxIdleConnsPerHost: 1024, IdleConnTimeout: 30 * time.Second,
		},
	}
	benign, attacks := wafLoadCorpus()
	// The first check compiles the in-path rule set; warm it and the
	// connection pool up before measuring.
	got := driveWAFLoad(t, client, h.EdgeURL(), hosts, benign, attacks, 100, 3*time.Second)
	if (mode == api.EdgeWAFModeWarn) != (got.attacksWarned > 0) || got.benignWarned > 0 {
		t.Fatalf("warm-up in mode %s: %d of %d attacks warned, %d benign warned; statuses %v",
			mode, got.attacksWarned, got.attacks, got.benignWarned, got.statuses)
	}
	gatewayPID := childPID(t, "gatewayd-internal")
	t.Logf("in-path WAF load: %d apps, WAF mode %s, %ds per rate, gatewayd-internal pid %d, test GOMAXPROCS %d",
		apps, mode, seconds, gatewayPID, runtime.GOMAXPROCS(0))
	for _, rate := range rates {
		before, cpuBefore := scrapeWAFInline(t, h), processCPUSeconds(gatewayPID)
		got := driveWAFLoad(t, client, h.EdgeURL(), hosts, benign, attacks, rate, time.Duration(seconds)*time.Second)
		after, cpuAfter := scrapeWAFInline(t, h), processCPUSeconds(gatewayPID)
		reportWAFLoad(t, rate, got, after.minus(before), cpuAfter-cpuBefore)
	}
}

func envInts(t *testing.T, name string, def []int) []int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	var out []int
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n <= 0 {
			t.Fatalf("%s: bad value %q", name, f)
		}
		out = append(out, n)
	}
	return out
}

// seedWAFLoadApps creates n preview apps, each answering its own host with
// a kind=respond rule behind a kind=waf rule in mode (none: no WAF rule), and
// returns the hosts.
func seedWAFLoadApps(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key string, n int, mode string) []string {
	t.Helper()
	ctx := context.Background()
	accountID := accountIDFromKey(t, ctx, pool, key)
	prodSlug := "wafload-prod"
	doReqBytes(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: prodSlug, RequireAuthn: boolPtr(false)})
	hosts := make([]string, n)
	for i := range n {
		slug := fmt.Sprintf("wafload-preview-%d", i)
		if err := insertPreviewApp(t, pool, accountID, slug, prodSlug, i+1, state.PreviewPrStateOpen); err != nil {
			t.Fatalf("insert preview %s: %v", slug, err)
		}
		app := getAppBySlug(t, pool, slug)
		hosts[i] = fmt.Sprintf("wafload-%d.apps.test.example", i)
		seedRouteSubstitute(t, ctx, pool, accountID, app.ID, hosts[i], slug)
		if mode != "none" {
			seedEdgeRuleDirect(t, ctx, pool, accountID, app.ID, hosts[i], state.EdgeRuleKindWAF,
				map[string]any{"kind": "waf", "waf": map[string]any{"mode": mode}})
		}
		seedEdgeRuleDirect(t, ctx, pool, accountID, app.ID, hosts[i], state.EdgeRuleKindRespond,
			map[string]any{"kind": "respond", "respond": map[string]any{"status_code": 200, "body": map[string]any{"ok": true}}})
	}
	return hosts
}

// wafLoadResult is what the client saw during one rate.
type wafLoadResult struct {
	mu                           sync.Mutex
	statuses                     map[int]int
	latencies                    []float64
	attacks, attacksWarned       int
	benignWarned, requestsFailed int
}

func (r *wafLoadResult) record(status int, latency time.Duration, attack, warned bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[status]++
	r.latencies = append(r.latencies, latency.Seconds())
	switch {
	case attack:
		r.attacks++
		if warned {
			r.attacksWarned++
		}
	case warned:
		r.benignWarned++
	}
}

// driveWAFLoad sends Poisson arrivals at the mean rate across hosts, one
// goroutine per request as concurrent clients would, and waits for them.
func driveWAFLoad(t *testing.T, client *http.Client, base string, hosts []string, benign, attacks []wafLoadRequest, rate int, d time.Duration) *wafLoadResult {
	t.Helper()
	res := &wafLoadResult{statuses: map[int]int{}}
	var wg sync.WaitGroup
	mean := float64(time.Second) / float64(rate)
	start := time.Now()
	next := start
	for n := 0; ; n++ {
		next = next.Add(time.Duration(rand.ExpFloat64() * mean))
		if next.After(start.Add(d)) {
			break
		}
		if wait := time.Until(next); wait > 0 {
			time.Sleep(wait)
		}
		req := benign[n%len(benign)]
		if n%20 == 10 {
			req = attacks[(n/20)%len(attacks)]
		}
		host := hosts[n%len(hosts)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sendWAFLoadRequest(client, base, host, req, res)
		}()
	}
	wg.Wait()
	return res
}

func sendWAFLoadRequest(client *http.Client, base, host string, req wafLoadRequest, res *wafLoadResult) {
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+req.path, nil)
	if err != nil {
		res.mu.Lock()
		res.requestsFailed++
		res.mu.Unlock()
		return
	}
	r.Host = host
	for k, v := range req.header {
		r.Header.Set(k, v)
	}
	began := time.Now()
	resp, err := client.Do(r)
	if err != nil {
		res.mu.Lock()
		res.requestsFailed++
		res.mu.Unlock()
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	res.record(resp.StatusCode, time.Since(began), req.attack, resp.Header.Get("X-WAF-Warning") != "")
}

// wafInlineScrape is gatewayd-internal's in-path check histogram (cumulative
// bucket counts by upper bound) and its check outcomes.
type wafInlineScrape struct {
	buckets  map[float64]float64
	outcomes map[string]float64
}

func (s wafInlineScrape) minus(o wafInlineScrape) wafInlineScrape {
	out := wafInlineScrape{buckets: map[float64]float64{}, outcomes: map[string]float64{}}
	for le, n := range s.buckets {
		out.buckets[le] = n - o.buckets[le]
	}
	for k, n := range s.outcomes {
		out.outcomes[k] = n - o.outcomes[k]
	}
	return out
}

func scrapeWAFInline(t *testing.T, h *e2etest.Harness) wafInlineScrape {
	t.Helper()
	resp, err := h.HTTPClient().Get(h.GatewayControlURL + "/metrics")
	if err != nil {
		t.Fatalf("scrape gatewayd-internal metrics: %v", err)
	}
	defer resp.Body.Close()
	out := wafInlineScrape{buckets: map[float64]float64{}, outcomes: map[string]float64{}}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, "{")
		if !ok {
			continue
		}
		labels, value, ok := strings.Cut(rest, "} ")
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			continue
		}
		switch name {
		case "gateway_waf_inline_seconds_bucket":
			le := labelValue(labels, "le")
			bound := math.Inf(1)
			if le != "+Inf" {
				if bound, err = strconv.ParseFloat(le, 64); err != nil {
					continue
				}
			}
			out.buckets[bound] += n
		case "gateway_waf_inline_checks_total":
			out.outcomes[labelValue(labels, "outcome")] += n
		}
	}
	return out
}

func labelValue(labels, name string) string {
	for _, kv := range strings.Split(labels, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// quantile interpolates within histogram buckets, as histogram_quantile
// does; a quantile beyond the last finite bound reports that bound.
func (s wafInlineScrape) quantile(q float64) float64 {
	bounds := make([]float64, 0, len(s.buckets))
	for le := range s.buckets {
		bounds = append(bounds, le)
	}
	slices.Sort(bounds)
	if len(bounds) == 0 || s.buckets[bounds[len(bounds)-1]] == 0 {
		return 0
	}
	rank := q * s.buckets[bounds[len(bounds)-1]]
	lower, below := 0.0, 0.0
	for _, le := range bounds {
		cum := s.buckets[le]
		if cum >= rank {
			if math.IsInf(le, 1) || cum == below {
				return lower
			}
			return lower + (le-lower)*(rank-below)/(cum-below)
		}
		lower, below = le, cum
	}
	return lower
}

func (s wafInlineScrape) fractionWithin(bound float64) float64 {
	total := s.buckets[math.Inf(1)]
	if total == 0 {
		return 0
	}
	return s.buckets[bound] / total
}

func reportWAFLoad(t *testing.T, rate int, got *wafLoadResult, checks wafInlineScrape, gatewayCPU float64) {
	t.Helper()
	requests := len(got.latencies) + got.requestsFailed
	checked := checks.buckets[math.Inf(1)]
	skipped := checks.outcomes["skipped"]
	slices.Sort(got.latencies)
	clientMs := func(q float64) float64 {
		if len(got.latencies) == 0 {
			return 0
		}
		return 1000 * got.latencies[min(int(q*float64(len(got.latencies))), len(got.latencies)-1)]
	}
	t.Logf("rate %5d req/s: %6d requests; in-path check p50 %.2f ms, p95 %.2f ms, p99 %.2f ms, %.1f%% within 2 ms; "+
		"skipped %.1f%%; attacks warned %d/%d, benign warned %d; client p50 %.2f ms, p95 %.2f ms; "+
		"gatewayd-internal CPU %.2f ms/request; statuses %v, failed %d",
		rate, requests, 1000*checks.quantile(0.50), 1000*checks.quantile(0.95), 1000*checks.quantile(0.99),
		100*checks.fractionWithin(0.002), 100*skipped/math.Max(checked+skipped, 1),
		got.attacksWarned, got.attacks, got.benignWarned, clientMs(0.50), clientMs(0.95),
		1000*gatewayCPU/math.Max(float64(requests), 1), got.statuses, got.requestsFailed)
	if checks.outcomes["error"] > 0 {
		t.Errorf("rate %d: %.0f in-path checks errored", rate, checks.outcomes["error"])
	}
	if got.benignWarned > 0 {
		t.Errorf("rate %d: %d benign requests warned", rate, got.benignWarned)
	}
}

// childPID finds this test process's child whose executable is named name
// (Linux only; 0 elsewhere, which turns the CPU column off).
func childPID(t *testing.T, name string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := procStatFields(string(stat))
		if len(fields) < 2 || fields[1] != strconv.Itoa(self) {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		argv0, _, _ := strings.Cut(string(cmdline), "\x00")
		if filepath.Base(argv0) == name || strings.Contains(string(cmdline), "/"+name+"\x00") {
			return pid
		}
	}
	return 0
}

// procStatFields returns /proc/<pid>/stat's fields after the command name:
// state, ppid, ... (utime and stime are indexes 11 and 12).
func procStatFields(stat string) []string {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return nil
	}
	return strings.Fields(stat[i+1:])
}

// processCPUSeconds is pid's user plus system CPU time, assuming the usual
// 100 clock ticks per second.
func processCPUSeconds(pid int) float64 {
	if pid == 0 {
		return 0
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	fields := procStatFields(string(stat))
	if len(fields) < 13 {
		return 0
	}
	utime, _ := strconv.ParseFloat(fields[11], 64)
	stime, _ := strconv.ParseFloat(fields[12], 64)
	return (utime + stime) / 100
}
