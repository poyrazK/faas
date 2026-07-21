// Package builderd — Residency probe implementations (spec §4.5, §13).
//
// slot.go defines the protocol: a probe reports Σ tenant residency in MB so
// DecideSlot can grant the opportunistic 2nd builder slot when tenants are
// quiet. The probe must be cheap (Decision runs every build, no I/O budget),
// non-blocking (DecideSlot blocks the build pipeline), and monotonically
// useful (builds never outrank tenant wakes).
//
// Two implementations:
//
//   - fixedResidentProbe — explicit value, used in unit tests and as the
//     "schedd unreachable at boot" fallback. Returns the value verbatim;
//     stale data is preferable to blocking.
//
//   - metricsResidentProbe — polls <schedd-metrics>/fcvm on a 2-second
//     cadence, parses `fcvm_resident_ram_pct` (the gauge schedd exposes —
//     see pkg/fcvm/metrics.go:186). Multiplying by RAMAdmissionCeilingMB/100
//     converts it back to MB. The HTTP scrape itself is best-effort and
//     cached behind mu.Lock so a slow schedd never blocks DecideSlot.
//
// The metrics endpoint contract is published by cmd/schedd/main.go (the
// sibling daemon exposes the fcvm_* gauges on metricsPath+"/fcvm" from
// schedd.toml; /metrics itself only has the daemon's own ops counters).
// builderd does NOT call schedd's gRPC socket — the metrics endpoint is
// shared by Prometheus already, so no extra surface is added.
package builderd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// residentPollInterval is how often metricsResidentProbe refreshes its cache.
// 2 s is well below the 5 s DashboardGauges TTL inside schedd, so the probe
// tracks schedd's view of the world without hammering it.
// Declared as a package var (not const) so unit tests can shorten it via
// the test-only `t.Cleanup` swap in residency_test.go and avoid sleeping
// through the production cadence.
var residentPollInterval = 2 * time.Second

// residentHTTPTimeout caps an individual scrape so a stuck schedd can never
// stall DecideSlot for more than 750 ms. The cached "last value" is returned
// on timeout/error.
const residentHTTPTimeout = 750 * time.Millisecond

// fixedResidentProbe is a ResidencyProbe that returns a constant MB value.
// Used by unit tests and as the "no schedd available" fallback when the
// configured metrics URL is empty.
type fixedResidentProbe struct {
	mu sync.Mutex
	mb int
}

// FixedResident returns a ResidencyProbe that always reports mb MB. Pass
// 0 to mimic "no tenants resident" (grants the opportunistic slot); pass
// math.MaxInt/2 to mimic "always-deny-opportunistic" (the unconfigured-URL
// fallback below uses this).
func FixedResident(mb int) ResidencyProbe {
	return &fixedResidentProbe{mb: mb}
}

func (p *fixedResidentProbe) ResidentMB() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mb
}

// denyOpportunisticResident is the fallback probe returned for an
// unconfigured ScheddMetricsURL. It reports a residency value well above
// any realistic RAMAdmissionCeilingMB so DecideSlot always lands in the
// "guaranteed-only" branch — matching the nil-probe posture in slot.go.
// We don't import api.MaxInt here to keep this file dependency-light and
// testable; math.MaxInt/2 is comfortably above api.RAMAdmissionCeilingMB
// (47,600 MB) without any future-scaling ambiguity.
const denyOpportunisticResidentMB = math.MaxInt / 2

// metricsResidentProbe polls schedd's /metrics/fcvm endpoint and extracts
// fcvm_resident_ram_pct. The poll runs on a goroutine bound to ctx; the
// returned ResidencyProbe is safe for concurrent use.
type metricsResidentProbe struct {
	url string

	// mu protects mb and healthy.
	mu      sync.Mutex
	mb      int
	healthy bool // true once at least one successful scrape has set mb

	client *http.Client
}

// NewMetricsResident returns a ResidencyProbe that polls url on a 2-second
// cadence. The poller exits when ctx is cancelled.
//
// An empty url means "no schedd metrics wired" (config not filled in yet,
// or this daemon is being run without schedd). In that case we return a
// probe that always denies the opportunistic slot — matching the nil-probe
// posture in slot.go. The operator's safe default is: "guaranteed slot only
// until you point ScheddMetricsURL at schedd", not "grant both slots and
// risk outranking tenant wakes during a partially-deployed boot".
//
// A non-empty url that points at the wrong endpoint (e.g. schedd's bare
// /metrics which doesn't expose fcvm_*) takes the same posture until the
// first successful scrape: until then, ResidentMB() returns the
// deny-opportunistic sentinel. Only after the probe has actually seen the
// gauge do we trust the cached value, including a cached 0.
func NewMetricsResident(ctx context.Context, url string) ResidencyProbe {
	p := newMetricsResident(ctx, url, true /* startLoop */)
	return p
}

// newMetricsResident is the unexported test seam. Production calls go
// through NewMetricsResident (startLoop=true); tests that only need a
// single prime-scrape can pass startLoop=false so they don't have to
// reason about the background goroutine or the residentPollInterval
// package var. url must be non-empty (the empty-URL short-circuit
// happens at NewMetricsResident).
func newMetricsResident(ctx context.Context, url string, startLoop bool) *metricsResidentProbe {
	p := &metricsResidentProbe{
		url:    url,
		client: &http.Client{Timeout: residentHTTPTimeout},
	}
	// Prime before returning so the first DecideSlot has a real value.
	// Failure leaves healthy=false; DecideSlot will then deny the
	// opportunistic slot until schedd becomes reachable.
	_ = p.scrape(ctx)
	if startLoop {
		go p.loop(ctx)
	}
	return p
}

func (p *metricsResidentProbe) ResidentMB() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.healthy {
		return denyOpportunisticResidentMB
	}
	return p.mb
}

// loop runs until ctx is done, polling every residentPollInterval. Errors
// are swallowed: the cached mb stays put, which is preferable to "strip
// the 2nd slot just because schedd hiccuped once". A future enhancement
// could deny opportunistic after some staleness threshold (e.g. > 30s
// since last good scrape); that requires threading time.Time through the
// probe struct, which is intentionally deferred to keep the current
// contract simple.
//
// residentPollInterval is captured at loop start to avoid racing with
// unit tests that swap the package var via t.Cleanup (see
// residency_test.go for the test-only swap pattern).
func (p *metricsResidentProbe) loop(ctx context.Context) {
	interval := residentPollInterval
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = p.scrape(ctx)
		}
	}
}

// scrape fetches /metrics and parses the fcvm_resident_ram_pct gauge.
// Returns nil on a clean read; any failure is logged at the caller's
// discretion (we deliberately don't slog here to keep the probe a pure
// piece of plumbing).
func (p *metricsResidentProbe) scrape(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("residency probe: %s returned %d", p.url, resp.StatusCode)
	}
	pct, err := parseResidentPct(resp.Body)
	if err != nil {
		return err
	}
	mb := int(float64(api.RAMAdmissionCeilingMB) * pct / 100.0)
	p.mu.Lock()
	p.mb = mb
	p.healthy = true
	p.mu.Unlock()
	return nil
}

// parseResidentPct greps fcvm_resident_ram_pct from the Prometheus text
// exposition format. Returns the value as a percentage 0.0–100.0.
//
// Why hand-rolled instead of prometheus.Gather: pulling the parser library
// into a hot path the unit-test suite exercises on every CI run. The metric
// is single-valued and never relabeled, so a 30-line scanner is plenty.
// If the metric name ever changes it surfaces as a clear parse error in
// the operator's logs instead of a silent zero.
func parseResidentPct(r io.Reader) (float64, error) {
	const prefix = "fcvm_resident_ram_pct"
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Skip comments (lines that start with '#').
		if strings.HasPrefix(line, "#") {
			continue
		}
		// Match either "fcvm_resident_ram_pct 12.34" or a labeled variant
		// "fcvm_resident_ram_pct{...} 12.34". Both start with the same
		// metric name; the trailing rune determines which sub-extraction
		// branch handles the value.
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		// Strip the metric name and any "{labels}" off, leaving "12.34".
		rest := strings.TrimSpace(line[len(prefix):])
		if i := strings.IndexByte(rest, '}'); i >= 0 {
			// Labeled — value comes after the closing brace + whitespace.
			rest = rest[i+1:]
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			return 0, fmt.Errorf("residency probe: parse %q: %w", line, err)
		}
		return v, nil
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("residency probe: read metrics: %w", err)
	}
	return 0, fmt.Errorf("residency probe: fcvm_resident_ram_pct not found in /metrics body")
}
