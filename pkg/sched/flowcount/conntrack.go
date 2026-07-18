package flowcount

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Runner is the local exec surface. pkg/wire.ExecRunner satisfies it
// structurally; tests provide a fake.
type Runner interface {
	Output(ctx context.Context, argv []string) ([]byte, error)
}

// DefaultTTL matches schedd's reaper tick (pkg/sched/loop.go: reaperT = 10 s).
// The reader never refreshes more often than this — every Warm within the TTL
// of the previous one is a no-op against the runner.
const DefaultTTL = 10 * time.Second

// DefaultBinPath is the Ubuntu 24.04 location of conntrack from the
// conntrack-tools package. Override via the WithBinPath option.
const DefaultBinPath = "/usr/sbin/conntrack"

// Option configures a Reader at construction time.
type Option func(*Reader)

// WithBinPath overrides the conntrack binary path. Useful for tests and for
// non-standard paths on custom images.
func WithBinPath(p string) Option {
	return func(r *Reader) { r.binPath = p }
}

// WithTTL overrides the cache lifetime. Tests use a tiny value to exercise
// the refresh path without sleeping.
func WithTTL(d time.Duration) Option {
	return func(r *Reader) { r.ttl = d }
}

// Reader is the production pkg/sched.FlowCounter. It shells out to conntrack
// on Warm (at most once per TTL) and serves Open calls from the parsed cache.
//
// Concurrency: all methods are safe for concurrent use. schedd's runReaper
// calls Open once per instance sequentially, but the mutex guards against
// future parallel reapers or external probes.
//
// Cache layout: counts is keyed by instance.ID. hostIndex is the reverse
// lookup the parser uses — it's rebuilt on every successful Warm so the
// previous tick's stale IPs don't match new traffic.
type Reader struct {
	runner  Runner
	binPath string
	ttl     time.Duration

	mu        sync.Mutex
	hostIndex map[string]string // IP -> instance.ID, rebuilt on each Warm
	counts    map[string]int64  // instance.ID -> open-flow count
	cacheAt   time.Time         // when the cache was last successfully filled
	failed    bool              // latch: true after a failed Warm, cleared on next successful Warm
}

// NewReader constructs a Reader with sensible defaults (DefaultBinPath,
// DefaultTTL). runner is typically wire.ExecRunner{}.
func NewReader(runner Runner, opts ...Option) *Reader {
	if runner == nil {
		panic("flowcount: nil runner")
	}
	r := &Reader{
		runner:  runner,
		binPath: DefaultBinPath,
		ttl:     DefaultTTL,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Warm records the current set of live instances and refreshes the cache if
// the previous Warm is older than TTL (or has never been called). schedd's
// runReaper calls this once per tick before iterating.
//
// On exec / parse / ctx error, the cache is left untouched, the failure latch
// is set, and the error is returned. Open will return (0, err) until the next
// successful Warm. This is the fail-open contract pinned by
// TestRunReaperFlowCounterErrorFailsOpen.
func (r *Reader) Warm(ctx context.Context, instances []state.Instance) error {
	r.mu.Lock()
	if !r.failed && !r.cacheAt.IsZero() && time.Since(r.cacheAt) < r.ttl {
		// Cache is fresh — but the warm list may have changed (instances
		// parked, new wakes). Rebuild the host index from the new list
		// without re-running conntrack: every instance.ID that was already
		// in counts keeps its prior count, new instances get 0 (they
		// weren't running last tick so they had no flows).
		r.hostIndex = buildHostIndex(instances)
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// Cache miss or expired: shell out and parse. Done outside the lock so
	// a slow conntrack call doesn't block other Readers — and a single
	// reader is the only production user, so contention is theoretical.
	out, err := r.runner.Output(ctx, []string{r.binPath, "-L", "-p", "tcp", "-n"})
	if err != nil {
		r.mu.Lock()
		r.failed = true
		r.mu.Unlock()
		return fmt.Errorf("flowcount: conntrack: %w", err)
	}

	counts := parseConntrack(out, r.hostIndexFor(instances))

	r.mu.Lock()
	r.hostIndex = buildHostIndex(instances)
	r.counts = counts
	r.cacheAt = time.Now()
	r.failed = false
	r.mu.Unlock()
	return nil
}

// Open returns the cached open-flow count for instanceID. See package doc for
// the fail-open contract.
func (r *Reader) Open(_ context.Context, instanceID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed {
		return 0, fmt.Errorf("flowcount: cache poisoned by prior Warm failure (fail-open)")
	}
	if r.counts == nil {
		// Warm hasn't run yet (or hasn't succeeded). Returning 0 + nil here
		// matches the "instance with no flows" semantic — the reaper still
		// applies LastRequest-only reaping, which is the safe default.
		return 0, nil
	}
	return r.counts[instanceID], nil
}

// hostIndexFor is a snapshot of the warm list's (IP -> ID) map. Used by the
// parser without holding the lock — the returned map is owned by the caller.
func (r *Reader) hostIndexFor(instances []state.Instance) map[string]string {
	return buildHostIndex(instances)
}

// buildHostIndex maps the per-instance host-side IP (10.100.x.y, see
// pkg/fcvm/alloc.go) to the instance ID. Instances with empty HostIP are
// skipped — they haven't been assigned a veth yet (WAKING / COLD_BOOTING
// states, or the SetInstanceRuntime call hasn't landed).
//
// If two instances somehow share an IP, the last one wins. The §6.2-5
// invariant (alloc_property_test.go) makes that impossible in production; the
// silent overwrite here is the safest behavior in a degraded test world.
func buildHostIndex(instances []state.Instance) map[string]string {
	idx := make(map[string]string, len(instances))
	for _, ins := range instances {
		if ins.HostIP == "" {
			continue
		}
		idx[ins.HostIP] = ins.ID
	}
	return idx
}

// parseConntrack walks the conntrack -L output and counts, per instance, the
// number of flows whose src= or dst= matches an IP in hostIndex. Bidirectional:
// a flow initiated by the instance (src match) and a flow initiated by a peer
// (dst match) both increment.
//
// Empty hostIndex → empty result. Empty input → empty result.
//
// The parser is intentionally tolerant of unknown lines (extra fields, locale
// variations, [ASSURED] markers) — anything that doesn't parse cleanly as a
// conntrack -L line is skipped, not failed. The contract is "best-effort
// count"; an unparseable line is not an error.
//
// conntrack -L -p tcp -n emits one line per flow, e.g.:
//
//	tcp      6 431999 ESTABLISHED src=10.100.0.5 dst=93.184.216.34 sport=42301 dport=443 [ASSURED] src=93.184.216.34 dst=10.100.0.5 sport=443 dport=42301
//
// Each line carries an original-direction tuple (src=/dst= before [ASSURED])
// and a reply-direction tuple (src=/dst= after). A connection is one flow
// that occupies both directions, so each matching IP increments once per
// tuple per line — an instance's outgoing flow matches as src in the
// original tuple and dst in the reply tuple (count +2 per flow).
//
// The connection-tracking state (ESTABLISHED, TIME_WAIT, …) is intentionally
// not filtered — a half-closed WebSocket or a slow-starting TLS handshake is
// the exact case where we want the instance to stay alive.
func parseConntrack(data []byte, hostIndex map[string]string) map[string]int64 {
	counts := make(map[string]int64, len(hostIndex))
	if len(hostIndex) == 0 || len(data) == 0 {
		return counts
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		for _, addr := range extractAllAddrs(line, "src=") {
			if id, ok := hostIndex[addr]; ok {
				counts[id]++
			}
		}
		for _, addr := range extractAllAddrs(line, "dst=") {
			if id, ok := hostIndex[addr]; ok {
				counts[id]++
			}
		}
	}
	return counts
}

// extractAllAddrs returns every value following the marker (e.g. "src=") on
// the line, bounded by the next whitespace or '[' (conntrack's [ASSURED] /
// [UNREPLIED] annotation). The reply-direction tuple after [ASSURED] is
// matched here too, so a single flow increments the matching instance twice
// (once per direction).
func extractAllAddrs(line []byte, marker string) []string {
	var out []string
	rest := line
	for {
		i := bytes.Index(rest, []byte(marker))
		if i < 0 {
			return out
		}
		rest = rest[i+len(marker):]
		end := len(rest)
		for j, b := range rest {
			if b == ' ' || b == '\t' || b == '[' {
				end = j
				break
			}
		}
		out = append(out, string(rest[:end]))
		// Advance past the value so the next search starts at the next marker.
		if end < len(rest) {
			rest = rest[end:]
		} else {
			return out
		}
	}
}
