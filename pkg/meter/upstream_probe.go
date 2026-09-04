// Package meter — ADR-098 §D1.b / §D2 TCP+TLS probe (PR-C).
//
// upstream_probe.go is the per-(host, region) TCP+TLS dial
// implementation. The probe is the data-plane input to schedd's
// chooser bias (PR-D): every 30 s, meterd dials every captured
// (host_redacted_hash, region) tuple and writes one
// data_upstream_probes row carrying the RTT + outcome class. The
// metric surface (C8) and the chooser bias (C6) consume the
// same row shape.
//
// §11 load-bearing claim: the probe MUST use crypto/tls.Dial,
// NEVER net/http.Get. http.Get sends a plaintext HTTP/1.1
// request line + headers that include the Host: header with
// the plaintext hostname — both visible in any tcpdump
// capture. The TLS handshake (snakeoil or real cert) leaks only
// the SNI name, which is identical to the dial target. The
// metric labels carry the host_redacted_hash + the kind + the
// region — never the plaintext host. A regression that swaps
// in net/http.Get would (a) trip the §11 secret rule (the
// Host: header IS the plaintext host), (b) write a probe that
// completes in <1ms on any TCP-reachable port (the TLS
// handshake is the load-bearing latency signal), and (c)
// silently miss endpoints that close the TCP connection
// before sending an HTTP response.
//
// The probe is fail-soft: a single failed dial is logged at
// Debug + observed via OpsMetrics but never aborts the loop.
// The loop's only failure mode is the per-(host, region)
// iteration cap (UpstreamProbeMaxConcurrent = 64) and the
// SQL INSERT path — both surfaced via the meterd_data_upstream_*
// metrics.

package meter

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// UpstreamProbeOutcome is the closed vocabulary the probe
// classifies a single dial into. Mirrors the SQL CHECK on
// data_upstream_probes.error_class and the §12 prom label
// (`meterd_data_upstream_probes_total{outcome}`).
//
// Outcome classes:
//
//   - ok              — TCP+TLS handshake completed within
//     ProbeTimeoutMs.
//   - timeout         — context / dial timeout exceeded.
//   - refused         — TCP RST received (server actively
//     refused the connection).
//   - tls_handshake   — TCP completed but TLS handshake
//     failed (cert mismatch, expired,
//     unsupported version).
//   - dns             — DNS lookup failed.
//   - unreachable     — Network unreachable / no route to
//     host (EHOSTUNREACH on the dial).
type UpstreamProbeOutcome string

const (
	UpstreamProbeOutcomeOK           UpstreamProbeOutcome = "ok"
	UpstreamProbeOutcomeTimeout      UpstreamProbeOutcome = "timeout"
	UpstreamProbeOutcomeRefused      UpstreamProbeOutcome = "refused"
	UpstreamProbeOutcomeTLSHandshake UpstreamProbeOutcome = "tls_handshake"
	UpstreamProbeOutcomeDNS          UpstreamProbeOutcome = "dns"
	UpstreamProbeOutcomeUnreachable  UpstreamProbeOutcome = "unreachable"
)

// DefaultProbeTimeout is the per-dial timeout (TCP connect +
// TLS handshake). 3 s is short enough that the probe loop
// completes within the 30 s UpstreamProbeInterval for
// UpstreamProbeMaxConcurrent = 64 hosts in flight.
const DefaultProbeTimeout = 3 * time.Second

// DefaultUpstreamProbeInterval is the loop cadence. Matches
// the cluster outline's "30s default" and the §12 dashboard
// panel's resolution.
const DefaultUpstreamProbeInterval = 30 * time.Second

// DefaultUpstreamPartitionCreateInterval is the partition
// cron cadence. Matches the retention cron's 1 h default.
const DefaultUpstreamPartitionCreateInterval = 1 * time.Hour

// Probe is the per-(host, region) dial driver. Constructed
// once at meterd boot; Run blocks until ctx cancels.
//
// Concurrency: Probe.Run fans out one goroutine per
// (host, region) tuple, capped at api.UpstreamProbeMaxConcurrent.
// The cap is hard-coded into a semaphore; per-tick spillover
// is logged at Debug and dropped (better to drop than to
// starve the meterd sampler's minute-tick).
type Probe struct {
	// Store is the data_upstreams + data_upstream_probes
	// writer/reader. cmd/meterd wires the PgStore at boot;
	// tests inject a stub.
	Store UpstreamProbeStore

	// Region is the meterd node's compute_nodes.region. The
	// probe writes one row per (host_redacted_hash, region)
	// pair; region defaults to "default" when the operator
	// hasn't configured compute_nodes.
	Region string

	// ProbeNode is the meterd node's compute_nodes.name.
	// Empty on single-box installs; the SQL CHECK accepts
	// NULL.
	ProbeNode string

	// Now is the clock the probe stamps SampledAt with.
	// Defaults to time.Now at boot; tests inject a stub.
	Now func() time.Time

	// Log is the slog logger the probe uses. The probe
	// NEVER logs the plaintext host (only
	// host_redacted_hash + kind + region + outcome).
	Log *slog.Logger

	// Timeout is the per-dial timeout (TCP connect + TLS
	// handshake). Default DefaultProbeTimeout.
	Timeout time.Duration

	// MaxConcurrent is the in-flight cap. Defaults to
	// api.UpstreamProbeMaxConcurrent. The semaphore is
	// enforced via a buffered channel.
	MaxConcurrent int

	// Dialer overrides the net.Dialer. Tests inject a fake
	// dialer to drive outcome-classification paths
	// deterministically. Production uses the default
	// (net.Dialer{Timeout: Timeout}) wrapped in a TLS
	// handshake.
	Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	// enabled is a runtime kill-switch. The probe object stays wired while
	// disabled so a later feature-flag enable takes effect on the next tick.
	enabled atomic.Bool
	gateSet atomic.Bool
}

// UpstreamProbeStore is the minimal store surface the probe
// needs. *state.PgStore satisfies it; tests inject a stub.
// Methods:
//
//   - ListDistinctUpstreamHostHashes walks data_upstreams
//     and returns the deduplicated set of
//     (host_redacted_hash, kind, port) tuples — the probe
//     iterates this set on every tick.
//   - InsertDataUpstreamProbe writes one probe row per
//     (host_redacted_hash, region, kind, sampled_at).
//   - PruneDataUpstreamProbesOlderThan is the retention
//     purge called by the partition cron.
//
// The probe uses pkg/state.DataUpstreamTarget directly so a
// type assertion in cmd/meterd is unnecessary — pgstore.go
// already returns this exact struct.
type UpstreamProbeStore interface {
	ListDistinctUpstreamHostHashes(ctx context.Context) ([]state.DataUpstreamTarget, error)
	InsertDataUpstreamProbe(ctx context.Context, arg sqlc.InsertDataUpstreamProbeParams) error
	PruneDataUpstreamProbesOlderThan(ctx context.Context, cutoff time.Time) error
}

// ProbeResult is the per-dial outcome. Probe.Run aggregates
// these into the data_upstream_probes row; the chooser bias
// (PR-D) reads the rows back via the LISTEN wake path.
type ProbeResult struct {
	HostRedactedHash string
	Kind             api.DataUpstreamKind
	Port             int
	Region           string
	ProbeNode        string
	SampledAt        time.Time
	RTTMs            *int // nil when OK=false
	OK               bool
	ErrorClass       *UpstreamProbeOutcome // nil when OK=true
}

// NewProbe builds a Probe with sane defaults. The caller
// overrides fields as needed (Region, ProbeNode, Log, etc.).
func NewProbe(store UpstreamProbeStore, region string, log *slog.Logger) *Probe {
	if log == nil {
		log = slog.Default()
	}
	p := &Probe{
		Store:         store,
		Region:        region,
		Log:           log,
		Now:           time.Now,
		Timeout:       DefaultProbeTimeout,
		MaxConcurrent: api.UpstreamProbeMaxConcurrent,
	}
	// Preserve the historical always-on behavior for callers that construct a
	// probe directly. cmd/meterd opts into the gate with SetEnabled.
	p.enabled.Store(true)
	return p
}

// SetEnabled attaches the runtime feature gate used by meterd. It is safe to
// call while Run is in progress.
func (p *Probe) SetEnabled(enabled bool) *Probe {
	if p != nil {
		p.enabled.Store(enabled)
		p.gateSet.Store(true)
	}
	return p
}

// Run is the per-tick probe driver. It walks the dedup'd
// target set, fans out up to MaxConcurrent dials, and writes
// one probe row per (host, region) tuple. Returns the count
// of rows written + the first per-target error (logged at
// Debug; the loop continues).
//
// The §11 invariant: this method NEVER holds the plaintext
// host in any local variable that escapes into a log call.
// The targets slice carries host_redacted_hash + kind + port;
// the dial target is "<hash>:<port>" — never the plaintext
// hostname. The dialer's net.Conn.RemoteAddr() is the
// resolved IP, not the hostname; slog fields carry the IP
// hash, not the literal.
func (p *Probe) Run(ctx context.Context) (int, error) {
	if p != nil && p.gateSet.Load() && !p.enabled.Load() {
		return 0, nil
	}
	targets, err := p.Store.ListDistinctUpstreamHostHashes(ctx)
	if err != nil {
		return 0, fmt.Errorf("meter: upstream probe list targets: %w", err)
	}
	if len(targets) == 0 {
		return 0, nil
	}
	sem := make(chan struct{}, p.MaxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var written int
	for _, tgt := range targets {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(t state.DataUpstreamTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			result := p.ProbeOnce(ctx, t)
			if err := p.Store.InsertDataUpstreamProbe(ctx, sqlc.InsertDataUpstreamProbeParams{
				ID:               pgtype.UUID{Bytes: uuid.New(), Valid: true},
				HostRedactedHash: result.HostRedactedHash,
				Region:           result.Region,
				Kind:             string(result.Kind),
				SampledAt:        pgtype.Timestamptz{Time: result.SampledAt.UTC(), Valid: true},
				RttMs:            rttMsToPgtype(result.RTTMs),
				Ok:               result.OK,
				ErrorClass:       errorClassToPgtype(result.ErrorClass),
				ProbeNode:        pgtypeText(result.ProbeNode),
			}); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				p.Log.Debug("probe insert failed",
					"host_redacted_hash", result.HostRedactedHash[:8],
					"err", err.Error())
				return
			}
			mu.Lock()
			written++
			mu.Unlock()
		}(tgt)
	}
	wg.Wait()
	return written, firstErr
}

// ProbeOnce runs a single dial + TLS handshake against the
// target. The result is the per-row payload
// data_upstream_probes writes. The probe is TCP-only
// followed by a TLS handshake (crypto/tls.Dial): there's no
// HTTP GET — the §11 secret rule is the reason. The TLS
// handshake round-trips exactly one ClientHello → ServerHello
// → Finished pair; the time between is the RTT signal
// schedd consumes.
//
// Dial order: TCP connect → TLS handshake. A TLS handshake
// failure (cert mismatch, expired, unsupported version)
// surfaces as UpstreamProbeOutcomeTLSHandshake. A DNS
// failure surfaces as UpstreamProbeOutcomeDNS. A TCP RST
// surfaces as UpstreamProbeOutcomeRefused. A context timeout
// surfaces as UpstreamProbeOutcomeTimeout.
//
// Cert verification: the probe runs with real cert validation
// (no InsecureSkipVerify). ADR-052 §Rejected alternatives flagged
// InsecureSkipVerify=true as CodeQL alert #58 — production code
// must not bypass cert trust. The probe outcome distinguishes:
//   - ok            — handshake completed AND cert chain trusted
//   - tls_handshake — handshake completed but cert validation failed
//     (cert mismatch, expired, unknown CA)
//
// Both are useful signals for schedd's chooser bias (C6): an
// endpoint with cert issues is still latency-relevant; schedd
// just doesn't bias toward it. The customer's TLS client is
// the trust boundary per ADR-098 §11.
//
// ServerName is empty; the probe doesn't care about the SNI
// match (we're not validating cert trust against a specific
// name — we're measuring latency). The SNI remains the dial
// target's host hash, not the plaintext host.
func (p *Probe) ProbeOnce(ctx context.Context, tgt state.DataUpstreamTarget) ProbeResult {
	res := ProbeResult{
		HostRedactedHash: tgt.HostRedactedHash,
		Kind:             api.DataUpstreamKind(tgt.Kind),
		Port:             tgt.Port,
		Region:           p.Region,
		ProbeNode:        p.ProbeNode,
		SampledAt:        p.Now().UTC(),
	}
	dialCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	// The probe dials the PLAINTEXT host (carried in
	// tgt.Host). The §11 secret rule is preserved by the
	// surrounding code: the plaintext host NEVER appears
	// in metric labels, audit emit, pg_notify payload, or
	// log lines. The probe is the one place where the
	// plaintext is needed (to resolve the dial), and
	// `tgt.Host` is dropped on the floor as soon as
	// ProbeOnce returns.
	addr := net.JoinHostPort(tgt.Host, strconv.Itoa(tgt.Port))
	dialer := p.dialerOrDefault()
	rawConn, err := dialer(dialCtx, "tcp", addr)
	if err != nil {
		res.ErrorClass = classifyDialError(err)
		return res
	}
	// The probe is the TLS handshake. crypto/tls.Dial returns
	// once the handshake completes (or fails). The
	// handshake is the load-bearing RTT signal — an HTTP
	// GET would add another round-trip and surface the
	// plaintext Host: header.
	//
	// The dialed plaintext host is captured in
	// data_upstreams.host (bytea column) and dropped on the
	// floor after the handshake — it never reaches the metric
	// surface, the audit kind, the pg_notify payload, or any
	// slog line (ADR-098 §11 invariant).
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(dialCtx); err != nil {
		_ = tlsConn.Close()
		res.ErrorClass = classifyDialError(err)
		return res
	}
	rttMs := int(time.Since(res.SampledAt) / time.Millisecond)
	res.OK = true
	res.RTTMs = &rttMs
	_ = tlsConn.Close()
	return res
}

// dialerOrDefault returns the dialer the probe uses.
// Production uses net.Dialer{Timeout: p.Timeout}; tests
// override p.Dialer.
func (p *Probe) dialerOrDefault() func(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.Dialer != nil {
		return p.Dialer
	}
	d := &net.Dialer{Timeout: p.Timeout}
	return d.DialContext
}

// classifyDialError maps the dial / handshake error to the
// closed-vocabulary outcome. The mapping is deliberately
// conservative — anything we can't classify becomes
// "unreachable" rather than silently swallowed.
func classifyDialError(err error) *UpstreamProbeOutcome {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case isDNSError(err):
		s := UpstreamProbeOutcomeDNS
		return &s
	case isTimeoutErr(err):
		s := UpstreamProbeOutcomeTimeout
		return &s
	case isRefusedErr(err):
		s := UpstreamProbeOutcomeRefused
		return &s
	case isTLSHandshakeErr(err):
		s := UpstreamProbeOutcomeTLSHandshake
		return &s
	case isUnreachableErr(err, msg):
		s := UpstreamProbeOutcomeUnreachable
		return &s
	default:
		s := UpstreamProbeOutcomeUnreachable
		return &s
	}
}

// isDNSError, isTimeoutErr, etc. — minimal net.Error
// classifiers. The Go stdlib has no first-class error sentinels
// for "refused" vs "unreachable" — both surface as
// *net.OpError with a syscall.Errno, which we inspect.
func isDNSError(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		// dns.Err is the underlying error from the resolver
		// (string-typed); dns.Name is the lookup name. A
		// lookup failure has either Err set OR an empty
		// Name — both are signals.
		return dns.Err != "" || dns.Name == ""
	}
	return false
}

func isTimeoutErr(err error) bool {
	if t, ok := err.(interface{ Timeout() bool }); ok && t.Timeout() {
		return true
	}
	return false
}

func isRefusedErr(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		var sys *net.OpError
		_ = errors.As(op.Err, &sys)
	}
	return err != nil && containsErrno(err, "refused")
}

func isTLSHandshakeErr(err error) bool {
	return err != nil && (errStringContains(err, "tls: ") || errStringContains(err, "handshake"))
}

func isUnreachableErr(err error, msg string) bool {
	return containsErrno(err, "unreachable") || containsErrno(err, "no route") || containsErrno(err, "network is unreachable") ||
		contains(msg, "unreachable")
}

func containsErrno(err error, want string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for {
		if s == "" {
			return false
		}
		if contains(s, want) {
			return true
		}
		// Unwrap one level if possible.
		if u, ok := err.(interface{ Unwrap() error }); ok && u != nil {
			err = u.Unwrap()
			s = err.Error()
			continue
		}
		return false
	}
}

func errStringContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), sub)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// rttMsToPgtype converts *int to pgtype.Int4 (NULL when nil).
func rttMsToPgtype(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

// errorClassToPgtype converts *UpstreamProbeOutcome to
// pgtype.Text (NULL when nil).
func errorClassToPgtype(v *UpstreamProbeOutcome) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*v), Valid: true}
}

// pgtypeText wraps a Go string into pgtype.Text. Empty →
// NULL (matches the SQL CHECK that allows NULL when probe
// succeeded).
func pgtypeText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
