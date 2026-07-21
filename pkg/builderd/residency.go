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
//   - metricsResidentProbe — polls <schedd-metrics>/metrics on a 2-second
//     cadence, parses `fcvm_resident_ram_pct` (the gauge schedd exposes —
//     see pkg/fcvm/metrics.go:185). Multiplying by RAMAdmissionCeilingMB/100
//     converts it back to MB. The HTTP scrape itself is best-effort and
//     cached behind mu.Lock so a slow schedd never blocks DecideSlot.
//
// The metrics endpoint contract is published by cmd/schedd/main.go (the
// sibling daemon exposes the fcvm_* gauges on the metricsAddr from
// schedd.toml). builderd does NOT call schedd's gRPC socket — the metrics
// endpoint is shared by Prometheus already, so no extra surface is added.
package builderd

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
// 0 to mimic "no tenants resident" (grants the opportunistic slot).
func FixedResident(mb int) ResidencyProbe {
	return &fixedResidentProbe{mb: mb}
}

func (p *fixedResidentProbe) ResidentMB() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mb
}

// metricsResidentProbe polls schedd's /metrics endpoint and extracts
// fcvm_resident_ram_pct. The poll runs on a goroutine bound to ctx; the
// returned ResidencyProbe is safe for concurrent use.
type metricsResidentProbe struct {
	url string

	// mu protects mb and lastGood.
	mu       sync.Mutex
	mb       int
	lastGood time.Time

	client *http.Client
}

// NewMetricsResident returns a ResidencyProbe that polls url on a 2-second
// cadence. The poller exits when ctx is cancelled. If url is empty the
// probe returns 0 (matches the previous nil-probe behavior of "guaranteed
// slot only") — no error is returned, since an empty config just means
// "deploy without the 2nd slot until you wire schedd's metrics URL".
func NewMetricsResident(ctx context.Context, url string) ResidencyProbe {
	if url == "" {
		return FixedResident(0)
	}
	p := &metricsResidentProbe{
		url:    url,
		client: &http.Client{Timeout: residentHTTPTimeout},
	}
	// Prime before returning so the first DecideSlot has a real value.
	// Failure here is silent — return the cached zero and the background
	// loop will fill it in.
	_ = p.scrape(ctx)
	go p.loop(ctx)
	return p
}

func (p *metricsResidentProbe) ResidentMB() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mb
}

// loop runs until ctx is done, polling every residentPollInterval. Errors
// are swallowed: the cached mb stays put, which is preferable to "strip
// the 2nd slot just because schedd hiccuped once". lastGood is consulted
// (but currently unused — placeholder for a future "stale > 30 s ⇒ give up"
// alarm).
func (p *metricsResidentProbe) loop(ctx context.Context) {
	t := time.NewTicker(residentPollInterval)
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
	p.lastGood = time.Now()
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
