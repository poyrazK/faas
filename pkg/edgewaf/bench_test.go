package edgewaf

// Benchmarks and an opt-in load run for the ADR-831 step-1 cost gate: worker
// CPU per inspection by body size and paranoia level, and how the node-wide
// pool behaves (queued, sampled out, dropped) when matched traffic exceeds
// it. Run on the reference node; numbers from a laptop are indicative only.
//
//	go test ./pkg/edgewaf/ -run '^$' -bench . -benchtime 3s
//	FAAS_WAF_LOAD=1 go test ./pkg/edgewaf/ -run TestInspectorLoad -v

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// jsonBody returns a JSON object of roughly n bytes shaped like an API
// payload (many short string and number fields), with attack placed in one
// field when non-empty. A body cut to the inspection cap is not valid JSON,
// which is the realistic case for bodies above the cap.
func jsonBody(n int, attack string) string {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		note := "regular order note for item " + strconv.Itoa(i)
		if i == 3 && attack != "" {
			note = attack
		}
		fmt.Fprintf(&b, `{"id":%d,"sku":"SKU-%06d","qty":%d,"price":"%d.99","note":%q}`, i, i, i%7+1, i%50+1, note)
	}
	b.WriteString(`]}`)
	return b.String()[:n]
}

func benchSample(paranoiaLevel int, body string) gateway.WAFSample {
	s := sample(http.MethodPost, "/api/orders?page=2&sort=created", nil, body)
	s.ParanoiaLevel = paranoiaLevel
	s.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2lnbmF0dXJl")
	s.Header.Set("Cookie", "session=6f1c2d3e4b5a69788796a5b4c3d2e1f0; theme=dark")
	s.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	return s
}

func BenchmarkEvaluate(b *testing.B) {
	inspector := New(&recordingObserver{}, nil)
	for _, pl := range []int{1, 2} {
		waf, err := inspector.engine(pl)
		if err != nil {
			b.Fatalf("compile PL%d: %v", pl, err)
		}
		for _, size := range []int{0, 1024, api.EdgeWAFDefaultInspectBodyBytes, 32 * 1024, api.MaxEdgeWAFInspectBodyBytes} {
			for _, kind := range []struct{ name, attack string }{
				{"benign", ""},
				{"attack", "1' UNION SELECT password FROM users--"},
			} {
				body := ""
				if size > 0 {
					body = jsonBody(size, kind.attack)
				}
				s := benchSample(pl, body)
				b.Run(fmt.Sprintf("PL%d/body=%dKiB/%s", pl, size/1024, kind.name), func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						if _, err := evaluate(waf, s); err != nil {
							b.Fatal(err)
						}
					}
					if size > 0 {
						b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(size), "ns/body-byte")
					}
				})
			}
		}
	}
}

// BenchmarkEvaluateBodyShape separates body size from field count: CRS runs
// its rules once per parsed argument, so many small JSON fields may cost far
// more than one long string of the same size.
func BenchmarkEvaluateBodyShape(b *testing.B) {
	waf, err := New(&recordingObserver{}, nil).engine(1)
	if err != nil {
		b.Fatal(err)
	}
	size := api.EdgeWAFDefaultInspectBodyBytes
	for _, shape := range []struct{ name, body, contentType string }{
		{"json-one-field", `{"text":"` + strings.Repeat("lorem ipsum ", size/12) + `"}`, "application/json"},
		{"json-many-fields", jsonBody(size, ""), "application/json"},
		{"text-plain", strings.Repeat("lorem ipsum ", size/12), "text/plain"},
	} {
		s := benchSample(1, shape.body)
		s.Header.Set("Content-Type", shape.contentType)
		b.Run(shape.name, func(b *testing.B) {
			for range b.N {
				if _, err := evaluate(waf, s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestInspectorLoad drives the real worker pool for a fixed time with
// matched traffic from many apps and reports what the pool did with it. It
// is opt-in (FAAS_WAF_LOAD=1) because it saturates the CPU on purpose.
//
// FAAS_WAF_LOAD_APPS, FAAS_WAF_LOAD_RPS_PER_APP, FAAS_WAF_LOAD_BODY_BYTES
// and FAAS_WAF_LOAD_SECONDS tune the run.
func TestInspectorLoad(t *testing.T) {
	if os.Getenv("FAAS_WAF_LOAD") != "1" {
		t.Skip("set FAAS_WAF_LOAD=1 to run the WAF inspector load test")
	}
	env := func(name string, def int) int {
		if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
			return v
		}
		return def
	}
	apps := env("FAAS_WAF_LOAD_APPS", 20)
	rpsPerApp := env("FAAS_WAF_LOAD_RPS_PER_APP", 50)
	bodyBytes := env("FAAS_WAF_LOAD_BODY_BYTES", api.EdgeWAFDefaultInspectBodyBytes)
	seconds := env("FAAS_WAF_LOAD_SECONDS", 20)

	obs := &recordingObserver{}
	i := New(obs, nil)
	for pl := 1; pl <= api.MaxEdgeWAFParanoiaLevel; pl++ {
		if _, err := i.engine(pl); err != nil {
			t.Fatalf("compile PL%d: %v", pl, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { i.Run(ctx); close(done) }()

	body := jsonBody(bodyBytes, "")
	attackBody := jsonBody(bodyBytes, "<script>alert(document.cookie)</script>")
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var wg sync.WaitGroup
	submitted := make([]int, apps)
	for a := range apps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(time.Second / time.Duration(rpsPerApp))
			defer tick.Stop()
			for n := 0; time.Now().Before(deadline); n++ {
				<-tick.C
				s := benchSample(1, body)
				if n%20 == 0 {
					s = benchSample(1, attackBody)
				}
				s.AppID = "app-" + strconv.Itoa(a)
				i.Submit(s)
				submitted[a]++
			}
		}()
	}
	wg.Wait()
	// Let queued samples drain before reading the outcome counters.
	for drain := time.Now().Add(30 * time.Second); len(i.queue) > 0 && time.Now().Before(drain); {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	total := 0
	for _, n := range submitted {
		total += n
	}
	inspected := obs.count(OutcomeClean) + obs.count(OutcomeDetected)
	pct := func(n int) float64 { return 100 * float64(n) / float64(max(total, 1)) }
	t.Logf("load: %d apps x %d req/s for %ds, %d-byte bodies, %d workers", apps, rpsPerApp, seconds, bodyBytes, api.EdgeWAFWorkers)
	t.Logf("submitted %d (%.0f/s)", total, float64(total)/float64(seconds))
	t.Logf("inspected %d (%.1f%%, %.0f/s): clean %d, detected %d",
		inspected, pct(inspected), float64(inspected)/float64(seconds), obs.count(OutcomeClean), obs.count(OutcomeDetected))
	t.Logf("sampled_out %d (%.1f%%), dropped %d (%.1f%%), error %d",
		obs.count(OutcomeSampledOut), pct(obs.count(OutcomeSampledOut)),
		obs.count(OutcomeDropped), pct(obs.count(OutcomeDropped)), obs.count(OutcomeError))
	if obs.count(OutcomeError) > 0 {
		t.Errorf("%d inspections errored", obs.count(OutcomeError))
	}
}

// inlineAttacks are header and URI attacks the in-path rule set must catch.
func inlineAttacks() []gateway.WAFSample {
	return []gateway.WAFSample{
		sample(http.MethodGet, "/api/items?q=1%27%20OR%201%3D1--", nil, ""),
		sample(http.MethodGet, "/search?q=%3Cscript%3Ealert(document.cookie)%3C%2Fscript%3E", nil, ""),
		sample(http.MethodGet, "/download?file=..%2F..%2F..%2Fetc%2Fpasswd", nil, ""),
		sample(http.MethodGet, "/ping?host=127.0.0.1%3Bcat%20%2Fetc%2Fpasswd", nil, ""),
		sample(http.MethodGet, "/login?next=%0d%0aSet-Cookie:%20session=attacker", nil, ""),
		withUA(sample(http.MethodGet, "/api/items?id=1", nil, ""), "sqlmap/1.7.2#stable (https://sqlmap.org)"),
	}
}

// TestInlineLoad drives CheckInline the way gatewayd-internal does, one
// goroutine per request, with Poisson arrivals at fixed mean rates, for the
// ADR-831 preview gate (gateway_waf_inline_seconds p95 under 2 ms). Per rate
// it reports check-time percentiles, process CPU per check (wall time well
// above it means the checks waited for a CPU), GC cycles, the skipped
// fraction and the detection rate. It is opt-in (FAAS_WAF_INLINE_LOAD=1)
// because it saturates the CPU on purpose. To measure contention, load the
// node from a separate process: a goroutine spinning in this one holds a Go
// scheduler slot for 10 ms at a time, which no tenant VM does.
//
// FAAS_WAF_INLINE_RATES (comma-separated mean req/s across all apps,
// default 100,500,1000,2000), FAAS_WAF_INLINE_APPS (default 10) and
// FAAS_WAF_INLINE_SECONDS per rate (default 10) tune the run;
// FAAS_WAF_INLINE_SLOTS and FAAS_WAF_INLINE_WAIT_US override the shipped
// slot count and slot wait, to compare sizings on one node (amendment 12).
//
//	FAAS_WAF_INLINE_LOAD=1 go test ./pkg/edgewaf/ -run TestInlineLoad -v
func TestInlineLoad(t *testing.T) {
	if os.Getenv("FAAS_WAF_INLINE_LOAD") != "1" {
		t.Skip("set FAAS_WAF_INLINE_LOAD=1 to run the in-path WAF load test")
	}
	env := func(name string, def int) int {
		if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
			return v
		}
		return def
	}
	rates := []int{100, 500, 1000, 2000}
	if v := os.Getenv("FAAS_WAF_INLINE_RATES"); v != "" {
		rates = rates[:0]
		for _, f := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil || n <= 0 {
				t.Fatalf("FAAS_WAF_INLINE_RATES: bad rate %q", f)
			}
			rates = append(rates, n)
		}
	}
	apps := max(env("FAAS_WAF_INLINE_APPS", 10), 1)
	seconds := max(env("FAAS_WAF_INLINE_SECONDS", 10), 1)
	shipped := New(&recordingObserver{}, nil)
	slots := max(env("FAAS_WAF_INLINE_SLOTS", shipped.InlineSlots()), 1)
	wait := time.Duration(env("FAAS_WAF_INLINE_WAIT_US", int(shipped.inlineWait/time.Microsecond))) * time.Microsecond

	var benign []gateway.WAFSample
	for _, br := range benignCorpus() {
		if !br.content && br.inPathFP == nil {
			benign = append(benign, br.s)
		}
	}
	t.Logf("in-path load: %d apps, %ds per rate, GOMAXPROCS %d, %d check slots, %s slot wait, %d ms/s per app (burst %d)",
		apps, seconds, runtime.GOMAXPROCS(0), slots, wait, api.EdgeWAFInlineMsPerAppPerSecond, api.EdgeWAFInlineMsPerAppBurst)
	for _, rate := range rates {
		runInlineLoad(t, rate, apps, seconds, slots, wait, benign, inlineAttacks())
	}
}

// inlineLoadStats collects one rate's outcomes.
type inlineLoadStats struct {
	mu                         sync.Mutex
	checked                    []float64
	outcomes                   map[string]int
	attacksSent, attacksCaught int
	benignFlagged              int
}

func (st *inlineLoadStats) record(res gateway.WAFInlineResult, attack bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.outcomes[res.Outcome]++
	if res.Outcome != gateway.WAFInlineSkipped {
		st.checked = append(st.checked, res.Seconds)
	}
	switch {
	case attack:
		st.attacksSent++
		if res.Outcome == gateway.WAFInlineDetected {
			st.attacksCaught++
		}
	case res.Outcome == gateway.WAFInlineDetected:
		st.benignFlagged++
	}
}

func cpuSeconds(t *testing.T) float64 {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatalf("getrusage: %v", err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()).Seconds()
}

func runInlineLoad(t *testing.T, rate, apps, seconds, slots int, wait time.Duration, benign, attacks []gateway.WAFSample) {
	t.Helper()
	i := New(&recordingObserver{}, nil)
	i.inlineSlots, i.inlineWait = make(chan struct{}, slots), wait
	if _, err := i.inlineWAF(); err != nil {
		t.Fatal(err)
	}
	st := &inlineLoadStats{outcomes: map[string]int{}}
	var reqs sync.WaitGroup
	var gcBefore, gcAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&gcBefore)
	cpuBefore := cpuSeconds(t)

	// One scheduler draws exponential gaps, so arrivals are Poisson at the
	// mean rate; when a sleep overshoots, the requests that fell due are
	// sent at once, as a burst of real traffic would arrive.
	mean := float64(time.Second) / float64(rate)
	start := time.Now()
	deadline := start.Add(time.Duration(seconds) * time.Second)
	next := start
	for n := 0; ; n++ {
		next = next.Add(time.Duration(rand.ExpFloat64() * mean))
		if next.After(deadline) {
			break
		}
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		}
		attack := n%20 == 10
		s := benign[n%len(benign)]
		if attack {
			s = attacks[(n/20)%len(attacks)]
		}
		s.AppID = "app-" + strconv.Itoa(n%apps)
		reqs.Add(1)
		go func() {
			defer reqs.Done()
			st.record(i.CheckInline(s), attack)
		}()
	}
	reqs.Wait()
	cpu := cpuSeconds(t) - cpuBefore
	runtime.ReadMemStats(&gcAfter)

	total := 0
	for _, n := range st.outcomes {
		total += n
	}
	checked := st.checked
	slices.Sort(checked)
	ms := func(p float64) float64 {
		if len(checked) == 0 {
			return 0
		}
		return 1000 * checked[min(int(p*float64(len(checked))), len(checked)-1)]
	}
	pct := func(n, d int) float64 { return 100 * float64(n) / float64(max(d, 1)) }
	t.Logf("rate %5d req/s: %6d requests, check p50 %.2f ms, p95 %.2f ms, p99 %.2f ms, max %.2f ms; CPU %.2f ms per check, %d GCs; skipped %.2f%%; attacks detected %d/%d (skips included); benign flagged %d; errors %d",
		rate, total, ms(0.50), ms(0.95), ms(0.99), ms(1), 1000*cpu/float64(max(len(checked), 1)), gcAfter.NumGC-gcBefore.NumGC,
		pct(st.outcomes[gateway.WAFInlineSkipped], total), st.attacksCaught, st.attacksSent, st.benignFlagged, st.outcomes[gateway.WAFInlineError])
	if st.outcomes[gateway.WAFInlineError] > 0 {
		t.Errorf("rate %d: %d checks errored", rate, st.outcomes[gateway.WAFInlineError])
	}
}
