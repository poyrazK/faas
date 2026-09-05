// Command meterd — metering, billing, and quota enforcement (spec §4.7).
//
// meterd owns three timers that share one Postgres-backed state.Store:
//
//   - sample tick: every 60 s, walks every app's live instances and writes
//     one minute of billable usage (plan RAM + 8 MB) to usage_minutes.
//     The billable unit is the admission-time RAM, not the cgroup RSS —
//     spec §4.7 / CLAUDE.md invariant.
//   - quota tick: every 60 s, walks every account and applies the
//     per-plan ladder: Free at ≥100 % flips the account to suspended
//     and parks every live instance; paid plans emit a one-shot
//     quota_warning and accrue overage.
//   - billing tick: every 1 h, replays completed billable UTC-hour windows
//     from the durable lookback to the selected provider. Provider
//     idempotency keys make restart and transient-outage recovery safe.
//
// meterd is the ONLY writer that triggers Free-tier hard stops — apid's
// auth gate and schedd's ledger just observe the resulting status.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jackc/pgx/v5/pgxpool"
	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/billing"
	billingloader "github.com/onebox-faas/faas/pkg/billing/loader"
	"github.com/onebox-faas/faas/pkg/billing/reconciler"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway/egresssocket"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/safedeploy"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"github.com/onebox-faas/faas/pkg/wire"
)

// scheddCPUAdapter exposes the schedd gRPC client as a
// meter.CPUSource. The adapter is local to cmd/meterd so pkg/meter
// stays decoupled from pkg/scheddgrpc (one-way dependency the other
// way), and so a test fake can swap the client without touching
// pkg/meter's source. Issue #279 / PR-B.
//
// The adapter refreshes the per-instance snapshot on every call.
// meterd's sampler walks ~max_concurrency instances per minute, so
// the per-call cost is one gRPC ListInstanceStats round trip
// returning a slice of ~max_concurrency rows. The gRPC socket is
// on the box's local unix socket (ADR-015), so the cost is
// negligible vs. the 1-minute sampler cadence.
type scheddCPUAdapter struct {
	parker  parkInstanceParker
	now     func() time.Time
	mu      sync.Mutex
	rows    map[string]scheddgrpc.InstanceStatsRow
	fetched time.Time
}

const scheddCPUAdapterTTL = 30 * time.Second

// Provider name constants used throughout meterd. Centralised so the
// goconst linter doesn't trip when a new call site appears, and so a
// rename only touches one place.
const (
	provStripe = "stripe"
	provPaddle = "paddle"
	provPolar  = "polar"
)

func (a *scheddCPUAdapter) CPUUsageUsec(instanceID string) (uint64, bool) {
	a.refresh()
	a.mu.Lock()
	defer a.mu.Unlock()
	row, ok := a.rows[instanceID]
	if !ok {
		return 0, false
	}
	// CpuValid mirrors instancestats.Validity: 0 = Valid,
	// 1 = Unknown, 2 = Stale. The meterd sampler must NOT
	// treat the raw counter as a baseline on a non-Valid row
	// — the vmmd cpustats.Cache already absorbed the
	// regression (Unknown: it dropped the baseline) or
	// freshness budget exceeded (Stale: the reading is
	// older than the snapshot's freshness SLA). In either
	// case the next valid sample picks up from the new
	// counter; the meterd side just returns ok=false so
	// AppendUsage writes 0 cpu_usec for that minute.
	if row.CPUValid != 0 {
		return 0, false
	}
	return row.CPUUsageUsec, true
}

// refresh refreshes the in-memory snapshot if the last fetch is
// older than scheddCPUAdapterTTL. The cost is one gRPC round trip
// per minute per sampler iteration; the TTL bounds the staleness
// without forcing a fetch per instance.
func (a *scheddCPUAdapter) refresh() {
	a.mu.Lock()
	last := a.fetched
	a.mu.Unlock()
	if !last.IsZero() && a.now().Sub(last) < scheddCPUAdapterTTL {
		return
	}
	rows, err := a.parker.ListInstanceStats(context.Background())
	if err != nil {
		// Preserve the previous snapshot on error so a transient
		// gRPC failure doesn't drop the CPU data for the rest of
		// the minute. The next sample retries.
		//
		// If schedd is down for longer than ~one minute, the
		// snapshot is stale by a wider margin: the per-instance
		// CPU counters on the schedd side are advancing (or
		// wrapping on regression) but the adapter keeps
		// returning the last-known values. The per-minute
		// AppendUsage write will silently under-count until the
		// next successful refresh. This is a known
		// silent-under-count; the operator can see it via the
		// schedd /metrics and the alert pipeline (M8 row 2
		// will add a `schedd_instance_stats_collect_failures_total`
		// tripwire).
		return
	}
	m := make(map[string]scheddgrpc.InstanceStatsRow, len(rows))
	for _, r := range rows {
		m[r.InstanceID] = r
	}
	a.mu.Lock()
	a.rows = m
	a.fetched = a.now()
	a.mu.Unlock()
}

// parkInstanceParker is the slice of scheddgrpc.Client meterd actually
// uses. Slice 4 adds ParkInstance to scheddgrpc; in tests we inject a
// recording stub. Defining the interface here keeps meterd independent
// of pkg/scheddgrpc until the surface exists (ADR-019).
//
// traceID (PR-#TBD / C6) is the new scheddgrpc.Client.ParkInstance
// arg; meterd has no operator-action audit row to attribute
// (it's an automated reaper), so it always passes "" — the
// schedd-side correlation envelope stays empty and the audit
// path falls back to whatever the schedd subscriber would
// have stamped. Mirrors the EmptyEnvelopeOK pattern in the
// CLI: an empty trace_id is a no-op, not an error.
type parkInstanceParker interface {
	ParkInstance(ctx context.Context, instanceID, reason, traceID string) error
	// ListInstanceStats is the per-instance CPU-µs snapshot the
	// meterd sampler reads once per minute. Issue #279 / PR-B.
	// Returns an empty slice when schedd has no rows for this
	// tick (boot, between ticks); the sampler treats that as
	// "no CPU data this minute" and writes 0. ADR-046 (PR-2)
	// extends the returned row with NetTxBytes + TxValid so
	// the scheddEgressAdapter below can read the net_tx_bytes
	// value alongside cpu_usec on the same gRPC round trip.
	ListInstanceStats(ctx context.Context) ([]scheddgrpc.InstanceStatsRow, error)
}

// scheddEgressAdapter (ADR-046, step 8) exposes the schedd gRPC
// client as a meter.EgressSource for the net_tx_bytes column
// (root-side vethHost.rx_bytes). It reuses the scheddCPUAdapter
// 's snapshot machinery so the egress and CPU readings share a
// single gRPC round trip and refresh cadence.
//
// The tx_bytes column (gateway response bytes) is sourced from
// gatewayEgressAdapter below — the two columns are NOT the
// same data and the schedd wire only carries net_tx_bytes
// (vmmd is the canonical producer for that column; the
// gateway is the canonical producer for tx_bytes).
type scheddEgressAdapter struct {
	cpu *scheddCPUAdapter
}

func (a *scheddEgressAdapter) EgressBytes(instanceID string) (uint64, uint64, bool) {
	if a == nil || a.cpu == nil {
		return 0, 0, false
	}
	a.cpu.refresh()
	a.cpu.mu.Lock()
	defer a.cpu.mu.Unlock()
	row, ok := a.cpu.rows[instanceID]
	if !ok {
		return 0, 0, false
	}
	// TxValid mirrors instancestats.Validity: 0 = Valid, 1 =
	// Unknown (first sample / regression / netstats cache
	// miss). The meterd sampler must NOT treat a non-Valid
	// row as a baseline — the vmmd netstats.Cache already
	// absorbed the regression (Unknown: it dropped the
	// baseline). In either case the next valid sample picks
	// up from the new counter; the meterd side returns
	// ok=false so AppendUsage writes 0 net_tx_bytes for
	// that minute (mirrors the cpu path's contract above).
	if row.TxValid != 0 {
		return 0, 0, false
	}
	// txBytes = 0 (gateway column is NOT sourced from schedd;
	// gatewayEgressAdapter owns it). netTxBytes = the schedd
	// value. ok = true signals "I have a row" so the
	// sampler stamps the row's netTxBytes even when
	// netTxBytes is 0 (zero egress in this tick is a real
	// value, distinct from "no source wired").
	return 0, row.NetTxBytes, true
}

// gatewayEgressAdapter (ADR-046, step 8; PR-2 = stream consumer)
// is the meterd-side source for usage_minutes.tx_bytes
// (gateway HTTP response bytes). Production wires the gatewayd-internal
// gRPC stream
// (onebox.faas.egress.v1.EgressTxService.StreamBytes on
// FAAS_GATEWAY_EGRESS_SOCKET) into a background goroutine that
// feeds a per-(instance, minute) byte snapshot. Sample-time
// reads (EgressBytes) look up the snapshot under the read lock
// and return (txBytes, 0, true) when the gateway reported any
// rows for this instance in the current minute.
//
// Storage layout:
//
//	snapshot[instanceID][minuteUnix] = bytes
//
// minuteUnix is the truncated minute the gateway observed the
// bytes over; the meterd sampler reads only the bucket whose
// key matches the floor(now) tick. Past-minute buckets are
// retained for the duration of one sampler tick so a transient
// scheduler delay (slow Postgres) doesn't lose attribution — a
// future drain-eviction pass on meterd's resetToCurrentMinute
// helper below disposes of them.
//
// Push vs pull:
//
//	The gateway stream pushes frames on a 1 Hz cadence
//	(pkg/gateway/egressgrpc.StreamCadence). Pulling per sample
//	would either burn an extra round trip per instance-per-tick
//	(~max_concurrency × 1/min × 2 daemons) or coalesce and lose
//	per-minute fidelity. The push channel lets the gateway
//	accumulate and the dialer read on its own cadence
//	(1/min/sample) without per-tick chatter.
//
// Reconnect:
//
//	The stream goroutine reconnects with backoff on disconnect
//	(gatewayd-internal restart, transient sock churn). Worst-case
//	behaviour during the gap is ok=false on every EgressBytes
//	read → AppendUsage writes 0 to tx_bytes for that minute,
//	which surfaces as a one-minute gap in the FaasTxBytesStall
//	alert (operational, not a customer-visible bill increase).
type gatewayEgressAdapter struct {
	// now is the clock injection seam for tests; production
	// uses time.Now. Replacing it doesn't restart the stream
	// goroutine — it's read on every EgressBytes call only.
	now func() time.Time

	// tlsCfg is the mTLS config the dialFn uses when the
	// gatewayd-internal egress listener lives on a remote compute node
	// (ADR-052). Nil on the single-box default-local path —
	// the unix socket dial skips the TLS handshake by design
	// (ADR-015: group-`faas` DAC is the auth posture).
	tlsCfg *tls.Config

	mu   sync.Mutex
	data map[string]map[int64]uint64 // instanceID → minuteUnix → bytes

	// dialFn is the unix-socket dialer for the gateway
	// stream. Tests substitute a fake dialer that returns a
	// hand-rolled stream client; production wires the
	// egresspb-grpc DailContext. tlsCfg is nil on the
	// single-box default-local path; mTLS-wrapped remote
	// gatewayd-internal deployments pass the loaded *tls.Config here
	// (ADR-052).
	dialFn func(ctx context.Context, socketPath string, tlsCfg *tls.Config) (egresspb.EgressTxServiceClient, error)
}

// EgressBytes returns the latest drained (instanceID,
// currentMinute) byte count from the gateway stream. ok is
// true when the gateway reported any bytes in the current
// minute. The 0/0/false fallback for "stream not yet open,
// or gateway hasn't drained anything yet" is intentional —
// the sampler falls through to its no-source branch and
// writes 0 to BOTH egress columns, mirroring the PR-1
// contract.
//
// netTxBytes is always 0 on this path — scheddEgressAdapter
// owns the net_tx_bytes column. The aggregator picks the
// right value per column.
func (a *gatewayEgressAdapter) EgressBytes(instanceID string) (uint64, uint64, bool) {
	if a == nil || instanceID == "" {
		return 0, 0, false
	}
	minute := a.now().UTC().Truncate(time.Minute).Unix()
	a.mu.Lock()
	defer a.mu.Unlock()
	rows, ok := a.data[instanceID]
	if !ok {
		return 0, 0, false
	}
	n, ok := rows[minute]
	if !ok {
		return 0, 0, false
	}
	return n, 0, true
}

// startStream dials FAAS_GATEWAY_SYNTH_SOCKET and runs the
// StreamBytes receive loop until ctx cancels. Reconnects with
// backoff on transient errors; each received frame updates
// the snapshot in place. Tests inject a fake dialFn + a fake
// now() to drive this path deterministically.
//
// Lifecycle:
//
//	Caller starts the goroutine exactly once at boot. The
//	stream is a long-lived connection — terminating it is
//	done by ctx cancellation. The lifecycle matches
//	egressAggregator's usage: start at daemon boot, terminate
//	by process shutdown.
func (a *gatewayEgressAdapter) startStream(ctx context.Context, socketPath string, log *slog.Logger) {
	if a.dialFn == nil || socketPath == "" {
		if log != nil {
			log.Warn("gatewayEgressAdapter: no dialFn or socket path; tx_bytes will stay 0")
		}
		return
	}
	go func() {
		backoff := 250 * time.Millisecond
		const backoffMax = 5 * time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			client, err := a.dialFn(ctx, socketPath, a.tlsCfg)
			if err != nil {
				if log != nil {
					log.Warn("gatewayEgressAdapter: dial failed; backing off", "err", err, "backoff", backoff)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > backoffMax {
					backoff = backoffMax
				}
				continue
			}
			backoff = 250 * time.Millisecond
			a.consumeStream(ctx, client, log)
		}
	}()
}

// consumeStream runs one stream-receive iteration: open the
// server-streaming RPC, fold every frame into the snapshot,
// return when the upstream closes or the ctx cancels.
func (a *gatewayEgressAdapter) consumeStream(ctx context.Context, client egresspb.EgressTxServiceClient, log *slog.Logger) {
	stream, err := client.StreamBytes(ctx, &egresspb.StreamBytesRequest{})
	if err != nil {
		if log != nil {
			log.Warn("gatewayEgressAdapter: stream open failed", "err", err)
		}
		return
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && log != nil {
				log.Debug("gatewayEgressAdapter: stream recv ended", "err", err)
			}
			return
		}
		a.recordFrame(frame)
	}
}

// recordFrame folds one BytesFrame into the snapshot. Called
// exclusively from consumeStream under the assumption that the
// producer (gatewayd-internal) sends strictly minute-aligned timestamps
// (it does — pkg/gateway/egresssink truncates on intake). A
// non-truncated minute from a future gateway-side change would
// be treated as its own bucket (different minuteUnix) and
// coexist with the truncated bucket; that's the documented
// "no implicit re-bucketing" contract.
func (a *gatewayEgressAdapter) recordFrame(frame *egresspb.BytesFrame) {
	if frame == nil || frame.InstanceId == "" || frame.Minute == nil {
		return
	}
	minuteUnix := frame.Minute.AsTime().UTC().Truncate(time.Minute).Unix()
	a.mu.Lock()
	defer a.mu.Unlock()
	rows, ok := a.data[frame.InstanceId]
	if !ok {
		rows = make(map[int64]uint64, 4)
		a.data[frame.InstanceId] = rows
	}
	rows[minuteUnix] = frame.Bytes
}

// Tracked returns the number of distinct instances the adapter
// currently holds rows for. Test seam; not on the hot path.
func (a *gatewayEgressAdapter) Tracked() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.data)
}

// dialGatewayEgressStream dials the gatewayd-internal egress listener and
// returns a stub EgressTxServiceClient. Single-box deployments
// pass a unix socket path + nil tlsCfg; the unix-socket DAC auth
// (ADR-015, group `faas`, mode 0660) is the only authentication
// on that path. Multi-box deployments pass a tcp/dns target +
// a non-nil tlsCfg loaded via cfg.LoadEgressTLS(); the
// stdlib verifier handles chain + SAN + EKU in a single pass
// (ADR-052). The dialer is overridable via gwEgress.dialFn for
// tests.
//
// A fresh *grpc.ClientConn per dial is the canonical "long-lived
// streaming RPC" shape (gRPC's stream stays alive across the
// lifetime of the conn, and a dropped conn signals a stream close
// which the goroutine handler reconnects on).
func dialGatewayEgressStream(ctx context.Context, target string, tlsCfg *tls.Config) (egresspb.EgressTxServiceClient, error) {
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("meterd: dial gatewayd-internal egress %s: %w", target, err)
	}
	return egresspb.NewEgressTxServiceClient(conn), nil
}

// egressAggregator combines scheddEgressAdapter (net_tx_bytes)
// and gatewayEgressAdapter (tx_bytes) into a single
// meter.EgressSource. The two columns are sourced from
// independent producers (ADR-046 §2) so a single tick may
// have one or both; the aggregator ORs ok from the underlying
// adapters and zeros out the column the underlying adapter
// didn't report.
type egressAggregator struct {
	schedd *scheddEgressAdapter
	gw     *gatewayEgressAdapter
}

func (a *egressAggregator) EgressBytes(instanceID string) (uint64, uint64, bool) {
	if a == nil || instanceID == "" {
		return 0, 0, false
	}
	// Per-column reads. Either adapter may report "no row" for
	// this instance; the aggregator returns the union — the
	// sampler treats nonzero bytes as a real observation even if
	// only one column produced a value, because tx_bytes and
	// net_tx_bytes are independent (gateway HTTP response vs
	// root-side veth rx) and "no gateway traffic but veth rx
	// present" is a perfectly valid per-minute state.
	var (
		txBytes, netTxBytes uint64
		okSchedd, okGw      bool
	)
	if a.schedd != nil {
		_, netTxBytes, okSchedd = a.schedd.EgressBytes(instanceID)
	}
	if a.gw != nil {
		txBytes, _, okGw = a.gw.EgressBytes(instanceID)
	}
	if !okSchedd && !okGw {
		return 0, 0, false
	}
	return txBytes, netTxBytes, true
}

func main() {
	wire.Daemon("meterd", run)
}

// runDeps is the dependency-injection seam for tests.
type runDeps struct {
	configPath string
	openDB     func(context.Context, string) (*pgxpool.Pool, error)
	migrate    func(context.Context, *pgxpool.Pool) error
	loadMeter  func(*Config) (*meter.Config, error)
	// getenv is the env reader the wire-up uses (FAAS_SCHEDD_ADDR,
	// FAAS_BILLING_PROVIDER, FAAS_QUOTA_INTERVAL, ...). Tests can stub it.
	// Mirrors cmd/apid/main.go's getenv on its runDeps.
	getenv func(string) string
	// dialSchedd is the constructor for the schedd gRPC client. nil in
	// production (defaultDeps wires scheddgrpc.DialContext); tests
	// inject a fake to avoid touching the unix socket. Issue #95:
	// signature takes ctx + tls config so the dial participates in the
	// daemon's lifecycle cancellation and can dial a TLS-wrapped remote
	// schedd once the control plane is decoupled.
	dialSchedd func(ctx context.Context, target string, tlsCfg *tls.Config) (parkInstanceParker, error)
	// loadBillingProvider constructs the billing.Provider the pusher
	// loop dispatches through (ADR-025 / PR #3). nil in production
	// (defaultDeps wires billingloader.LoadProviderForMeterd); tests
	// inject a stub that returns a no-op Provider so the loop body
	// runs without touching Stripe/Paddle. Mirrors the test-double
	// pattern at cmd/apid/main.go.
	//
	// PR-P2: the env-overlaid TOML config is threaded through so the
	// loader can pick the active provider (Stripe vs Paddle) without
	// the daemon reaching into pkg/billing/loader. The caller applies
	// ApplyBillingEnvOverlay before the call; loadBillingProvider does
	// not re-read env.
	loadBillingProvider func(cfg *billingloader.RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (billing.Provider, string, error)
	// loadBillingConfig reads the [billing] block from meterd.toml.
	// nil in production (defaultDeps wires billingloader.LoadBillingConfigFromPath);
	// tests stub to return a hand-rolled *RootBillingConfig so the
	// loader path can be exercised without writing a temp TOML.
	loadBillingConfig func(path string) (*billingloader.RootBillingConfig, error)
	// The two collaborators are wired in production by runWithDeps
	// after the pool is open; tests can pre-populate via the fields.
	parker parkInstanceParker
	pusher billing.Provider
	// mailer is the dunning-timer's outbound email. Wired via
	// mail.SenderFromEnv in defaultDeps so the FAAS_MAIL_TRANSPORT
	// knob is honored (default: log). Tests can inject a noop.
	mailer mail.Sender
	now    func() time.Time
	// metricsListenAndServe returns a fully-built *http.Server bound to a
	// fresh net.Listener on addr (or the error from net.Listen). The caller
	// invokes `srv.Serve(ln)` on a goroutine and `srv.Shutdown(stopCtx)`
	// during graceful drain — the same server owns both halves, so the
	// pair stays in lockstep (no possibility of one server's Serve
	// outliving another's Shutdown). Mirrors cmd/schedd/main.go:151-158.
	//
	// The four timeouts + maxHeaderBytes are passed in (resolved by
	// cfg.MetricsListener at the call site) rather than re-resolved
	// inside the factory so the factory stays a pure *http.Server
	// builder with no cfg dependency — tests stubbing
	// metricsListenAndServe never need to construct a Config.
	// Defaults live in pkg/api/limits.go as
	// Metrics*SecondsDefault; per-daemon override is the four
	// cfg.Metrics* TOML fields (ADR-122).
	metricsListenAndServe func(addr string, h http.Handler, readTimeout, writeTimeout, idleTimeout time.Duration, maxHeaderBytes int64) (*http.Server, error)
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check.
	capCheck func() error
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: "/etc/faas/meterd.toml",
		openDB:     db.Open,
		migrate:    db.MigrateUp, // F2 / ADR-124: acquires pg_advisory_lock; safe for fleet bootstrap
		loadMeter:  func(c *Config) (*meter.Config, error) { return c.Meter, nil },
		getenv:     os.Getenv,
		dialSchedd: func(ctx context.Context, target string, tlsCfg *tls.Config) (parkInstanceParker, error) {
			c, err := scheddgrpc.DialContext(ctx, target, tlsCfg)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		loadBillingProvider: func(cfg *billingloader.RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (billing.Provider, string, error) {
			return billingloader.LoadProviderForMeterd(cfg, env, store, log)
		},
		loadBillingConfig: billingloader.LoadBillingConfigFromPath,
		mailer:            nil, // populated lazily in runWithDeps via mail.SenderFromEnv
		now:               time.Now,
		metricsListenAndServe: func(addr string, h http.Handler, readTimeout, writeTimeout, idleTimeout time.Duration, maxHeaderBytes int64) (*http.Server, error) {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return nil, err
			}
			srv := &http.Server{
				Handler:           h,
				ReadHeaderTimeout: 10 * time.Second, // pre-existing; ADR-122 doesn't override
				ReadTimeout:       readTimeout,
				WriteTimeout:      writeTimeout,
				IdleTimeout:       idleTimeout,
				MaxHeaderBytes:    int(maxHeaderBytes), // http.Server field is int; the cfg knob is int64 to mirror api.DefaultMaxHeaderBytes
			}
			// Serve in a goroutine; the daemon keeps `srv` and calls
			// Shutdown on it during drain. Pairing Serve/Shutdown on the
			// same *http.Server avoids the dual-server asymmetry the
			// factory's previous shape allowed (PR #75 review finding).
			// Errors are logged via the package-level slog.Default here
			// because defaultDeps is built before runWithDeps wires the
			// daemon's *slog.Logger.
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Default().Error("meterd: metrics http", "err", err)
				}
			}()
			return srv, nil
		},
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	// DEPLOY-1 / ADR-075 capdecl gate. meterd is unprivileged —
	// no Allow, no Deny. The sampler, quota, stripe ticks and
	// the age-sealed secret reads all run inside the unit's
	// systemd hardening (NoNewPrivileges, ProtectSystem,
	// PrivateTmp, etc.). Any future cap_ add lands here, not in
	// the unit file. The capCheck seam (review finding M2) lets
	// tests stub the live /proc/self/status check.
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("meterd")
	traceShutdown, traceErr := trace.InitTracerWithRegistry(ctx, "meterd", wire.Version, log, ops.Registry(), ops.MetricPrefix())
	if traceErr != nil {
		return fmt.Errorf("meterd: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("meterd: trace shutdown failed", "err", err)
		}
	}()

	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	// SAFE-RELEASES-F3: canary progression and safedeploy action dispatch
	// are one activation unit. The action dispatcher needs the APID client
	// created from the canary token; fail before opening Postgres if an
	// operator stages only one secret and would otherwise leave rollouts in
	// a partially automated state.
	if err := validateSafeDeployTokenPair(
		safeDeployToken(deps.getenv, "FAAS_CANARY_PROGRESSION_TOKEN"),
		safeDeployToken(deps.getenv, "FAAS_SAFEDEPLOY_TOKEN"),
	); err != nil {
		return err
	}
	// Gate-B box-role gate. meterd is a control-plane daemon —
	// it refuses to start under RoleComputeOnly. The role is
	// set from TOML or FAAS_METERD_ROLE at deploy time; default
	// is RoleSingleBox so single-box dev boots unmoved.
	if err := role.Require("meterd", cfg.Role, role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	// Mega-PR-A (issue #911 / ADR-110 PR-1): boot log carrying the
	// multi-box identity. Mirrors schedd/apid/gatewayd-public so
	// the playbook shape is identical across daemons.
	if cfg.NodeName != "" {
		log.Info("meterd owner node", "node_name", cfg.NodeName)
	} else {
		log.Info("meterd: legacy single-box (cfg.NodeName empty)")
	}
	mc, err := deps.loadMeter(cfg)
	if err != nil {
		return err
	}
	mc.Defaults()

	pool, err := deps.openDB(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := deps.migrate(ctx, pool); err != nil {
		return err
	}

	store := state.NewPgStore(pool)
	pn := db.PoolNotifier{Pool: pool}

	// Resolve the schedd socket: env wins over the TOML default so the
	// e2e harness can dial a per-test socket without rewriting the unit
	// file. Both empty is the strict-exit failure case (issue #52
	// acceptance — refuse to start rather than run unbounded).
	scheddAddr := deps.getenv("FAAS_SCHEDD_ADDR")
	if scheddAddr == "" {
		scheddAddr = cfg.SocketPath
	}
	if scheddAddr == "" {
		return fmt.Errorf("meterd: FAAS_SCHEDD_ADDR (or socket_path in meterd.toml) is required")
	}
	parker := deps.parker
	if parker == nil {
		if deps.dialSchedd == nil {
			return fmt.Errorf("meterd: nil dialSchedd and nil parker (refusing to start unbounded)")
		}
		// ADR-052: load the mTLS config meterd uses to dial schedd.
		// Single-box deployments keep all three paths empty and
		// LoadScheddTLS returns (nil, nil); multi-box deployments
		// pass tcp:// or dns:// + a TLS cluster.
		scheddTLS, err := cfg.LoadScheddTLS()
		if err != nil {
			return fmt.Errorf("meterd: load schedd TLS: %w", err)
		}
		c, err := deps.dialSchedd(ctx, scheddAddr, scheddTLS)
		if err != nil {
			return fmt.Errorf("meterd: dial schedd %q: %w", scheddAddr, err)
		}
		parker = c
	}

	pusher := deps.pusher
	var provName string
	if pusher == nil {
		if deps.loadBillingProvider == nil {
			return fmt.Errorf("meterd: nil loadBillingProvider and nil pusher (refusing to start unbounded)")
		}
		// PR-P2: read the [billing] block from the daemon's TOML and
		// overlay env on top. Missing file is non-fatal (defaults), bad
		// TOML is fatal — LoadBillingConfig wraps with %w so the operator
		// sees the underlying parse error. The env overlay runs after
		// LoadBillingConfig so env wins over TOML (the docs claim).
		loadBillingConfig := deps.loadBillingConfig
		if loadBillingConfig == nil {
			loadBillingConfig = billingloader.LoadBillingConfigFromPath
		}
		billingCfg, err := loadBillingConfig(deps.configPath)
		if err != nil {
			return fmt.Errorf("meterd: load billing config: %w", err)
		}
		billingCfg = billingloader.ApplyBillingEnvOverlay(billingCfg, deps.getenv)
		var loadErr error
		pusher, provName, loadErr = deps.loadBillingProvider(billingCfg, deps.getenv, store, log)
		if loadErr != nil {
			return fmt.Errorf("meterd: load billing provider: %w", loadErr)
		}
		// Empty API key on a Stripe box is a soft-warn today
		// (pushUsageRecordSDKSum returns an error per call, the loop
		// logs and skips); with Polar or Paddle, the API key must
		// be set or the SDK refuses to initialize. Surface the provider
		// name so an operator can match the warning to the right
		// source.
		//
		// Read from the merged cfg (env wins if non-empty, TOML is the
		// fallback — pkg/billing/loader/config.go::ApplyBillingEnvOverlay)
		// so a TOML-only deploy doesn't emit a false-positive warning.
		// Reading deps.getenv directly here would warn even when the
		// TOML key is present and the SDK initializes fine.
		warnIfEmptyAPIKey(log, billingCfg, provName)
		log.Info("meterd billing provider loaded", "provider", provName)
	}
	// Mailer: defaults to mail.SenderFromEnv so FAAS_MAIL_TRANSPORT
	// selects the transport (resend/postmark/log/noop). The dunning
	// timer needs this for its transition emails. Operator-selected
	// resend / postmark with the credential env var empty is
	// fail-closed (ADR-115 §D5); the wrapped ErrMailerMisconfigured
	// propagates here so the daemon refuses to boot instead of
	// silently dropping email into slog.
	//
	// Issue #246 extends that contract from "credential missing" to
	// "transport unselected": on a non-dev box, an unset or unknown
	// FAAS_MAIL_TRANSPORT also fails closed via ErrMailUnsetInProd
	// / ErrMailUnknownTransport. Operators who really do want mail
	// in the journal can set FAAS_MAIL_TRANSPORT=log; developers
	// iterating locally can set FAAS_DEV=1 to fall back to log when
	// the transport is unset. Both escapes are documented in the
	// boot hint below so the message names every escape hatch.
	mailer := deps.mailer
	if mailer == nil {
		var err error
		mailer, err = mail.SenderFromEnv(deps.getenv, log)
		if err != nil {
			return fmt.Errorf("meterd: %w\n"+
				"  fix one of:\n"+
				"    - set FAAS_MAIL_TRANSPORT=resend (or postmark) plus FAAS_MAIL_FROM and the provider key in /etc/faas/sealed.env\n"+
				"    - set FAAS_MAIL_TRANSPORT=log to keep mail in the journal\n"+
				"    - set FAAS_DEV=1 on a dev/CI box where unset transport should resolve to log", err)
		}
	}
	// PR #1191 fixup: wrap the transport with the decorator stack so
	// the dunning timer's outbound mail is suppressed-aware + retries
	// on 429/5xx. Without this a past-due customer bounces a
	// Resend request and meterd's quota-tick loop sees the failure
	// as a permanent error instead of retrying within the wall-clock
	// budget. Tests inject deps.mailer (non-nil) so this block is
	// skipped on the unit-test path.
	if deps.mailer == nil && mailer != nil {
		transportLabel := strings.ToLower(deps.getenv("FAAS_MAIL_TRANSPORT"))
		mailer = &mail.SuppressingSender{
			Inner: &mail.RetryingSender{
				Inner:         mailer,
				TransportName: transportLabel,
				Log:           log,
			},
			Store: mailStoreCheckerAdapter{s: store},
			Log:   log,
		}
	}

	// Bulk-sender compliance (issue #246 item 4): the quota-warning
	// template is the ONE outbound mail that carries a
	// List-Unsubscribe header pair (RFC 8058). The URL must be an
	// absolute http/https URL — anything else is a Gmail/Yahoo
	// rejection path. Empty = dev box (header skipped, not
	// substituted with a placeholder).
	if unsub := deps.getenv("FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL"); unsub != "" {
		if err := mail.ValidateUnsubscribeURL(unsub); err != nil {
			return fmt.Errorf("meterd: FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL: %w", err)
		}
		meter.SetNotificationsUnsubscribeURL(unsub)
		log.Info("meterd: notifications unsubscribe URL configured",
			"len", len(unsub))
	}

	// FAAS_QUOTA_INTERVAL / FAAS_SAMPLE_INTERVAL / FAAS_STRIPE_INTERVAL /
	// FAAS_DUNNING_INTERVAL / FAAS_RESIDENCY_INTERVAL let the e2e test
	// shrink the timer cadences to sub-second for the "transition
	// within one tick" acceptance. A bad parse logs and falls through
	// to mc.Defaults() rather than crashing the daemon.
	applyEnvTick("FAAS_SAMPLE_INTERVAL", &mc.SampleInterval, deps.getenv, log)
	applyEnvTick("FAAS_QUOTA_INTERVAL", &mc.QuotaInterval, deps.getenv, log)
	applyEnvTick("FAAS_STRIPE_INTERVAL", &mc.StripeInterval, deps.getenv, log)
	applyEnvTick("FAAS_DUNNING_INTERVAL", &mc.DunningInterval, deps.getenv, log)
	applyEnvTick("FAAS_RESIDENCY_INTERVAL", &mc.ResidencyInterval, deps.getenv, log)
	applyEnvTick("FAAS_ALERT_EVAL_INTERVAL", &mc.AlertEvalInterval, deps.getenv, log)
	applyEnvTick("FAAS_ROLLUP_INTERVAL", &mc.RollupInterval, deps.getenv, log)
	// ADR-049 §B.1/§B.3/§B.4 — drift detector, storage rollup,
	// and retention cadences. Defaults live in pkg/meter/{config,
	// storage, retention}.go so cmd/meterd stays thin.
	applyEnvTick("FAAS_RECONCILE_INTERVAL", &mc.ReconcileInterval, deps.getenv, log)
	applyEnvTick("FAAS_STORAGE_ROLLUP_INTERVAL", &mc.StorageRollupInterval, deps.getenv, log)
	applyEnvTick("FAAS_RETENTION_INTERVAL", &mc.RetentionInterval, deps.getenv, log)
	// ADR-098 PR-C: connection-aware probe + partition cadences.
	// Defaults live in pkg/meter/{config,upstream_probe,upstream_partitions}.go;
	// the env override is here so the e2e test can shrink the
	// 30 s probe cadence to sub-second when the FAAS_UPSTREAM_PROBE
	// flag is on. A bad parse logs and falls through to mc.Defaults().
	applyEnvTick("FAAS_UPSTREAM_PROBE_INTERVAL", &mc.UpstreamProbeInterval, deps.getenv, log)
	applyEnvTick("FAAS_UPSTREAM_PROBE_PARTITION_INTERVAL", &mc.UpstreamPartitionCreateInterval, deps.getenv, log)
	// Production-leveling Stream C: env-scoped stuck-after
	// threshold (FAAS_SAFEDEPLOY_STUCK_AFTER). The var must be
	// applied before the safedeploy orchestrator's walkRow
	// reads StuckAfterDuration — both setters happen at boot
	// before any tick goroutine starts (runTicks is spawned
	// after this block). The setter silently ignores <= 0 so a
	// bad parse falls through to the canned 30 min default.
	safedeploy.SetStuckAfterDuration(stuckAfterFromEnvMeterd(deps.getenv, log))
	state.SetRecoverRolloutStuckAfter(stuckAfterFromEnvMeterd(deps.getenv, log))
	// ADR-123: cert-expiry refresher + MTD spend aggregator
	// cadences. Defaults live in cmd/meterd/alert_presets_ticks.go
	// so the loops and the env-tick parser share the same source
	// of truth.
	applyEnvTick("FAAS_CERT_EXPIRY_REFRESHER_INTERVAL", &mc.CertExpiryRefresherInterval, deps.getenv, log)
	applyEnvTick("FAAS_ACCOUNT_SPEND_AGGREGATOR_INTERVAL", &mc.AccountSpendAggregatorInterval, deps.getenv, log)
	// Validate after applying environment overrides. In production the
	// provider is loaded before the timer overlay, so validating only the
	// TOML value would let FAAS_STRIPE_INTERVAL=24h bypass Polar's hourly
	// delivery contract.
	if err := validateBillingPushInterval(provName, mc.StripeInterval); err != nil {
		return err
	}

	// Dunning timer: drives the 7-day past_due → suspended and 21-day
	// suspended → deleted_pending transitions (spec §4.7, §17). Wired
	// into the loop alongside sample/quota/stripe so all five timers
	// share the same ctx-cancel lifecycle.
	dunning := meter.NewDunning(meter.DunningParams{
		Store:  store,
		Parker: parker,
		Mailer: mailer,
		Notif:  pn,
		Log:    log,
	})

	// Per-daemon Prometheus registry (ADR-015) — built unconditionally
	// so the Loop has it from the first tick. meter.NewLoop accepts nil
	// and coerces to a fresh test registry; here we hand it the real one.
	wire.BootStamps(ctx, "meterd", ops)
	wire.RegisterDefaultOps(ops)
	// Residency timer: emits the §12 "Resident GB per paying customer"
	// gauge (ADR-031, PR #141). Wired into the loop alongside
	// sample/quota/stripe/dunning so all five timers share the same
	// ctx-cancel lifecycle. ops is the per-daemon registry above;
	// residency.SetResidentGBPerCustomer is nil-safe so a later ops
	// swap doesn't take the gauge down with it.
	residency := meter.NewResidency(store, deps.now, log, ops)

	// The five timers run in goroutines; the cancel-watcher below picks
	// up the first error and returns. meterd has no inbound gRPC — the
	// public listener is gatewayd-public's (spec §Component ownership).
	//
	// Issue #279 / PR-B: the cpu adapter lets the sampler read the
	// per-instance CPU-µs snapshot the schedd's instancestats.Poller
	// maintains. The adapter dials the schedd gRPC socket on the same
	// box (ADR-015) and refreshes the snapshot at most once per 30 s
	// — bounded staleness without forcing a gRPC round trip per
	// instance.
	cpu := &scheddCPUAdapter{parker: parker, now: deps.now}
	// ADR-046 (PR-1 + PR-2): wire the egress adapters so the
	// sampler can append tx_bytes + net_tx_bytes to
	// usage_minutes. PR-1 leaves the gateway adapter as a
	// no-op (the ring-buffer producer lands in PR-2) so the
	// aggregator returns ok=false from gatewayEgressAdapter;
	// the schedd adapter is real (reads NetTxBytes from the
	// existing schedd gRPC round trip on the same rows map
	// cpu already fetched). The aggregator stays in place
	// across PR-1 and PR-2; PR-2 replaces gatewayEgressAdapter's
	// body without changing the wiring. WithEgress passes the
	// aggregator into NewLoop so the loop's sampler uses the
	// 4-arg NewSamplerWithEgress instead of the legacy
	// 3-arg NewSampler. Loop.WithEgress is nil-safe so a
	// future test harness can omit the egress wire without
	// touching the constructor.
	scheddEgress := &scheddEgressAdapter{cpu: cpu}
	// Load the mTLS config for the egress dial (ADR-052). Single-box
	// deployments keep all three paths empty and LoadEgressTLS returns
	// (nil, nil); multi-box deployments point egress_target at tcp://
	// or dns:// + a TLS cluster.
	gwEgressTLS, err := cfg.LoadEgressTLS()
	if err != nil {
		return fmt.Errorf("meterd: load egress TLS: %w", err)
	}
	gwEgress := &gatewayEgressAdapter{
		now:    deps.now,
		tlsCfg: gwEgressTLS,
		data:   make(map[string]map[int64]uint64),
		dialFn: dialGatewayEgressStream,
	}
	// PR-2: kick off the gateway stream consumer. The unix-socket
	// path is resolved by egresssocket.ResolveFromOS, which prefers
	// FAAS_EGRESS_SOCKET (added in PR-C+D), then the legacy
	// FAAS_GATEWAY_EGRESS_SOCKET (one release cycle), then the
	// config's new EgressSocket field, then the legacy
	// GatewayEgressSocket field, then egresssocket.DefaultSocketPath.
	// Passing both config fields is load-bearing: gatewayd-internal
	// binds the new /run/faas/egress.sock path, so meterd must consume
	// the same canonical config rather than silently selecting the
	// legacy socket when no environment override is present. Both sockets (this one and
	// FAAS_GATEWAY_SYNTH_SOCKET) share the same group-`faas` DAC auth
	// (ADR-015). ctx here is the loop ctx — when the daemon shuts
	// down the goroutine returns.
	envOr := func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return ""
	}
	gwSocketPath := egresssocket.ResolveFromOS(envOr, cfg.EgressSocket, cfg.GatewayEgressSocket)
	gwEgress.startStream(ctx, gwSocketPath, log)
	egress := &egressAggregator{schedd: scheddEgress, gw: gwEgress}
	// Issue #396 / ADR-045 PR 4: instantiate the alert evaluator and
	// hand it to the loop. The evaluator is nil-coerced below when
	// neither FAAS_PROMETHEUS_URL nor FAAS_HOST_AGE_IDENTITY_PATH is
	// configured — the dev loop runs five ticks on a stripped-down box
	// where Prometheus isn't reachable and host age isn't loaded.
	// The single meterd process today has exactly one evaluator; the
	// loop's contract is "at most one", matching the design note at
	// pkg/alerts/evaluator.go.
	evaluator := buildAlertEvaluator(deps, store, log, ops)
	// ADR-098 PR-C: connection-aware upstream probe + partition
	// cron. The FAAS_UPSTREAM_PROBE environment value is the
	// bootstrap fallback; the durable data-placement flag can
	// enable or disable both ticks without restarting meterd. The
	// probe needs the meterd region (declared on the host via
	// FAAS_REGION, mirrored from schedd) so each data_upstream_probes
	// row carries the region label.
	probe := buildUpstreamProbe(deps, store, ops, log)
	dataPlacementEnabled := runtimeconfig.NewBoolFlag(deps.getenv("FAAS_UPSTREAM_PROBE") != "")
	partitionCreate := PartitionCreateOnceFn(poolAdapter{pool}, log)
	gatedPartitionCreate := func(ctx context.Context) {
		if dataPlacementEnabled.Load() {
			partitionCreate(ctx)
		}
	}
	// ADR-132: the data-placement flag controls both the scheduler affinity
	// reader and this probe. Keep the probe goroutine alive while disabled so
	// a hot enable is picked up on the next interval.
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	defer runtimeCancel()
	watcher := runtimeconfig.New(store, pool, []string{runtimeconfig.KeyDataPlacement},
		func(ctx context.Context, key string, value json.RawMessage, _ int64) error {
			enabled, err := runtimeconfig.Bool(value)
			if err != nil {
				return err
			}
			if key == runtimeconfig.KeyDataPlacement {
				dataPlacementEnabled.Store(enabled)
				probe.SetEnabled(enabled)
			}
			return nil
		}, log)
	if err := watcher.Reconcile(runtimeCtx); err != nil {
		log.Warn("meterd: initial runtime config reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(runtimeCtx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("meterd: runtime config watcher exited", "err", err)
		}
	}()
	canaryProg, canaryAPID := buildCanaryProgression(deps, store, ops, log)
	safeDeployOrch := buildSafeDeployOrchestrator(deps, store, ops, log, canaryAPID, evaluator)
	// ADR-099 / issue #1184 Workstream A: 7 job-task Prometheus
	// metrics on a fresh per-daemon registry (same pattern as
	// pkg/fcvm/FrameworkReadyMetrics — see pkg/fcvm/metrics.go:178).
	// Mounted on the /metrics gatherer below alongside the
	// reconciler + ops registries so a single scrape exposes
	// app + job + reconciler + ops in one place. Built
	// unconditionally so the sample tick can reference the
	// gauges without a nil check (zero value is a no-op).
	jobMetrics := meter.NewJobMetrics()
	loop := meter.NewLoop(store, cpu, parker, pusher, pn, mailer, dunning, residency, evaluator, deps.now, log, mc, ops).
		WithEgress(egress).
		WithProbe(probe).
		WithPartitionCreate(gatedPartitionCreate).
		WithCanaryProgression(canaryProg).
		WithSafeDeploy(safeDeployOrch)
	errc := make(chan error, 1)
	go func() { errc <- loop.Run(ctx) }()

	// ADR-048 §5: usage_daily rollup goroutine. Free-function
	// (mirrors pkg/builderd/reaper.go) so the Loop struct's
	// surface stays focused on its existing 6 timer ticks.
	// The pgxpool.Pool satisfies the meter.execer contract
	// (Exec(ctx, sql, args...) → (rows int64, err error)).
	go meter.RollupLoop(ctx, poolAdapter{pool}, mc.RollupInterval, log)

	// ADR-049 §B.1: drift detector. The reconciler owns its own
	// Prometheus registry (not wire.OpsMetrics) so it can be wired
	// alongside the existing gauges without coupling — the two
	// registries are merged at the /metrics scrape below. We hand
	// it the same billing.Provider the pusher uses (no second SDK
	// client load). provName defaults to Polar for injected test or
	// compatibility pushers that do not carry a provider name; cmd/meterd/main.go:613
	// guarantees the pusher is loaded before we get here.
	recRegistry := prometheus.NewRegistry()
	if provName == "" {
		provName = provPolar
	}
	rec := reconciler.New(provName, store, pusher, log, recRegistry)
	go rec.Loop(ctx, mc.ReconcileInterval)

	// ADR-049 §B.3: snapshot/app-layer storage rollup. The store
	// interface (pkg/meter/storage.go) is a narrow projection over
	// state.Store so pkg/meter doesn't import the whole surface.
	// layerFn is nil this PR — overlay staging byte accounting
	// lands in a follow-up (ADR-049 §B.3 follow-up bullet); the
	// rollup still emits snapshot_bytes daily.
	storageStore := storageStoreAdapter{s: store}
	go meter.StorageRollupLoop(ctx, storageStore, nil, mc.StorageRollupInterval, log)

	// ADR-049 §B.4: 13-month retention DELETE cron. The pool
	// satisfies the retentionExecer contract.
	go meter.RetentionLoop(ctx, poolAdapter{pool}, mc.RetentionInterval, log)

	// ADR-127: per-request telemetry retention sweep. Runs on a
	// shorter cadence (hourly default) than the usage_minutes
	// sweep because Hobby's retention cap is 3 days — a daily
	// sweep would let the table accumulate several extra days
	// of rows between ticks.
	go meter.RetentionLoopRequestTelemetry(ctx, poolAdapter{pool}, meter.RequestTelemetryRetentionInterval, log)

	// SAFE-RELEASES production-leveling Stream D (issue #976 /
	// ADR-122 post-merge audit): deployment_audit GC cron.
	// Without this sweep the deployment_audit table grows
	// unbounded — disk fill + index bloat over months. 90-day
	// retention matches the on-call investigation envelope
	// (operators rarely look past 30 days post-incident); the
	// sweep is bounded-DELETE + idempotent so a missed tick is
	// safe. The deploymentAuditGCRowsDeleted counter is Inc'd
	// after each successful pass so the operator can tell
	// whether the GC is keeping up.
	go meter.RetentionLoopDeploymentAudit(ctx, poolAdapter{pool}, mc.DeploymentAuditRetentionInterval, log, func(n int64) {
		if c := ops.DeploymentAuditGCRowsDeleted(); c != nil {
			c.Add(float64(n))
		}
	}, func(error) {
		// SAFE-RELEASES-OBS PR-A: bump the GC-failed counter so
		// PR-B's deployment_audit_gc_failing alert can page on a
		// sustained prune-loop failure. Pre-PR this was journal-
		// only.
		if c := ops.DeploymentAuditGCFailedTotal(); c != nil {
			c.Inc()
		}
	})

	// ADR-123 / issue #1233: alert-preset signal-feeding
	// goroutines. CertExpiryRefresherLoop (meterd-owned, owns the
	// meterd_tenant_surface_cert_expiry_state table per the
	// CLAUDE.md ownership rule) feeds
	// apid_tenant_surface_cert_expiry_seconds
	// (alert preset cert_expiring_14d); AccountSpendAggregatorLoop
	// feeds meterd_account_spend_eur (alert preset spend_eur_20).
	// PR-B adds APIReachabilitySweepLoop (alert preset api_down,
	// meterd_api_reachable gauge) and DeploymentFailureSweepLoop
	// (alert preset deploy_failed, apid_deployment_failed_total
	// delta counter). All four free-function goroutines share the
	// loop ctx so the daemon's drain cancels them in one go.
	go CertExpiryRefresherLoop(ctx, CertExpiryRefresherParams{
		Store:    store,
		Log:      log,
		Ops:      ops,
		Interval: mc.CertExpiryRefresherInterval,
	})
	go AccountSpendAggregatorLoop(ctx, AccountSpendAggregatorParams{
		Store:    store,
		Log:      log,
		Ops:      ops,
		Interval: mc.AccountSpendAggregatorInterval,
	})
	go APIReachabilitySweepLoop(ctx, APIReachabilitySweepParams{
		Store:    store,
		Log:      log,
		Ops:      ops,
		Interval: mc.APIReachabilitySweepInterval,
	})
	go DeploymentFailureSweepLoop(ctx, DeploymentFailureSweepParams{
		Store:    store,
		Log:      log,
		Ops:      ops,
		Interval: mc.DeploymentFailureSweepInterval,
	})
	// Metrics + healthz listener. Mirrors cmd/schedd/main.go:143-158 —
	// per-daemon Prometheus registry (ADR-015), mux at /metrics +
	// /healthz, 5s graceful shutdown on drain. Empty cfg.MetricsAddr
	// disables both endpoints (the production default in
	// deploy/etc/meterd.toml.example — RETIRED in PR-1 Phase 2 after
	// PR-X; the v2 path is deploy/ansible/roles/control_plane_service/
	// files/meterd.toml.example).
	const metricsPath = "/metrics"
	// Issue #571 PR-A2: /readyz probe driven by loop.Health.
	// Built before the metrics-listener block so the
	// ControlMuxLite registration below can wire /readyz on
	// the same mux as /healthz + /metrics. defer stop so the
	// SIGTERM drain window surfaces in daemon_ready as 0.
	meterdProbe := BuildReadinessProbe(loop)
	meterdProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("meterd", ready, reason)
	})
	var metricsSrv *http.Server
	if cfg.MetricsAddr != "" {
		if deps.metricsListenAndServe == nil {
			return fmt.Errorf("meterd: nil metricsListenAndServe (refusing to start with MetricsAddr set)")
		}
		mux := http.NewServeMux()
		// /metrics merges the wire.OpsMetrics registry with the
		// reconciler's per-package registry via a Gatherers, so a
		// single scrape endpoint exposes both. prometheus.DefaultGatherer
		// is also included so the promhttp internals
		// (promhttp_metric_handler_errors_total) and the Go runtime
		// collector show up — TestRun_MetricsAddrServesEndpoints pins
		// the promhttp internals line as the load-bearing proof the
		// handler is mounted. Both wire + reconciler registries are
		// isolated so pkg/billing/reconciler stays free of an import
		// on pkg/wire. ADR-049 §B.1.
		gatherers := prometheus.Gatherers{ops.Registry(), recRegistry, jobMetrics.Registry(), prometheus.DefaultGatherer}
		mux.Handle(metricsPath, promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{}))
		// /healthz — 200 when every tracked timer (sample / quota /
		// stripe / dunning) has fired within
		// meter.StaleAfterMultiplier × its interval (spec §14 M7,
		// "meterd healthy iff sampled within 3 minutes"); 503 with a
		// JSON body listing the stale tick names otherwise. The body
		// always includes a per-tick last-fire wall clock so an
		// operator can diagnose without grepping journald.
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			status := loop.Health(time.Now())
			w.Header().Set("Content-Type", "application/json")
			code := http.StatusOK
			if !status.Healthy {
				code = http.StatusServiceUnavailable
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(status)
		})
		// Issue #571 PR-A2: /readyz (operator-side, short ASCII
		// body for the LB scrape). Driven by the same loop.Health
		// verdict via a 1s adapter goroutine (see
		// cmd/meterd/readiness.go). Stale tick names surface in
		// the body reason when the probe is 503. /healthz stays
		// the rich-JSON path for dashboards.
		wire.ControlReadyMuxLite(mux, meterdProbe.ReadyFunc(), meterdProbe.ReasonFunc())
		readTimeout, writeTimeout, idleTimeout, maxHeaderBytes := cfg.MetricsListener()
		srv, err := deps.metricsListenAndServe(cfg.MetricsAddr, mux, readTimeout, writeTimeout, idleTimeout, maxHeaderBytes)
		if err != nil {
			return fmt.Errorf("meterd: metrics listen %q: %w", cfg.MetricsAddr, err)
		}
		metricsSrv = srv
		log.Info("meterd metrics listening", "addr", cfg.MetricsAddr)
	}

	select {
	case <-ctx.Done():
		log.Info("meterd draining")
	case err := <-errc:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}

	// Graceful shutdown: detach a context from the already-cancelled caller
	// ctx (net/http Shutdown requires a non-Done parent). 5s matches the
	// schedd/vmmd/builderd shutdown deadline.
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if metricsSrv != nil {
		//nolint:contextcheck // shutdown ctx must outlive the already-cancelled caller ctx per net/http contract.
		_ = metricsSrv.Shutdown(stopCtx)
	}
	return nil
}

// applyEnvTick parses FAAS_*_INTERVAL on top of mc.Defaults(). Mirrors
// cmd/apid/main.go::graceIntervalFromEnv; kept local so meterd stays
// in one file.
func applyEnvTick(key string, dst *time.Duration, getenv func(string) string, log *slog.Logger) {
	v := getenv(key)
	if v == "" {
		return
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Warn("unparseable interval; using default", "env", key, "got", v, "err", err)
		return
	}
	*dst = d
}

// stuckAfterFromEnvMeterd reads FAAS_SAFEDEPLOY_STUCK_AFTER for
// the safedeploy orchestrator's StuckAfterDuration var (production-
// leveling Stream C). Mirrors cmd/apid/main.go::stuckAfterFromEnv;
// takes deps.getenv so tests can stub the env without mutating the
// process env. Returns 0 on unset / unparseable so the setter
// silently ignores it and the canned 30 min default stays in
// effect.
func stuckAfterFromEnvMeterd(getenv func(string) string, log *slog.Logger) time.Duration {
	v := getenv("FAAS_SAFEDEPLOY_STUCK_AFTER")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		if log != nil {
			log.Warn("FAAS_SAFEDEPLOY_STUCK_AFTER unparseable, using default",
				"value", v, "err", err)
		}
		return 0
	}
	return d
}

// buildAlertEvaluator wires the alert-evaluator (issue #396 /
// ADR-045 PR 4). Returns nil if neither FAAS_PROMETHEUS_URL nor
// FAAS_HOST_AGE_IDENTITY_PATH is configured — the dev loop runs
// five ticks on a stripped-down box where Prometheus isn't
// reachable and host age isn't loaded. The single meterd process
// today has exactly one evaluator; the loop's contract is "at most
// one", matching the design note at pkg/alerts/evaluator.go.
//
// Both env vars are read fresh on each call (cmd/meterd runs this
// helper once at startup, not on each tick). The Prometheus URL is
// used to build a pkg/promql.Client — empty URL means nil PromQL,
// which the evaluator treats as a "degraded: prometheus not
// configured" source for every rule. The identity path is loaded
// strictly; a 0o400 file-mode check (pkg/secretbox.LoadHostKey) is
// the load-bearing detail for the §11 tripwire.
func buildAlertEvaluator(deps runDeps, store state.Store, log *slog.Logger, ops *wire.OpsMetrics) *alerts.Evaluator {
	promURL := deps.getenv("FAAS_PROMETHEUS_URL")
	identityPath := deps.getenv("FAAS_HOST_AGE_IDENTITY_PATH")
	if promURL == "" && identityPath == "" {
		log.Warn("meterd: alert evaluator disabled — both FAAS_PROMETHEUS_URL and FAAS_HOST_AGE_IDENTITY_PATH unset; running with five ticks")
		return nil
	}

	var promClient appmetrics.PromQL
	if promURL != "" {
		// pkg/promql.NewClient takes an HTTPDoer for testability;
		// nil resolves to http.DefaultClient. PerAttempt timeout is
		// applied by pkg/webhookout's dispatcher, not the
		// evaluator (the evaluator's PromQL calls have their own
		// per-query deadline via the caller's context).
		promClient = promql.NewClient(promURL, nil)
	}

	var identityLoader func() *age.X25519Identity
	var identitiesLoader func() []*age.X25519Identity
	if identityPath != "" {
		ident, err := secretbox.LoadHostKey(identityPath)
		if err != nil {
			// A failure to load the identity is fatal for the
			// alert evaluator — without it we cannot unseal any
			// webhook_secret, so every dispatch would be a no-op.
			// Log loudly and skip the evaluator (the daemon
			// stays up and the other five ticks run).
			log.Error("meterd: load host age identity; alert evaluator disabled",
				"path", identityPath, "err", err)
			return nil
		}
		log.Info("meterd: host age identity loaded for alert evaluator",
			"path", identityPath)
		identityLoader = func() *age.X25519Identity { return ident }

		// Issue #316 / ADR-057: also load the multi-identity
		// slice from the same directory so the 30-day rotation
		// overlap window unseals webhook secrets sealed under
		// the previous host.age. Degrade to single-identity
		// (with a Warn) if LoadHostKeys fails — the box is
		// still unsealing current-keyed envelopes, just not
		// previous-keyed ones.
		if identities, loadErr := secretbox.LoadHostKeys(filepath.Dir(identityPath)); loadErr != nil {
			log.Warn("meterd: LoadHostKeys (rotation overlap) failed; alert dispatch will unseal only envelopes sealed under the current host.age",
				"dir", filepath.Dir(identityPath), "err", loadErr.Error())
		} else {
			identitiesLoader = func() []*age.X25519Identity { return identities }
			if len(identities) > 1 {
				log.Info("meterd: rotation overlap active — alert dispatch falls back across current + previous host.age")
			}
		}
	}

	dispatcher := webhookout.NewDispatcher(webhookout.DispatcherOptions{})
	auditor := audit.New(store, log, ops, "meterd")
	return alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     promClient,
		Audit:      auditor,
		Identity:   identityLoader,
		Identities: identitiesLoader,
		Dispatcher: dispatcher,
		Log:        log,
		Ops:        ops,
	})
}

// buildUpstreamProbe (ADR-098 PR-C) wires the connection-aware
// upstream probe driver. It always returns a gated probe so the
// durable data-placement flag can be changed without restarting
// meterd. FAAS_UPSTREAM_PROBE is used only as the bootstrap
// fallback. The probe is instantiated with:
//
//   - the meterd region (declared on the host via FAAS_REGION,
//     mirrored from schedd) so each data_upstream_probes row
//     carries the region label.
//   - the production Prom registry (ops) so the
//     meterd_data_upstream_rtt_ms + ..._probes_total + ...
//     _probe_duration_seconds metrics surface alongside the
//     other per-daemon gauges.
//
// The probe is keyed on the dedup'd (host_redacted_hash, kind,
// port) target set (Probe.Run fans out dials up to
// api.UpstreamProbeMaxConcurrent) — never plain host strings —
// so the §11 secret rule is enforced end-to-end.
func buildUpstreamProbe(deps runDeps, store state.Store, ops *wire.OpsMetrics, log *slog.Logger) *meter.Probe {
	enabled := deps.getenv("FAAS_UPSTREAM_PROBE") != ""
	if !enabled {
		log.Info("meterd: upstream probe disabled — FAAS_UPSTREAM_PROBE unset; probe remains wired for runtime enable")
	}
	region := deps.getenv("FAAS_REGION")
	if region == "" {
		log.Warn("meterd: FAAS_REGION empty; using \"unknown\" region label")
		region = "unknown"
	}
	return meter.NewProbe(store, region, log).SetEnabled(enabled)
}

// buildCanaryProgression (issue #976 / ADR-122 / SAFE-RELEASES-A)
// wires the canary_progression meterd tick driver. Returns
// (progression, apid client). Both are nil when
// FAAS_CANARY_PROGRESSION_TOKEN is unset (default
// OFF — the cluster outline's rollout gate flips the token
// generation ON in a follow-up operator-config PR). When ON:
//
//   - FAAS_APID_BASE_URL points to the apid instance the tick
//     drives (default http://localhost:8080 for the
//     single-control-plane topology; the multi-host fleet reads
//     it from the host-age identity file).
//   - FAAS_CANARY_PROGRESSION_TOKEN is the apid-issued
//     service-account bearer (NOT a customer token). APID stamps
//     the trusted actor and account_id on the atomic audit row.
//
// Returns (nil, nil) when the token is missing — the
// call sites nil-check the progression and skip the goroutine,
// preserving the pre-PR meterd behaviour exactly.
func buildCanaryProgression(deps runDeps, store state.Store, ops *wire.OpsMetrics, log *slog.Logger) (*canary.Progression, *api.Client) {
	token := safeDeployToken(deps.getenv, "FAAS_CANARY_PROGRESSION_TOKEN")
	if token == "" {
		log.Info("meterd: canary_progression disabled — FAAS_CANARY_PROGRESSION_TOKEN unset; running without canary_progression tick")
		return nil, nil
	}
	apidBase := deps.getenv("FAAS_APID_BASE_URL")
	if apidBase == "" {
		log.Warn("meterd: FAAS_CANARY_PROGRESSION_TOKEN set but FAAS_APID_BASE_URL empty; using http://localhost:8080 default")
		apidBase = "http://localhost:8080"
	}
	apid := api.NewClient(apidBase, token)
	progression := canary.NewProgression(&canaryStoreAdapter{store: store}, apid, ops, log)
	return progression, apid
}

// buildSafeDeployOrchestrator (issue #976 / ADR-122 / SAFE-RELEASES-F)
// wires the safedeploy orchestrator meterd tick driver. Returns
// nil when FAAS_SAFEDEPLOY_TOKEN is unset (default OFF — the
// cluster outline's rollout gate flips the token generation ON in
// a follow-up operator-config PR). When ON:
//
//   - FAAS_SAFEDEPLOY_TOKEN is the apid-issued service-account
//     bearer the orchestrator uses for any future apid HTTP calls
//     (today the orchestrator only stamps pkg/state.Store — no
//     apid HTTP — but the bearer stays wired for forward-compat
//     with pre-deploy diff checks).
//   - FAAS_APID_BASE_URL is reused from the canary_progression
//     configuration (the same apid instance serves both ticks).
//
// Returns nil when the token is missing — the call sites
// nil-check the orchestrator and skip the goroutine, preserving
// the pre-PR meterd behaviour exactly.
//
// ActionDispatcher wiring: the alert evaluator's SetActionExec
// method is called on the Evaluator instance built upstream
// (the Evaluator doesn't know about pkg/safedeploy; pkg/safedeploy
// doesn't know about pkg/alerts; the seam lives at
// alerts.Evaluator.SetActionExec).
func buildSafeDeployOrchestrator(deps runDeps, store state.Store, ops *wire.OpsMetrics, log *slog.Logger, apidClient *api.Client, evaluator *alerts.Evaluator) *safedeploy.Orchestrator {
	token := safeDeployToken(deps.getenv, "FAAS_SAFEDEPLOY_TOKEN")
	if token == "" {
		log.Info("meterd: safedeploy disabled — FAAS_SAFEDEPLOY_TOKEN unset; running without safedeploy tick")
		return nil
	}
	// The orchestrator's Store surface is narrower than the
	// canary store adapter's — SafedeployListPendingRollouts +
	// SafedeployStampRollout + AppendDeploymentAudit all exist
	// on the concrete *state.PgStore / *state.MemStore. Wrap
	// the store at the seam.
	storeAdapter := &safedeployStoreAdapter{store: store}
	const actorSentinel = "meterd:safedeploy"
	orchestrator := safedeploy.NewOrchestrator(storeAdapter, log, actorSentinel, actorSentinel)
	// SAFE-RELEASES-OBS PR-A: wire the daemon's wire.OpsMetrics
	// so emitAudit can bump the deployment_audit_emitted_total
	// counter on every successful + failed audit emit. nil-allowed
	// in the constructor so the smoke test (which builds an
	// Orchestrator without ops) keeps compiling.
	orchestrator.Ops = ops
	// Wire the ActionDispatcher onto the Evaluator. When
	// apidClient is nil (FAAS_CANARY_PROGRESSION_TOKEN unset but
	// FAAS_SAFEDEPLOY_TOKEN set), the ActionDispatcher is built
	// with a nil APID — its Execute path returns
	// ErrActionDispatcherNoAPID for any non-webhook action and
	// the evaluator's Stats.ActionSkipped counter bumps. This
	// is fail-safe: an alert rule with action=rollback fires the
	// webhook fan-out regardless, and the rollout's state
	// machine still walks via the orchestrator's pkg/state
	// writes.
	actionDispatcher := safedeploy.NewActionDispatcher(apidClient, log, actorSentinel).WithTargetResolver(store)
	if evaluator != nil {
		evaluator.SetActionExec(actionDispatcher)
	}
	return orchestrator
}

// safedeployStoreAdapter bridges pkg/state.Store to
// pkg/safedeploy.Store. Lives in cmd/meterd (not pkg/safedeploy)
// so pkg/safedeploy stays free of pkg/state's full surface —
// only the three methods the orchestrator calls are exposed.
type safedeployStoreAdapter struct {
	store state.Store
}

func (a *safedeployStoreAdapter) SafedeployListPendingRollouts(ctx context.Context) ([]state.Deployment, error) {
	return a.store.SafedeployListPendingRollouts(ctx)
}

func (a *safedeployStoreAdapter) SafedeployStampRollout(ctx context.Context, id string, rolloutState string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (state.Deployment, error) {
	return a.store.SafedeployStampRollout(ctx, id, rolloutState, startedAt, completedAt, abortedAt, abortedReason)
}

func (a *safedeployStoreAdapter) AppendDeploymentAudit(ctx context.Context, entry state.DeploymentAudit) (int64, error) {
	return a.store.AppendDeploymentAudit(ctx, entry)
}

// func(ctx context.Context) signature so Loop.WithPartitionCreate
// stays thin (it doesn't need the execer / interval / log
// parameters on its own surface). Called from main only when
// FAAS_UPSTREAM_PROBE is on; Loop.Health omits "upstream_part"
// from /healthz when the helper isn't wired.
//
// Note: callers that want recordTick("upstream_part", ...) to
// fire each tick should use PartitionCreateOnceFn instead — the
// loop variant blocks inside its own ticker, so wrapping it in
// Loop.runTicks never returns and recordTick is never observed.
func PartitionCreateLoopFn(db meter.PartitionCreateExecer, interval time.Duration, log *slog.Logger) func(ctx context.Context) {
	return func(ctx context.Context) {
		meter.PartitionCreateLoop(ctx, db, interval, log)
	}
}

// PartitionCreateOnceFn wraps meter.PartitionCreateOnce (the
// single-shot sweep) into a func(ctx context.Context) signature.
// Loop.runTicks then drives the cadence via its own ticker and
// observes each tick via recordTick("upstream_part", ...), so the
// /healthz endpoint reports a fresh timestamp instead of "never".
// This is the wiring TestRun_MetricsAddrServesEndpoints asserts on
// (cmd/meterd/main_test.go:308-313). The earlier Loop-wrapping
// pattern blocked inside PartitionCreateLoop's own ticker and
// starved recordTick — caught by the meterd pg-shard-2 CI gate
// on R13 fix #2 v1.
func PartitionCreateOnceFn(db meter.PartitionCreateExecer, log *slog.Logger) func(ctx context.Context) {
	return func(ctx context.Context) {
		_, err := meter.PartitionCreateOnce(ctx, db, time.Now())
		if log != nil && err != nil {
			log.Error("upstream partition create tick failed", "err", err)
		}
	}
}

// poolAdapter adapts *pgxpool.Pool to the meter.execer contract
// (Exec returns (rows int64, err error) instead of
// pgconn.CommandTag). Defined here so cmd/meterd/main.go wires the
// real pool without pkg/meter importing pgxpool.
type poolAdapter struct{ p *pgxpool.Pool }

func (a poolAdapter) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := a.p.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// storageStoreAdapter narrows state.Store to the meter.Store
// projection pkg/meter/storage.go needs. Defined here so pkg/meter
// doesn't import the entire state.Store surface just for the
// rollup. The cast is safe at the SQL layer — every column the
// rollup reads (apps.account_id, apps.id, snapshots.mem_bytes,
// snapshots.disk_bytes, snapshot_storage_daily.*) is exposed on
// state.Store today.
type storageStoreAdapter struct{ s state.Store }

func (a storageStoreAdapter) ListAllApps(ctx context.Context) ([]meter.AppRow, error) {
	rows, err := a.s.ListAllApps(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]meter.AppRow, len(rows))
	for i, r := range rows {
		out[i] = meter.AppRow{AccountID: r.AccountID, AppID: r.ID}
	}
	return out, nil
}

func (a storageStoreAdapter) LatestSnapshotBytes(ctx context.Context, appID string) (int64, int64, error) {
	return a.s.LatestSnapshotBytes(ctx, appID)
}

func (a storageStoreAdapter) AppendSnapshotStorage(ctx context.Context, accountID, appID string, day time.Time, snapshotBytes, layerBytes int64) error {
	return a.s.AppendSnapshotStorage(ctx, accountID, appID, day, snapshotBytes, layerBytes)
}

// mailStoreCheckerAdapter narrows state.Store to the
// mail.SuppressionChecker surface SuppressingSender needs. Keeps
// pkg/mail free of an import on pkg/state (the leaf-package
// interface seam at pkg/mail/suppression.go is the rule). PR #1191
// fixup: same adapter shape cmd/apid uses; duplicated here so each
// daemon's runDeps namespace owns its adapters.
type mailStoreCheckerAdapter struct{ s state.Store }

func (a mailStoreCheckerAdapter) IsMailSuppressed(ctx context.Context, email string) (bool, error) {
	return a.s.IsMailSuppressed(ctx, email)
}

// warnIfEmptyAPIKey emits a soft warning when the active provider has no
// API key in the merged config. Reading from billingCfg — the merged view
// after ApplyBillingEnvOverlay (env wins if non-empty, TOML is the
// fallback) — means a TOML-only deploy doesn't trigger a false-positive
// warn on every boot.
//
// Post-public-release (Polar is the default): an empty provider key is now
// a constructor error (NewProvider returns ErrNoAPIKey on empty /
// whitespace-only keys — CRIT-2 fix), so the daemon refuses to start
// rather than booting and warn-logging per tick. The Stripe branch
// below is retained as a soft-warn because the legacy Stripe surface
// has no equivalent guard (pushUsageRecordSDKSum returns an error per
// call and the loop logs-and-skips).
//
// Extracted so tests can pin the behaviour without spinning up runWithDeps.
func warnIfEmptyAPIKey(log *slog.Logger, billingCfg *billingloader.RootBillingConfig, provName string) {
	if billingCfg == nil {
		return
	}
	if provName == provStripe && billingCfg.Stripe != nil && billingCfg.Stripe.APIKey == "" {
		log.Warn("Stripe API key is empty — billing usage push will no-op (pushUsageRecordSDKSum returns an error without a key)",
			"provider", provName)
		return
	}
	if provName == provPaddle && billingCfg.Paddle != nil && strings.TrimSpace(billingCfg.Paddle.APIKey) == "" {
		// Pre-PR-#962 this was a soft-warn ('Paddle API key is empty —
		// daily push will no-op'); post-PR-#962 the loader fatals at
		// boot on the same condition so this branch is dead code on
		// production. Retained for the test path (TestLoadProviderForMeterd*
		// shapes construct a *Provider directly without going through
		// the loader) and as a defensive tripwire if a future caller
		// hands meterd a *Provider with an empty key (the SDK would
		// silently dial api.paddle.com with no auth otherwise).
		log.Error("Paddle API key is empty — this should have been caught at apid/meterd boot by NewProvider's ErrNoAPIKey guard (PR #962 CRIT-2). Treating the per-tick path as a no-op.",
			"provider", provName)
		return
	}
}

// validateBillingPushInterval keeps Polar delivery within the one-hour
// settlement cadence. The pusher has a durable backfill, but a longer
// interval increases receipt-time attribution skew and can exceed a
// deployment's configured lookback during an outage. The historical field
// name StripeInterval is retained for config/API compatibility.
func validateBillingPushInterval(provName string, interval time.Duration) error {
	if provName == provPolar && interval > time.Hour {
		return fmt.Errorf("meterd: Polar usage push interval must be <= 1h (got %s); set [meter].stripe_interval = \"3600s\" or FAAS_STRIPE_INTERVAL=1h", interval)
	}
	return nil
}
