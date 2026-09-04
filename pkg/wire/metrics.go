// Prometheus hooks shared by every daemon that exposes ops metrics over
// /metrics. ADR-015 fixes the metric naming convention for vmmd: every
// emitted metric and histogram MUST be prefixed "<daemon>_", e.g.
// "vmmd_ops_total" / "vmmd_op_duration_seconds". This file carries the
// helper that produces those two and the registry wrapper.
//
// Why a per-daemon prometheus.Registry (vs the default one):
//   - test isolation: each daemon's test builds its own registry, no
//     duplicate-registration panic between unit tests.
//   - per-daemon /metrics endpoint without a global scrape config fan-in.
//
// New in the M1 package: prometheus/client_golang.

package wire

import (
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/gateway/writegate"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// InstanceStatRow is the minimal per-instance rollup signal the
// instancestats poller (issue #170 / PR-A) feeds into the
// per-{app,node} Prometheus gauges. Defined here so pkg/wire does
// not import pkg/sched/instancestats and the schedd-side package
// stays free to evolve its richer InstanceStat (validity, freshness,
// sampling metadata) without disturbing the wire-emission contract.
//
// The values are:
//   - AppID / NodeID: the (app, node) label tuple.
//   - CPUPct: host cgroup CPU percent. math.NaN() means "absent
//     this tick" — the wire does not emit a sample for that row.
//   - RSSMB: cgroup memory.current, in MiB. math.NaN() means "absent".
//   - InflightRequests: outstanding ForwardHTTP count. Always 0 or
//     positive; zero is a real value and is emitted.
//   - CPUSeconds: cumulative CPU-seconds since the cpustats
//     cache's last regression reset (issue #279 / PR-B). 0 means
//     "no baseline yet" — the wire does not emit a counter delta
//     for that row. Otherwise the rollup Adds this to the
//     per-(app,node) CounterVec. NaN is treated as 0.
//
// The NaN-for-absent convention lets the wire side collapse rows
// the poller marked Unknown without a separate Validity field.
type InstanceStatRow struct {
	AppID            string
	NodeID           string
	CPUPct           float64
	RSSMB            float64
	InflightRequests int64
	CPUSeconds       float64
	// AccountID is the apps row's owning account (issue #301,
	// ADR-044). Required by the vmmd-side throttle counter so
	// the (account_id, app_id) label tuple is well-formed; the
	// top-100 admission primitive (topAppSet) keys on the
	// composite. Empty string is treated as "anonymous" by the
	// admission setter (matching the requestTotal overflow
	// policy) so a missing owner doesn't explode the wire
	// surface.
	AccountID string
	// ThrottledUsec is the cumulative cpu.stat throttled_usec
	// for the instance's cgroup slice (issue #301, ADR-044).
	// 0 means "no baseline yet" — the wire does not emit a
	// counter delta for that row. Otherwise the rollup Adds
	// the per-row delta to vmmd_cpu_throttle_seconds_total{account_id,
	// app_id} via the per-(account_id, app_id) baseline in
	// cpuThrottleLastSeen. NaN is treated as 0. The cpu.max
	// ratio gauge (vmmd_cpu_throttle_ratio{slice}) is the
	// alert source; this counter is the dashboard top-N view.
	ThrottledUsec float64
}

// OpsMetrics is the (per-daemon) bundle emitted at /metrics. Construct via
// NewOpsMetrics and pass the result into every handler that wants to record
// a counter + latency histogram in the ADR-015 shape.
type OpsMetrics struct {
	registry *prometheus.Registry
	// metricPrefix is the exact prefix used by this registry's metric
	// names. It can differ from the OTel service name for compatibility
	// aliases such as gatewayd-internal → gatewayd.
	metricPrefix string
	ops          *prometheus.CounterVec
	dur          *prometheus.HistogramVec
	// watchdogKills: introduced in commit 3 for the §6.1 state
	// watchdog. Labels identify the transition the watchdog forced
	// (from_state → to_state) — alerting on a non-zero rate of
	// "waking→cold_booting" labels is the spec §6.1 health signal.
	watchdogKills *prometheus.CounterVec
	// warmSnapshotErrors (issue #470 / PR A / ADR-055). Labelled by
	// reason ∈ {vmm_call, store_write}; the closed set disambiguates
	// the two failure modes without leaking the deployment_id into
	// the TSDB series. The PromQL `rate(vmmd_warm_snapshot_errors_total[5m])`
	// panel is the §12 warm-capture-error alert's primary signal.
	warmSnapshotErrors *prometheus.CounterVec
	// warmupErrors (Tier A8 / ADR-083). Per-(app slug) probe-failure
	// counter for the standby warm-up scraper. Bounded cardinality
	// (operator-managed FAAS_STANDBY_WARMUP_SLUGS_PATH); see
	// the warmupErrors constructor block below.
	warmupErrors *prometheus.CounterVec
	// writeRedirectTotal (Tier A9 / ADR-084) counts every write
	// request the cmd/gatewayd-internal writeGate classifies
	// (relayed, redirected, blocked, or short-circuited). Labelled
	// by outcome ∈ {relayed, redirect_307, same_box,
	// cookie_blocked, leader_unreachable, loop_prevented,
	// mTLS_failure, error} AND auth_kind ∈ {bearer, cookie,
	// anonymous}. The closed label sets keep the TSDB cardinality
	// bounded — the writeGate classifies at request entry, never
	// per-request-derived. See pkg/gateway/writegate for the
	// classification rules.
	writeRedirectTotal *prometheus.CounterVec
	// writeRedirectLatency (Tier A9 / ADR-084) is the histogram
	// of cross-box relay durations observed by writeGate. Only
	// cross-box hops emit a sample (local same-box and 307
	// fallback paths don't); the histogram's nil branch means a
	// pure-local daemon never bumps a series.
	writeRedirectLatency prometheus.Histogram
	// livenessRestarts (issue #554 / ADR-078) is the per-(app,
	// deployment) counter the Engine.DestroyForLivenessFailure path
	// increments on every liveness-driven destroy. The dashboard
	// panel "liveness: restarts by deployment (5m)" queries this
	// counter; the liveness_exhausted park alert
	// (instances.parked_liveness_exhausted audit kind) is the
	// operator-facing signal. Labels are bounded by the per-app
	// deployment count (≤ 20 for Scale, ≤ 1 for Hobby), so the
	// cardinality stays safe.
	livenessRestarts *prometheus.CounterVec
	// workloadOOMKills (Cluster C / ADR-121) is the per-(app,
	// deployment) counter the Engine.DestroyForWorkloadOOMFailure
	// path increments on every workload OOM-kill detected on the
	// customer's per-VM cgroup v2 leaf. Distinct from livenessRestarts
	// because the failure mode is "the kernel killed the workload,
	// not the liveness probe" — the operator-facing dashboard panel
	// surfaces these are correlated with plan RAM caps, not liveness
	// path issues. Cardinality bounds match livenessRestarts (per-app
	// deployment count).
	workloadOOMKills *prometheus.CounterVec
	// daemonRestartCount (issue #573 / ADR-128) is the per-(daemon,
	// version) counter that records how many times systemd has
	// restarted THIS process in its lifetime. The producer is the
	// wire.Daemon() boot path, which reads the
	// $SYSTEMD_RESTARTS_ON_FAILURE env var (set by the systemd unit
	// Restart=on-failure + RestartCountExport logic) and calls
	// RecordDaemonRestart(name, Version) once at startup. The
	// counter's purpose is to backstop node_exporter's
	// node_systemd_restart_count{name=~"faas-.*\\.service"} metric
	// in environments where the systemd collector isn't running
	// (operator disabled --collector.systemd, or scrape is broken) —
	// the alert rules in faas.rules.yml (FaasRestartLoop /
	// FaasRepeatedRestart) prefer the node_exporter metric but
	// fall back to daemon_restart_count{daemon} with a longer
	// for-window. Labels are bounded by the closed daemon set
	// (apid, gatewayd-public, gatewayd-internal, schedd, vmmd,
	// imaged, meterd, builderd, githubd, gregale) × the wire.Version
	// string, so the cartesian is pre-instantiated at boot to
	// surface zero rows from idle.
	daemonRestartCount *prometheus.CounterVec
	// daemonBuildInfo (issue #586 / ADR-129) is the per-(daemon,
	// version, git_sha, build_time) gauge that exposes the
	// identity of the running binary. Always 1 (the gauge's value
	// is meaningless — the labels carry the signal). Operator
	// dashboards query this metric for the "Daemon versions
	// fleet-wide" heatmap panel. The label set is bounded at 10
	// daemon names × the wire.Version × git_sha × build_time
	// cartesian, but in practice git_sha and build_time are
	// constant per binary so the realistic cardinality is 10 (one
	// row per daemon, all sharing the same version+git_sha
	// tuple). Pre-instantiated at boot — see SetDaemonBuildInfo.
	daemonBuildInfo *prometheus.GaugeVec
	// daemonUptimeSeconds (issue #586 / ADR-129) is the per-daemon
	// gauge that exposes process uptime in seconds. Updated every
	// 1s by a goroutine spawned from wire.Daemon() (see
	// recordUptime for the contract). The gauge is set, not
	// accumulated, so a daemon restart drops the value to a
	// small number and operators can read the value directly as
	// "seconds since this process started". Pre-instantiated at
	// the closed daemon set with an initial 0.
	daemonUptimeSeconds *prometheus.GaugeVec
	// daemonReady (issue #586 / ADR-129) is the per-daemon gauge
	// that flips 0 → 1 when the daemon's aggregate ReadyzProbe
	// reports that initialization and serving dependencies are
	// healthy. Until the probe signals readiness (see issue #571
	// and pkg/wire/readiness.go), the gauge reads 0.
	// Pre-instantiated at 0 for every daemon so the §12
	// dashboard's "Fleet readiness" panel surfaces a zero row
	// from boot. The probe observer calls MarkReady(id).
	daemonReady *prometheus.GaugeVec
	// faasDeployVersion (issue #586 / ADR-129 / cluster C commit 11)
	// is the per-version gauge that exposes the platform's
	// current release identifier. Single-version by design —
	// every daemon in a single release reports the same value,
	// so the cartesian has at most N rows for the last N
	// releases (operators compare across versions during a
	// rolling deploy). The label is `version` (mirrors
	// daemonBuildInfo's convention); the gauge value is always
	// 1 and the label carries the signal. Pre-instantiated at
	// boot from the current wire.Version so /metrics surfaces
	// the release from process start without a SetDeployVersion
	// call. SetDeployVersion(v) re-stamps on version change
	// (rolling deploy, hot-reload).
	faasDeployVersion *prometheus.GaugeVec
	// guestInitDuration (issue #470 / PR C / ADR-074) measures the
	// wall-clock time between the vmmd DGRAM recv of the framework-ready
	// signal and the Manager.MarkInstanceFrameworkReady return. Labelled
	// by {app, runner} so a Grafana panel can split per runtime. The
	// sentinels ("", "") are pre-instantiated so the dashboard panel
	// has a non-zero series from boot. Bucket set is spec §6.3 verbatim
	// {.05, .1, .2, .3, .35, .5, .8, 1, 1.5, 3, 5} — the consecutive
	// 0.3/0.35 pair is intentional (the 350 ms warm-wake budget needs
	// tight resolution near 0.35).
	guestInitDuration *prometheus.HistogramVec
	// wakeRPCDuration (ADR-097, P1B) measures schedd-side wall-clock
	// for each wake phase: admit_to_rpc (gRPC handler → vmmd RPC
	// start), rpc_call (vmmd Create{FromSnapshot,ColdBoot} round
	// trip), rpc_to_running (RPC return → WAKING/COLD_BOOTING → RUNNING
	// transition). Labelled by {app, phase} — phase is closed-set
	// {admit_to_rpc, rpc_call, rpc_to_running}. The empty-app
	// sentinel is pre-instantiated for every phase value so the §12
	// wake-latency decomposition dashboard panel surfaces a zero row
	// from boot. Bucket set reuses the spec §6.3 wake-latency budget
	// with one extra low-end bucket (0.01) for admit_to_rpc, which
	// is dominated by in-process lock + ledger consult and rarely
	// exceeds a few ms — without it the phase would collapse to the
	// 0.05 bucket and lose observability on the lock/ledger path.
	// wake_id is attached as a prometheus.Exemplar on each observation
	// so operators can join to gateway_wake_latency_seconds on the
	// gateway side and to the events table (BootStarted / BootCompleted
	// rows in pkg/sched/events.go) — exemplar attachment does NOT
	// add a wake_id label, so cardinality stays O(autoscale-enabled
	// apps × 3 phase values).
	wakeRPCDuration *prometheus.HistogramVec
	// gatewayDrainWaitSeconds (issue #587 / PR-A) — histogram
	// for the per-daemon graceful-shutdown drain. Closed label
	// set {daemon, outcome} pre-instantiated in NewOpsMetrics so
	// /metrics surfaces the closed cross-product from boot.
	gatewayDrainWaitSeconds *prometheus.HistogramVec
	// gatewayInflightRequests (issue #587 / PR-A) — gauge for
	// the per-daemon drain.Tracker in-flight count. Closed
	// label set {daemon, op}; no plan or app label per the
	// cluster plan's "Decisions baked in" §2 (Prometheus
	// cardinality discipline).
	gatewayInflightRequests *prometheus.GaugeVec
	// wakeSnapshotTier (issue #470 / PR C / ADR-074) — closed-set
	// counter for the warm-vs-init-vs-cold-boot choice Engine.usableSnapshotForWake
	// makes on every wake. Labels ∈ {warm, init, cold_boot_fallback}.
	// Pre-instantiated at boot so the wake-tier-mix panel has zero
	// rows from idle fleet, non-zero as soon as production wakes happen.
	wakeSnapshotTier *prometheus.CounterVec
	// wakeFailure (issue #1059 / ADR-127) — operator-facing wake
	// failure-mode counter. Labelled by (box, reason). The closed
	// reason vocabulary is
	// {snapshot_stale, disk_full, jailer_fail, netns_fail,
	// cgroup_fail, vsock_fail, snapshot_restore_err, mem_backend_err}
	// — every wake-failure site maps to exactly one of these (see
	// pkg/fcvm/wake_classify.go). The box label is bounded by the
	// boxLabelSet admission (maxBoxLabelValues = 64); overflow
	// collapses to otherBoxLabel ("__other__"). Pre-instantiated at
	// the closed (box, reason) cartesian so the §12
	// "Wake failures by reason (24h)" panel surfaces zero rows from
	// idle fleet. vmmd and schedd each emit their own
	// prefix_wake_failure_total from the same OpsMetrics — schedd
	// stamps record_runtime_failed from the audit-reason string at
	// pkg/sched/engine.go:2198; vmmd stamps the eight failure-reasons
	// from the wake-failure hook sites at pkg/fcvm/manager.go.
	wakeFailure *prometheus.CounterVec
	// wakeLatency (issue #1059 / ADR-127) — operator-facing per-box
	// per-phase wake-latency histogram. Labelled by (box, phase).
	// The closed phase set
	// {restore_ms, netns_tap_ms, guest_ready_ms} mirrors the
	// existing fleet-level vmmd_wake_phase_duration_seconds{phase}
	// (pkg/fcvm/metrics.go) so the §12 dashboard panel can swap
	// fleet → per-box without a legend change. The box label is
	// bounded by the boxLabelSet admission (same contract as
	// wakeFailure above). Bucket set reuses the existing per-phase
	// histogram's spec §6.3 envelope {0.05, 0.1, 0.2, 0.3, 0.35,
	// 0.5, 0.8, 1, 1.5, 3, 5, 10} — the 0.3/0.35 pair gives tight
	// resolution near the 350 ms warm-wake budget per ADR-074
	// §3.5. wake_id is attached as a prometheus.Exemplar on each
	// observation (matching the wakeRPCDuration precedent at
	// metrics.go:1421) so operators can join to the events table
	// (BootStarted / BootCompleted rows) on wake_id.
	wakeLatency *prometheus.HistogramVec
	// boxLabels: the bounded admission set shared by wakeFailure and
	// wakeLatency (issue #1059 / ADR-127). Same shape and contract
	// as accountLabelSet / ipLabelSet (fixed-capacity, non-evicting,
	// __other__ collapse) but a distinct type so the three sets do
	// not share capacity. The cap is maxBoxLabelValues (64) — sized
	// for the Tier A multi-host rollout (ADR-062 / ADR-066 chain);
	// the single-box production reality today resolves box to the
	// literal "local". Constructed once per OpsMetrics in
	// NewOpsMetrics; the mutex is the only synchronisation
	// primitive and is held only across the lookup/insert path.
	// Prometheus increments happen outside the critical section.
	boxLabels *boxLabelSet
	// appLabels: the bounded admission set that backs the
	// app-labelled metrics on OpsMetrics (issue #1059 / ADR-127
	// §3.5 deferred work). Same shape and contract as boxLabels /
	// accountLabels / ipLabels (fixed-capacity, non-evicting,
	// __other__ collapse via otherAppLabel) but a distinct type
	// so the four sets do not share capacity. The cap is
	// maxAppLabelValues (256) — sized for the Scale plan budget
	// (100 deployed apps) plus Tier A multi-region fan-out
	// headroom. The reserved "" label surfaces a missing-app-slug
	// path (a hook site fired before app_id was resolved) distinctly
	// from a real app slug that hit the admission cap (which
	// collapses to otherAppLabel). Constructed once per OpsMetrics
	// in NewOpsMetrics; the mutex is the only synchronisation
	// primitive and is held only across the lookup/insert path.
	// Prometheus increments happen outside the critical section.
	appLabels *appLabelSet
	// guestTailSeconds (issue #667 / ADR-078) — histogram of the
	// per-tail-task wall-clock duration from registration (a
	// waitUntil(promise) call inside the handler) to terminal
	// (completed / failed / timeout). Labels {plan, runtime,
	// outcome}:
	//   plan     ∈ api.Plans (Free/Hobby/Pro/Scale) — see limits.go
	//   runtime  ∈ {node22, node24, python312, python313, go124}
	//             (the 5 runtime images, hard-coded; references
	//             ADR-052 — closed set, never adds a sixth runtime
	//             without bumping this list)
	//   outcome  ∈ {completed, failed, timeout}
	//             (the 3 closed-set bytes a runner emits in the 0x04
	//             DGRAM envelope; "failed" includes handler_error
	//             AND forced_at_park — the per-plan breakdown is on
	//             guestTailFailedTotal below)
	// 4 plans × 5 runtimes × 3 outcomes = 60 series. Buckets
	// {0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 180, 600}: the 60s
	// bucket matches the Scale plan's TailTimeoutS ceiling; the 180s
	// bucket catches a runaway tail (3× the longest plan ceiling) so
	// an operator can spot a runner that's reverted to the pre-#667
	// behaviour; the 600s bucket matches buildDur's ceiling — a
	// tail that survives 10 minutes is a wire-incompatible bug
	// worth observing, not silently dropping into +Inf. Pre-
	// instantiated at boot so the §12 tail-watchdog panel has a
	// non-zero series from idle fleet.
	guestTailSeconds *prometheus.HistogramVec
	// guestTailFailedTotal (issue #667 / ADR-078) — counter for
	// per-tail-task failures. Distinct from guestTailSeconds'
	// outcome=failed label because the runner emits a single failed
	// byte regardless of the underlying cause, and the operator
	// wants to know WHY the task failed. Labels {plan, reason}:
	//   plan    ∈ api.Plans
	//   reason  ∈ {timeout, handler_error, forced_at_park, unknown}
	//            (closed set; "unknown" captures the catch-all where
	//            the runner emitted a non-closed outcome byte — a
	//            wire-incompatible bug worth surfacing)
	// 4 plans × 4 reasons = 16 series. Pre-instantiated at boot.
	guestTailFailedTotal *prometheus.CounterVec
	// planGateRescuedByExclude (ADR-124 follow-up #2) — counter
	// the apid scan service increments when --exclude flipped a
	// blocked pre-exclude gate to allowed. Labels {plan, reason}:
	//   plan    ∈ api.Plans
	//   reason  ∈ {apps_over_limit, crons_over_limit,
	//              crons_not_allowed}  (closed set; see
	//              cmd/apid/scan_service.go::gateRescueReason
	//              for the canonical bucketing — the classifier
	//              collapses the templated raw reasons to these
	//              three buckets so the TSDB series are bounded
	//              regardless of how many project slugs the
	//              operator excluded in a given apply)
	// 4 plans × 3 reasons = 12 series. Pre-instantiated at boot
	// so the §12 gate-rescue panel renders zero-row from boot
	// (the dashboard keys off the series; a counter that only
	// appears post-emit would surface as "no data" until the
	// first rescue, which is exactly when operators want to
	// see it).
	planGateRescuedByExclude *prometheus.CounterVec
	// tailCapReached (issue #667 / ADR-078) — counter the runner
	// increments when a customer tries to register the
	// (ConcurrentTailsPerInstance + 1)-th tail. Labels {plan} only
	// because the per-instance cap is the plan-matrix axis, not the
	// per-request TailCapMax (which is structural = 16). 4 series.
	// Pre-instantiated at boot.
	tailCapReached *prometheus.CounterVec
	// evictedPriority (issue #475) — closed-set counter for the
	// per-app eviction tier the reaper parked. Labels:
	//   priority ∈ {best_effort, reserved}
	//   reason ∈ {idle, eviction_aggressive, eviction_ram}
	// Pre-instantiated at boot so the §12 eviction-by-tier panel
	// has a non-zero series from idle fleet. The closed label set
	// keeps the TSDB series bounded; the loop increments the
	// counter after every successful park (idle / aggressive /
	// RAM-pressure).
	evictedPriority *prometheus.CounterVec
	// bridgeFramingTotal (ADR-127 §D3, Layer 7) — closed-set
	// counter for the per-request framing decision at the bridge.
	// Labels:
	//   app_protocol     ∈ {http1, http2, grpc}
	//   bridge_protocol  ∈ {h1, h2c}
	//   framing          ∈ {match, mismatch}
	// Pre-instantiates the full 3×2×2 = 12-series cross-product at
	// boot so the §12 bridge-protection dashboard surfaces a zero
	// row from idle fleet. The "mismatch" row is the operator-
	// facing surgical-rollback signal — when bridge_protocol=h1
	// but app_protocol ∈ {http2, grpc} (an operator forced the
	// rollback per docs/ops/h2c-rollback.md), framing=mismatch so
	// the FaasBridgeFramingMismatch alert (pkg/api/alerts.go) can
	// trip a non-zero rate and page on-call.
	bridgeFramingTotal *prometheus.CounterVec
	// eventsWriteFail: introduced in commit 4 for the audit-log
	// emission. A non-zero rate indicates that transitions are
	// succeeding but the events row isn't being written — the state
	// row is the source of truth, so this is observation-only.
	eventsWriteFail prometheus.Counter
	// auditWriteFail: introduced in IAM-4 (ADR-035) for the apid-side
	// auth audit emit. Mirrors eventsWriteFail — a failed audit write
	// logs Warn and increments the counter; the auth action has
	// already returned 200, so this is observation-only. Labelled by
	// account_id (issue #278) so an operator can graph a single
	// customer's audit-write failure stream. The label value is
	// resolved through accountLabel — see accountLabelSet — which
	// bounds the cardinality at maxAccountLabelValues; overflow
	// collapses to "__other__" so the Prometheus TSDB series set
	// stays bounded over the daemon's lifetime.
	auditWriteFail *prometheus.CounterVec
	// auditWriteDur: latency of state.Store.AppendEvent on the apid
	// audit seam, labelled by result ∈ {ok, failed} (issue #278). The
	// histogram covers the failure-path latency distribution so an
	// operator can distinguish a Postgres outage (slow AppendEvent,
	// many failures) from a transient insert race (fast failures).
	// Buckets sized for the single-row INSERT round-trip; distinct
	// from the control-plane dur histogram whose sub-millisecond
	// buckets are wrong for a Postgres call.
	auditWriteDur *prometheus.HistogramVec
	// cronFireNowDispatchDur: end-to-end latency of a manual cron
	// fire-now (issue #791 PR-D / ADR-090 §"Sub-decision 7"). The
	// start time is the customer's request row's `requested_at`
	// (captured at the apid INSERT); the observation point is the
	// schedd terminal stamp (MarkFireNowRequestSucceeded /
	// MarkFireNowRequestFailed). Labelled by result ∈ {succeeded,
	// failed} so an operator can split dispatcher-side latency
	// (successful wakes) from woke-then-failed or notify-lost
	// failures. The asymmetric buckets (0.1, 0.5, 1, 5, 30, 60)
	// cover three failure modes: notify-wake stall (<5s),
	// scheduler bypass / dispatcher capacity rejection (5-30s),
	// and the full-build-VM path (30-60s).
	cronFireNowDispatchDur *prometheus.HistogramVec
	// accountOrgMismatch: PR 3 (issue #190, ADR-061) registers the
	// counter on every daemon; PR 4 (handlers) and PR 6 (billing
	// webhook) emit on it when the dual-write path detects that
	// account.* and the personal-org mirror disagree. Labelled by
	// kind ∈ {plan, status, provider_customer_id,
	// stripe_subscription_item} — the closed set of mirrored fields
	// per ADR-061. Observation-only — writes never block on
	// mismatch. PR 7's cutover gate is "rate() == 0 for ≥1 metering
	// cycle"; this counter is the gate's input signal.
	accountOrgMismatch *prometheus.CounterVec
	// requestFailures: HTTP requests completed with status >= 400,
	// labelled by account_id, the route template, and code ∈ {ok, err}
	// (issue #278; PR #336 added the `code` label, issue #303 follow-up
	// closes the asymmetry with requestTotal — see ADR-039). The route
	// label reuses r.Pattern (the Go mux pattern, e.g. "GET
	// /v1/apps/{slug}") so cardinality is bounded by the route table,
	// not by URL paths — same precedent as apid_ops_total{op} (PR
	// #132). account_id flows through accountLabel as for the audit
	// counter; the three metrics (requestTotal, requestFailures,
	// auditWriteFail) share the same admission set so an account is
	// either represented by its real id in all three, or by
	// "__other__" in all three. `code` is always "err" today (the
	// counter only fires on status >= 400) but the label is added so
	// future derivations (e.g. partial-failure accounting) can flip
	// without a schema break.
	requestFailures *prometheus.CounterVec
	// requestTotal: HTTP requests completed, labelled by account_id,
	// route, and code ∈ {ok, err} (issue #303, ADR-039). The
	// per-request total counterpart to requestFailures — the two
	// metrics share the same accountLabelSet admission so a customer
	// is represented by their real id in both, or by "__other__" in
	// both. The `code` label is added so the error-rate alert
	// variant can run off the same counter; this is a deliberate
	// asymmetry from requestFailures (which has no `code` since
	// failures are by definition `err`) and is reconciled in a
	// follow-up PR. Threshold traffic is captured here so §12
	// traffic-anomaly recording rules (faas_apid_request_rate_5m,
	// faas_apid_error_rate_5m, _3d_baseline, _ratio) read from
	// one counter.
	requestTotal *prometheus.CounterVec
	// cveCheckTotal (issue #601 / ADR-131) is the per-CVE-check-run
	// counter the meterd scraper pushes from the .github/workflows/
	// cve-check.yml nightly artifact. Labelled by {result, severity}
	// where result ∈ {pass, fail, new_cve} (the three terminal
	// states of a run) and severity ∈ {low, medium, high, critical}
	// (the four severities the workflow filters). Pre-instantiated
	// at the 12-cartesian (3 results × 4 severities) so dashboards
	// render from boot. The Prometheus alert FaasNewCve reads the
	// `result="new_cve"` rows to page on a fresh CVE ≥ medium.
	cveCheckTotal *prometheus.CounterVec
	// cvesOpenTotal (issue #601 / ADR-131) is the per-(severity,
	// dep) gauge the cve-check workflow pushes from grype's filtered
	// output. dep is the package name + version (e.g.
	// "libssl@1.1.1k-7"); severity is the closed-set vocabulary
	// above. The dep label cardinality is bounded by the package
	// list — single-digit thousands at worst, well under the
	// Prometheus guideline. Recorded alongside cveCheckTotal so the
	// §12 dashboard's "Open CVEs by severity" stack panel reads
	// from one metric.
	cvesOpenTotal *prometheus.GaugeVec
	// appErrorsRecorded (ADR-096) — counter the gatewayd-internal
	// recorder (cmd/gatewayd-internal/app_errors_recorder.go) and
	// the apid gRPC handler (cmd/apid/grpc_server_apperrors.go)
	// increment per outcome. outcome ∈ {ok, redaction_failed,
	// rate_limited, db_error}. `ok` is the §12 customer-error-
	// ingest panel (rate over 5m); `redaction_failed` is the
	// tripwire for pkg/redact panicking (must stay at 0); the
	// closed label set keeps cardinality bounded. Single-registry:
	// registered on every daemon; only gatewayd-internal +
	// apid increment via ObserveAppErrorsRecorded.
	//
	// NOTE (post-review): `db_error` increments per-row on EVERY
	// failure — not just the 5th-consecutive. A batch is drained
	// from the ringbuffer in tryFlush BEFORE flushBatch is called,
	// so a failure means those rows are already lost; suppressing
	// the per-row observe until the tripwire fires would let the
	// first four failures of every outage disappear from the
	// dashboard.
	appErrorsRecorded *prometheus.CounterVec
	// requestTelemetryRecorded (ADR-127 PR-B) — counter the
	// apid gRPC handler (cmd/apid/grpc_server_request_telemetry.go)
	// increments per outcome. outcome ∈ {inserted, rate_limited,
	// db_error}. `inserted` is the §12 customer-telemetry-ingest
	// panel (rate over 5m). `rate_limited` is the per-account
	// token-bucket overflow path (DebugTelemetryRequestsPerMinute
	// exceeded). `db_error` is the per-row INSERT failure path.
	// The closed label set keeps cardinality bounded.
	// Single-registry: registered on every daemon; only apid
	// increments via IncrementRequestTelemetryRecorded.
	requestTelemetryRecorded *prometheus.CounterVec
	// gatewaydPublicOtelSpansIngested (ADR-127 PR-D) — counter
	// the gatewayd-public POST /v1/otel/v1/traces handler
	// increments per outcome. outcome ∈ {inserted, rate_limited,
	// db_error, shape_invalid}. `inserted` is the
	// customer-telemetry-spans panel (rate over 5m).
	// `rate_limited` is the per-account token-bucket overflow
	// path (DebugTelemetryRequestsPerMinute exceeded). `db_error`
	// is the apid WriteSpansSummary RPC failure path.
	// `shape_invalid` is the OTLP body parser rejection
	// (malformed JSON, missing trace_id, etc). The closed label
	// set keeps cardinality bounded. Single-registry: registered
	// on every daemon; only gatewayd-public increments via
	// IncrementGatewaydPublicOtelSpansIngested.
	gatewaydPublicOtelSpansIngested *prometheus.CounterVec
	// gatewaydPublicOtelSpansTruncated (ADR-127 PR-D) —
	// truncation tripwire counter the gatewayd-public OTel
	// handler increments once per POST whose inbound body
	// exceeded DebugTelemetrySpansPerTrace. Unlabelled —
	// single-registry pattern; only gatewayd-public increments
	// via IncrementGatewaydPublicOtelSpansTruncated.
	gatewaydPublicOtelSpansTruncated prometheus.Counter
	// gatewaydPublicOtelAuthFailures (ADR-127 PR-D) —
	// counter the gatewayd-public OTel handler increments when
	// the apid AuthenticateKey RPC rejects the bearer token or
	// the plan doesn't include telemetry. reason ∈
	// {unauthenticated, plan_disabled, internal}. Single-registry:
	// registered on every daemon; only gatewayd-public increments
	// via IncrementGatewaydPublicOtelAuthFailures.
	gatewaydPublicOtelAuthFailures *prometheus.CounterVec
	// spansWriteOutcomes (ADR-127 PR-D) — counter the apid
	// SpansWriter gRPC receiver increments per outcome.
	// outcome ∈ {inserted, rate_limited, db_error}. `inserted`
	// is a successful UPDATE on request_telemetry.spans_summary.
	// `rate_limited` is the per-account token-bucket overflow
	// on the write path. `db_error` is the per-row UPDATE
	// failure. The closed label set keeps cardinality bounded.
	// Single-registry: registered on every daemon; only apid
	// increments via IncrementSpansWriteOutcome.
	spansWriteOutcomes *prometheus.CounterVec
	// appErrorsFingerprintCacheHits (ADR-096) — counter for the
	// gatewayd-internal recorder's in-process LRU fingerprint
	// cache. Every hit is a record() that did NOT need to call
	// the regex / dedupe logic; every miss did. The hit rate
	// (over 5m, divided by requestFailures) is the §12
	// fingerprint-cache-effectiveness panel; a sustained <80%
	// rate means the LRU is too small for the customer's traffic
	// shape. Unlabelled — no cardinality risk. Single-registry:
	// registered on every daemon; only gatewayd-internal
	// increments via ObserveAppErrorsFingerprintCacheHit.
	appErrorsFingerprintCacheHits prometheus.Counter
	// appErrorsDedupeMerges (ADR-096) — counter for server-side
	// dedupe-merge hits in the apid gRPC handler. Increments
	// once per ON CONFLICT (account_id, app_id, fingerprint) DO
	// UPDATE that bumps the existing row's count + last_seen_at
	// (within AppErrorsDedupeWindowSeconds). The dedupe-merge
	// rate vs the new-fingerprint insert rate is the §12
	// dedupe-effectiveness panel; if 100% of inserts are merges,
	// the LRU backstop in the recorder is effectively never
	// firing. Unlabelled. Single-registry: registered on every
	// daemon; only apid increments via ObserveAppErrorsDedupeMerge.
	appErrorsDedupeMerges prometheus.Counter
	// appErrorsFlushDuration (ADR-096) — histogram of the
	// gatewayd-internal publisher's per-flush wall-clock
	// duration (FlushInterval or FlushBatchSize, whichever
	// first). Bucket set {0.001, 0.005, 0.01, 0.05, 0.1, 0.5,
	// 1, 5}: the 1ms bucket catches the empty-drain case
	// (FlushInterval fires with nothing to do); the 5s bucket
	// catches a stuck DB connection (alertable via
	// FlushDurationP99Slo). Unlabelled. Single-registry:
	// registered on every daemon; only gatewayd-internal
	// increments via ObserveAppErrorsFlushDuration.
	appErrorsFlushDuration prometheus.Histogram
	// appErrorsPurges (ADR-096) — counter for the apid retention
	// cron (cmd/apid/app_errors_purge.go). outcome ∈
	// {ok, no_accounts, failed}. `ok` is the §12 retention-
	// enforcement panel (rate over 24h, MUST be >0 once an
	// account iterator lands); `no_accounts` is the signal that
	// the cron ran but had no accounts to walk (PR-A ships with
	// no iterator wired, so this fires every 24h until PR-B);
	// `failed` is the tripwire for a SQL-level failure (alertable).
	// Single-registry: registered on every daemon; only apid
	// increments via ObserveAppErrorsPurge.
	appErrorsPurges *prometheus.CounterVec
	// previewJanitorOutcomes (ADR-095 PR-C) — counter for the
	// preview teardown cron (cmd/apid/preview_janitor.go).
	// outcome ∈ {ok, failed, torn_down}. `ok` fires every tick
	// the sweep ran cleanly (whether or not it tombstoned a
	// row); `torn_down` fires per-row when a preview app
	// reaches the torn_down state and is soft-deleted (the
	// §12 teardown-rate panel sums this over 1h to chart PR-
	// close cadence); `failed` is the tripwire for a SQL-level
	// failure (alertable). Single-registry: registered on every
	// daemon; only apid increments via ObservePreviewJanitor.
	previewJanitorOutcomes *prometheus.CounterVec
	// dataUpstreamRTT (ADR-098 PR-C) — observed RTT bucketed
	// per (kind, host_redacted_hash, region). The label set is
	// closed: kind ∈ closed-vocab; host_redacted_hash is the
	// 8-hex prefix of sha256(salt||host); region is the meterd
	// node's compute_nodes.region. §11: the plaintext host NEVER
	// appears in any label. Histogram (not Gauge) — the alert
	// rules at faas.rules.yml:1062-1087 read p95 RTT via
	// histogram_quantile() over the `_bucket` series.
	dataUpstreamRTT *prometheus.HistogramVec
	// dataUpstreamProbes (ADR-098 PR-C) — counter for the
	// probe outcome class. outcome ∈ {ok, timeout, refused,
	// tls_handshake, dns, unreachable}.
	dataUpstreamProbes *prometheus.CounterVec
	// dataUpstreamProbeDuration (ADR-098 PR-C) — wall-clock
	// duration of each probe (TCP+TLS handshake).
	dataUpstreamProbeDuration prometheus.Histogram
	// accountLabels: the bounded admission set shared by the
	// account_id-labelled metrics above. See accountLabelSet docs
	// for the fixed-capacity, non-evicting contract — an evicting
	// LRU would let evicted ids re-admit later and grow the series
	// set unbounded over process lifetime.
	accountLabels *accountLabelSet
	// failedLoginTotal: SOC 2 CC7.2 evidence (issue #286). Counts
	// every failed login attempt on the dashboard auth surface
	// (`POST /login`, `POST /signup`, OAuth callbacks), labelled by
	// the source IP. Bounded by ipLabelSet (maxIPLabelValues) so
	// the Prometheus TSDB series set stays bounded over the daemon's
	// lifetime — overflow collapses to "__other__". Backs the
	// FaasFailedLoginSpike alert at 20/min/IP/5m.
	failedLoginTotal *prometheus.CounterVec
	// failedLoginDropped: tracks rows that the apid async-batched
	// audit channel dropped because the in-process buffer was full
	// (4030+ sustained failed-logins/sec would do this under a
	// credential-stuffing burst). Unlabelled — the operator only
	// needs to know IF the channel is shedding load, not which IP
	// is causing it. A non-zero rate is the canary for "the
	// auth_limit bucket is shedding the attack but the audit
	// flusher is the bottleneck".
	failedLoginDropped prometheus.Counter
	// failedLoginAuditWriteFailures: counts apid-side failed-login
	// audit writes (cmd/apid/audit.go::flushOne) whose events row
	// could not be written. Distinct from auditWriteFailures
	// (which serves the success-path AuditEmit surface and is
	// labelled by account_id) because a failed login cannot be
	// attributed to a known account — routing both failure paths
	// through AuditWriteFailures("") would conflate "subject is
	// nil because nobody is logged in" with "subject is nil
	// because the caller didn't supply one" in the operator's
	// `account_id="anonymous"` view. Unlabelled — the same
	// subject-nil rationale applies, and a count over a fixed
	// baseline is enough to drive a "the flusher can talk to
	// Postgres?" question.
	failedLoginAuditWriteFailures prometheus.Counter
	// auditEventsDeletedTotal: rows pruned by the daily event-
	// retention cleanup loop (pkg/eventretention.Cleanup, ADR-075).
	// Unlabelled — the loop has one outcome (rows deleted) and the
	// per-kind breakdown lives on auditEventsVolumeTotal{kind_prefix}
	// instead. Backs the "is the prune loop keeping up?" runbook
	// question (docs/runbooks/FaasAuditRetentionExhaustion.md).
	// ADR-091 D20.3 / PR-B residual.
	auditEventsDeletedTotal prometheus.Counter
	// auditEventsRetentionLagSeconds: gauge of (now − cutoff) at the
	// moment each retention pass runs. A positive value means the
	// loop is keeping up; a value pinned at 0 means the loop is
	// running but never deleting anything (operator action needed
	// if the events table is growing). Backs the same runbook as
	// auditEventsDeletedTotal. The gauge is set (not Inc) so the
	// value reflects the LATEST pass, not a cumulative sum.
	// ADR-091 D20.3 / PR-B residual.
	auditEventsRetentionLagSeconds prometheus.Gauge
	// deploymentAuditGCRowsDeletedTotal: rows pruned by the
	// deployment_audit retention cron (pkg/meter/
	// RetentionOnceDeploymentAudit, 90-day window). SAFE-
	// RELEASES production-leveling Stream D (issue #976 /
	// ADR-122 post-merge audit) — without this counter an
	// operator can't tell whether the GC is keeping up or the
	// table is silently growing. Unlabelled — the loop has
	// one outcome (rows deleted); the per-kind breakdown
	// stays on the deployment_audit_at_gc_idx index (planner
	// range-scans the at column) + the dashboard's audit
	// timeline drill-down.
	deploymentAuditGCRowsDeletedTotal prometheus.Counter
	// domainDoctorOldestObservationSeconds (ADR-120 Tier A1): gauge
	// of (now − min(observed_at)) across every row in
	// domain_doctor_observations at the moment each doctor pass
	// completes. A positive value means the loop is keeping up; a
	// value pinned at 0 with a non-empty row set means rows exist
	// but the per-domain probe is failing to refresh
	// observed_at (operator action needed if the per-domain
	// dashboard "last check Xm ago" timeline is going stale).
	// Unlabelled — the gauge is a fleet-wide signal, the per-domain
	// staleness is rendered in the dashboard. The gauge is set (not
	// Inc) so the value reflects the LATEST pass, not a cumulative
	// sum. Backs the FaasDomainDoctorStalled / FaasDomainDoctorStretched
	// alerts (docs/runbooks/FaasDomainDoctorStalled.md).
	domainDoctorOldestObservationSeconds prometheus.Gauge
	// domainDoctorSkippedFlagDisabled (ADR-120 Tier A1): counter of
	// doctor passes skipped because the operator set
	// FAAS_DOMAIN_DOCTOR_ENABLED=false. Labelled by daemon=apid
	// (single label, fixed string — the single-registry pattern
	// demands the field is present on every daemon's OpsMetrics so
	// dashboards don't lose the series on a fleet roll-out, but
	// only apid increments since only apid runs the doctor loop).
	// Operator can correlate a non-zero rate with a customer
	// "why is my domain not being checked?" ticket.
	domainDoctorSkippedFlagDisabled prometheus.Counter
	// debugRegressionOldestPassSeconds (ADR-127 PR-B): gauge of
	// (now − max(last_pass_at)) across every
	// debug_regression_observations row that the most recent
	// regression cron tick refreshed. The regression cron ticks
	// every 5 minutes and writes one row per (app, deployment,
	// route) regression observed in the prior window. A positive
	// value means the loop is keeping up; a value pinned at 0
	// with a non-empty row set means regressions exist but the
	// cron is failing to refresh last_detected_at (operator
	// action needed). Unlabelled — the gauge is a fleet-wide
	// signal, per-app staleness is rendered in the dashboard.
	// Backs FaasDebugRegressionStalled (page, 30m).
	debugRegressionOldestPassSeconds prometheus.Gauge
	// debugRegressionSkippedFlagDisabled (ADR-127 PR-B): counter
	// of regression cron passes skipped because the operator set
	// FAAS_DEBUG_TELEMETRY_ENABLED=false (or DebugTelemetryEnabled
	// is false on every account the cron enumerated). Single
	// label, fixed string "apid" — single-registry pattern so
	// dashboards keep the series on a fleet roll-out, only apid
	// increments. Backs the "regression cron not running"
	// operator view.
	debugRegressionSkippedFlagDisabled prometheus.Counter
	// debugRegressionDetected (ADR-127 Debugger UX v1): counter
	// of fresh debug_regression_observations rows persisted by
	// the regression cron (cmd/apid/debug_regression_cron.go).
	// Increments once per (deployment, route) PRIMARY KEY upsert
	// that lands a NEW row; existing-row refreshes are silent
	// (PRIMARY KEY clash — same key only updates affected/p95).
	// Backs the FaasDebugRegressionDetected page-tier alert.
	debugRegressionDetected prometheus.Counter
	// auditEventsVolumeTotal{kind_prefix}: counts emit calls to the
	// events table by kind prefix (auth.*, key.*, secret.*,
	// account.*, stateless.*, webhook.*, edge_rule.*, cron.*,
	// "" / unmatched). kind_prefix is the bounded-admission label
	// — overflow collapses to "__other__" via the wire admission
	// helper so Prometheus series stay bounded even if a future
	// kind namespace blows up. Backs the "is the audit write rate
	// healthy per kind?" dashboard panel. ADR-091 D20.3 / PR-B
	// residual.
	auditEventsVolumeTotal *prometheus.CounterVec
	// alertEvalSkippedDegradedTotal — counts alert-rule evaluation
	// ticks skipped because pkg/appmetrics returned a degraded source
	// string ("degraded: <reason>"). Unlabelled. The dashboard's
	// "alert evaluation health" panel renders this counter next to
	// alertEvalFiredTotal so the operator can see a Prometheus
	// outage correlate with a spike of skipped evaluations.
	alertEvalSkippedDegradedTotal prometheus.Counter
	// alertEvalFiredTotal — counts alert-rule evaluations where
	// the comparison crossed the threshold AND ClaimAlertFire won
	// the cool-down race. Unlabelled. Paired with
	// alertEvalSkippedDegradedTotal for the dashboard panel.
	alertEvalFiredTotal prometheus.Counter
	// canaryProgressionAdvancedTotal (issue #976 / ADR-122 /
	// SAFE-RELEASES-A) counts every canary step boundary crossed
	// by the meterd tick (canary_step → canary_step+1 with
	// elapsed >= current stage.Duration). Unlabelled. Mirrors
	// alertEvalFiredTotal as a fleet-level rollup counter.
	canaryProgressionAdvancedTotal prometheus.Counter
	// canaryProgressionErrorsTotal (issue #976 / ADR-122 /
	// SAFE-RELEASES-A) counts every per-row error inside the
	// canary_progression tick (atomic APID advance failure, etc.).
	// Labelled by reason ∈ {advance, list_in_flight}. Closed
	// vocabulary — unknown reasons drop to the no-op closure.
	canaryProgressionErrorsTotal *prometheus.CounterVec
	// canaryProgressionZeroTimestampTotal (SAFE-RELEASES code-review
	// hardening, migration 00517) counts every row the
	// canary_progression tick walks whose canary_step_started_at is
	// the zero time. Post-00517 the column is NOT NULL DEFAULT NOW(),
	// so this counter should never fire in steady state; a non-zero
	// rate is the tripwire for "a write path bypassed the apid Create
	// handler and left the column at the zero value" — exactly the
	// failure mode code-review finding #1 was worried about. The
	// runtime's behavior on zero is unchanged (still advances on the
	// first tick — elapsed = 56 years > Duration), but the operator
	// gets a fleet-level signal that the schema default was bypassed.
	// Unlabelled — fleet rollup; per-deployment detail lives in the
	// existing deploy.traffic_changed audit row.
	canaryProgressionZeroTimestampTotal prometheus.Counter
	// alertDeliveryAttemptsTotal — counts dispatched alert-rule
	// webhook attempts, labelled by outcome ∈ {delivered, failed}.
	// Label cardinality budget = 2 (closed vocabulary). The counter
	// surfaces the dispatcher's success rate without exposing
	// per-customer detail (account_id is intentionally absent — the
	// audit events table is the per-customer detail; this is the
	// fleet-wide counter for the §12 dashboard).
	alertDeliveryAttemptsTotal *prometheus.CounterVec
	// alertActionExecutedTotal (issue #976 / ADR-122 /
	// SAFE-RELEASES-B) counts alert-rule firings that triggered an
	// in-process action beyond the legacy webhook fan-out (rollout
	// rollback / demote / promote). Labelled by action ∈
	// {rollback, demote, promote} (closed vocabulary — 'webhook'
	// is the no-action default and not labelled here, so the metric
	// only ever sees non-trivial side-effects). The counter is the
	// §12 dashboard's "auto-rollback / auto-promote rate" panel;
	// a non-zero rate combined with alertDeliveryAttemptsTotal
	// (delivered) on the same rule family is the healthy signal.
	alertActionExecutedTotal *prometheus.CounterVec
	// paddleWebhookVerifyFailedTotal — counts Paddle webhook signature
	// verify failures (PR-P4). Unlabelled; the per-event detail
	// (event_id, err message, tolerance) lives in the journal line
	// emitted by cmd/apid/handlers_ext.go::paddleWebhook ("paddle_webhook.verify_failed")
	// and in the response body of the 400 RFC 7807 problem. The counter
	// is the fleet-level tripwire for "wrong webhook secret in
	// dashboard" or "clock skew beyond tolerance". The handler is the
	// only incrementer (apid only); the field exists on every daemon
	// per the single-registry pattern. Mirrors alertEvalSkippedDegradedTotal
	// (line 288) — both are unlabelled fleet-level signals with no PII.
	paddleWebhookVerifyFailedTotal prometheus.Counter
	// paddleWebhookReplaySuppressedTotal — counts Paddle webhook events
	// rejected by pkg/webhookdedupe (PR-P4). Unlabelled; the per-event
	// detail (delivery_id) lives in the existing webhook.replay_rejected
	// audit row. The counter is the fleet-level tripwire for "Paddle is
	// redelivering" — a sustained rate over 30 min is the alert. The
	// handler is the only incrementer (apid only); the field exists on
	// every daemon per the single-registry pattern. Mirrors
	// alertEvalFiredTotal (line 293).
	paddleWebhookReplaySuppressedTotal prometheus.Counter
	// alertEvaluatorEnabled — operator-facing gauge scraped by
	// /healthz and the dashboard's alert-evaluation-health panel.
	// 1 when the Evaluator tick is wired and running on the current
	// meterd process; 0 when it isn't (Prometheus not configured,
	// FAAS_HOST_AGE_IDENTITY_PATH empty, meterd's caller skipped the
	// loop, etc). Unlabelled — the gauge is a fleet-level status
	// signal, not a per-rule status. Pair with
	// alertEvalFiredTotal / alertEvalSkippedDegradedTotal for the
	// operator's "is meterd actually evaluating rules?" view.
	alertEvaluatorEnabled prometheus.Gauge

	// Issue #1233 / ADR-123 — alert-preset signal gauges.
	// meterdAccountSpendEur (account_id) is the MTD EUR spend
	// computed by the meterd tick loop and stamped every
	// AlertEvalInterval. Cardinity is bounded by account count.
	meterdAccountSpendEur *prometheus.GaugeVec
	// apidTenantSurfaceCertExpirySeconds (account_id, app_id,
	// hostname) is the per-host cert-expiry gauge fed by the
	// meterd_tenant_surface_cert_expiry_state walker
	// (CLAUDE.md ownership rule: this is a derived signal cache,
	// not customer intent, so the meter daemon owns the writer
	// side; the apid process only reads via state.MinCertExpiryForApp).
	// The alert evaluator's cert_expiry_seconds metric reads MIN
	// across the label set.
	apidTenantSurfaceCertExpirySeconds *prometheus.GaugeVec
	// apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal
	// (result) is the walker's fleet-level status counter —
	// {ok, error} closed vocabulary. Surfaces a healthy vs
	// failing walker for the §12 self-healing alert. The
	// accessor is exposed as a `meterd_*` sibling at
	// MeterdTenantSurfaceCertExpiryRefresherWalkCompleteTotal
	// (renamed in the ADR-123 ownership review) to make the
	// meter-daemon-writer side explicit.
	apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal *prometheus.CounterVec
	// meterdAPIReachable (account_id, app_id) is the per-{app}
	// reachability gauge fed by cmd/meterd/api_reachability_sweep.go
	// every AlertEvalInterval: 1.0 when the app has served a
	// successful invocation within the last 5 minutes, 0.0 when
	// it has not. Backs the alert preset api_down. The alert
	// evaluator reads the same data inline via
	// state.WasInvokedSuccessfullySince; this gauge is the
	// operator-visible mirror. PR-B / issue #1233 closes the
	// "meterd_api_reachable{...} exists" prerequisite that ADR-123
	// §Follow-ups calls out before flipping enabled_in_catalog=true.
	meterdAPIReachable *prometheus.GaugeVec
	// apidDeploymentFailedTotal (account_id, app_id) is the
	// per-{app} delta counter of deployments whose status
	// transitioned to 'failed' since the previous sweep. Fed by
	// cmd/meterd/deployment_failure_sweep.go every AlertEvalInterval.
	// Backs the alert preset deploy_failed. The alert evaluator
	// reads the same data inline via state.CountFailedDeploymentsSince;
	// this counter is the operator-visible mirror. PR-B / issue
	// #1233.
	apidDeploymentFailedTotal *prometheus.CounterVec
	// standbyState — operator-facing enum gauge stamped by
	// gatewayd-public on every active-passive HA state transition
	// (Tier A8 / ADR-083). Values are 1/2/3 mapped to the
	// closed vocabulary {warming, warm, draining}; an un-set
	// gauge defaults to 0 (the "never seen" state — surfaces as
	// a "no data" rather than 0 to the alert rule). Pre-
	// instantiated to warming in NewOpsMetrics so the row
	// surfaces in /metrics from boot (precedent: alertEvaluator
	// Enabled above and pgBackupLastPushed below). The
	// `FaasStandbyStateWarmingTooLong` alert rule
	// (deploy/ansible/roles/prometheus/files/ha_failover.rules.yml)
	// fires when this gauge holds at 1 for > 60s on a node
	// where the operator has marked active=true — that's the
	// tripwire for a misconfigured DNS provider or a stuck
	// leader election. Unlabelled — single-box per-node state,
	// no fan-out needed today.
	standbyState prometheus.Gauge
	// standbyStateMu + standbyStateValue shadow the gauge so
	// StandbyState() can return the current int without scraping
	// /metrics (prometheus.Gauge exposes only Set/Inc/Dec/Add/Sub
	// /Write — no Value() accessor). Same precedent as
	// alertEvaluatorEnabledValue above. Lock contention is non-
	// issue: the gauge is stamped at boot, on
	// compute_node_changed, and on drain start — not per-tick.
	standbyStateMu    sync.Mutex
	standbyStateValue int
	// activePassiveFailoversTotal — Tier A8 / ADR-083
	// active-passive HA fail-over observability. Counter labelled
	// by outcome ∈ {dns_flipped, dns_stale, peer_unreachable,
	// manual_drain} — the closed set the dns_handoff orchestrator
	// (cmd/gatewayd-public/dns_handoff.go) can land in. `dns_flipped`
	// is the §12 dashboard panel (the active-passive flip succeeded
	// inside HADNSRecordStaleSeconds). `dns_stale` is the tripwire
	// for the DNS provider failing UpsertRecord after retries —
	// operationally distinct from `peer_unreachable` (which is the
	// pg_notify consumer falling behind). `manual_drain` is the
	// operator-initiated path. Single-registry: registered on every
	// daemon (mirrors liveMigrationDecisions / rebalanceDecisions /
	// migratingReconcileDecisions); only gatewayd-public increments
	// via ActivePassiveFailovers in production. Pre-instantiated in
	// NewOpsMetrics so the row surfaces in /metrics from boot.
	activePassiveFailoversTotal *prometheus.CounterVec
	// pgBackupLastPushed — operator-facing gauge stamped by the
	// apid's pgBackupPushedSampler (cmd/apid/main.go). Holds the
	// age of the newest tarball in /var/lib/pgsql/basebackup/ (in
	// seconds; 0 when the dir is empty). The PgBackupStale alert
	// (deploy/ansible/roles/prometheus/files/pg_backup.rules.yml,
	// issue #250) queries this gauge; the alert fires when the
	// value exceeds 86400 (24h). Unlabelled — single-box fleet, no
	// per-node fan-out needed today.
	pgBackupLastPushed prometheus.Gauge
	// alertEvaluatorMu + alertEvaluatorEnabledValue shadow the gauge
	// so AlertEvaluatorEnabled() can return a bool without scraping
	// /metrics or relying on a non-existent prometheus.Gauge.Value()
	// accessor (the interface exposes only Set/Inc/Dec/Add/Sub/Write).
	// Lock contention is non-issue: the gauge is stamped at boot and
	// potentially at identity-rotation time, not per-tick.
	alertEvaluatorMu           sync.Mutex
	alertEvaluatorEnabledValue bool
	// ipLabels: the bounded admission set shared by ip-labelled
	// metrics (failedLoginTotal today). See accountLabelSet docs
	// for the same fixed-capacity, non-evicting contract.
	// Distinct from accountLabels because the cap-sizing rationale
	// differs — IPs grow under attack, account_ids grow under
	// signup — and the two sets should not share capacity.
	ipLabels *ipLabelSet
	// topTenantRPS: introduced in issue #300 — a per-tenant RPS
	// gauge sampled every 5s by the daemon's topNSampler goroutine
	// (cmd/apid/topn.go / cmd/gatewayd-internal/listener.go). Bounded at
	// topAccountSetCap (1000) real customer ids plus the "other"
	// overflow bucket (see pkg/wire/topn.go). Layered above
	// accountLabelSet: an id admitted at the 10k level can still
	// be demoted past the top-1000 here. The gauge is a presentation
	// view over the already-bounded requestTotal counter, not a
	// separate source of truth. Help string documents the two-tier
	// cardinality contract for operators.
	topTenantRPS *prometheus.GaugeVec
	// topAccounts: the bounded top-N admission primitive that
	// drives topTenantRPS. See pkg/wire/topn.go for the contract.
	topAccounts *topAccountSet
	// throttleSecondsTotal (issue #301 / ADR-044): cumulative
	// vmmd CPU-throttled seconds per (account_id, app_id).
	// Counter (not Gauge) because the value is monotonic between
	// cgroup regressions — the rollup Adds the per-row delta
	// (curr - last) via the per-(account_id, app_id) baseline in
	// throttleSecondsLastSeen. The label tuple is bounded by
	// topAppSetCap (100) real cached pairs plus the
	// ("other", "other") overflow; the actual Prometheus series
	// set is also bounded by topAppSet's snapshot emission.
	// Layered above accountLabelSet the same way topTenantRPS
	// is: an (account_id, app_id) pair admitted at the 10k
	// account-id level can still be demoted past the top-100
	// here. The counter is the dashboard top-N view; the
	// FaasCpuStarvation alert (issue #301 acceptance #3) reads
	// from the sibling throttleRatio gauge instead.
	throttleSecondsTotal *prometheus.CounterVec
	// throttleRatio (issue #301 / ADR-044): per-slice throttle
	// ratio (throttle_delta / (throttle_delta + usage_delta))
	// over the 5s sampler window. Gauge (not Counter) because
	// the value is computed windowed, not cumulative. Labelled
	// by slice ∈ {tenant-free, tenant-hobby, tenant-pro,
	// tenant-scale}; the closed plan set is pre-instantiated at
	// boot so the dashboard surfaces "no data" → "0" on an
	// idle box. The FaasCpuStarvation alert reads
	// throttleRatio{slice=~"tenant-.*"} > 0.8 for 5m.
	throttleRatio *prometheus.GaugeVec
	// topApps: the bounded top-N admission primitive that
	// drives throttleSecondsTotal. Keyed on the composite
	// (account_id, app_id); cap = topAppSetCap (100). See
	// pkg/wire/topn_app.go for the contract. Sibling of
	// topAccounts (issue #300's per-customer noise primitive)
	// with a smaller cap and a composite key — see the
	// package doc on topn_app.go for why a separate type.
	topApps *topAppSet
	// throttleSecondsLastSeen: per-(account_id, app_id) baseline
	// for the cumulative throttleSecondsTotal counter. Mirrors
	// cpuSecondsLastSeen (issue #279 / PR-B) — on regression
	// (curr < last) the baseline is reset to curr and the
	// delta is 0, so the counter stays monotonic. See
	// cpuSecondsLastSeen for the full mutex + map contract.
	throttleSecondsLastSeen *cpuThrottleLastSeen
	// stripePushDur: introduced in feat/m7-stripe-push-observability.
	// Per-push latency to Stripe, labelled by terminal result code.
	// Distinct from the dur histogram (which labels by op only) because
	// card-declines (≈50 ms) and rate-limit stalls (≈5 s) belong in
	// different buckets — alerting on the rate_limit bucket is the
	// difference between "customer's card bounced" and "Stripe is
	// throttling us". Buckets cover the documented Stripe SLA (p99
	// ≈ 5 s, p99.9 ≈ 30 s); the 60 s ceiling is the documented API
	// timeout.
	stripePushDur *prometheus.HistogramVec
	// paddlePushDur: parallel to stripePushDur for the Paddle Billing v2
	// provider. The label set is paddle.PushResultLabels() — the Paddle
	// closed set has one substitution ("negative-quantity" → "negative-
	// mb-sec") and one addition ("overage-price-missing") vs Stripe;
	// both histograms are pre-instantiated with their own canonical
	// labels at boot (same precedent as stripePushDur). Sharing the
	// Stripe histogram would lose the closed-set distinction the
	// dashboard panel definitions depend on.
	paddlePushDur *prometheus.HistogramVec
	// polarPushDur is the per-push latency histogram for Polar event
	// ingestion. It uses polar.PolarPushResultLabels() as its closed label set.
	polarPushDur *prometheus.HistogramVec
	// wakeIDV4Fallback: introduced in feat/wake-id review followup
	// (gaps analysis 2026-07-23, finding #6). Increments when schedd
	// mints a wake_id and uuid.NewV7 returns an error — the engine
	// falls back to uuid.New (v4) in that case so a wake is never
	// refused for ID-generation reasons, but a v4 wake_id breaks the
	// time-ordering invariant the partial index is built on. Any
	// non-zero rate indicates a broken crypto/rand subsystem and
	// should alert. Unlabelled: one counter, no cardinality.
	wakeIDV4Fallback prometheus.Counter
	// snapshotDiskDrift: PR scale-out readiness #3 (read-only schedd
	// disk-drift sweep). One counter, no cardinality — drift is
	// process-level not per-snapshot. Each Tick walks /srv/fc/snap
	// and compares each <dep>/{mem,vmstate} file's size to the
	// corresponding snapshots.mem_bytes / disk_bytes row; each
	// discrepancy (missing file, size mismatch, unexpected entry,
	// non-regular entry) increments by 1. Persistent drift shows up
	// as a non-zero rate; the sweep never writes. Registered on every
	// daemon's OpsMetrics registry (single-registry pattern); only
	// schedd's Tick produces samples in production.
	snapshotDiskDrift prometheus.Counter
	// capacity_signature_rejected: ADR-053 §3 — every rejected
	// CapacityReport stream increments this counter once. See
	// CapacitySignatureRejected() accessor for the operator-facing
	// semantics. Registered on every daemon's OpsMetrics registry
	// (single-registry pattern); only schedd's ReportCapacity handler
	// produces samples in production.
	capacitySignatureRejected prometheus.Counter
	// buildDur / buildQueueWait: introduced in ADR-030 for builderd's
	// build lifecycle. Distinct from the dur histogram (which tops out
	// at 5 s — sub-millisecond control-plane sizing) because a build runs
	// up to the 10-min BuildTimeoutSeconds cap and a queued build can wait
	// on the single guaranteed builder slot. Same precedent as
	// stripePushDur (ADR-027): control-plane buckets are wrong for these
	// multi-second/multi-minute ops. Success/failure classification stays
	// on the shared ops counter as ops_total{op="build",code}; the duration
	// histogram carries an `outcome` label ({cache_hit,ok,failed}) so the
	// §12 panels can slice cleanly — cache hits run <1 s and would
	// otherwise drown the real-build p50/p95 in cache-hit noise. The queue-
	// wait histogram is unlabelled (every observation has the same shape).
	buildDur       *prometheus.HistogramVec
	buildQueueWait prometheus.Histogram
	// cpuStatsCollectDur: introduced for issue #279 / PR-B / ADR-039.
	// Wall-clock duration of the CPU-rate-and-accumulator read path
	// on the vmmd and schedd wires. Stored as prometheus.Histogram
	// (unlabelled) so the bucket set is sized to the actual hot path
	// (sub-ms per row) — the general `dur` histogram tops out at 5 s
	// and would lose all resolution here. nil on daemons that don't
	// expose the path (apid, imaged, builderd, gatewayd-internal, meterd,
	// githubd, faas CLI). Buckets: 100 µs → 100 ms.
	cpuStatsCollectDur prometheus.Histogram
	// residentGBPerCustomer: per-plan "resident GB-hours per paying
	// customer" gauge emitted by meterd (ADR-031, PR #141). Labelled
	// by plan ∈ {free, hobby, pro, scale} so the §12 dashboard's
	// "Resident GB per paying customer" panel can split by plan while
	// the FaasResidentGbPerCustomerHigh alert rule fans out per-plan.
	// Cardinality bounded at 4 — the closed plan set is enumerated
	// in the pre-instantiation loop below so every plan label surfaces
	// in /metrics from the moment the daemon boots.
	residentGBPerCustomer *prometheus.GaugeVec
	// billingCapExceededTotal: counter incremented by meterd's quota
	// tick every time the per-account overage_cap_cents is met and
	// the overage-row insert is skipped (issue #279). Labelled by
	// plan ∈ {free, hobby, pro, scale} so the §12 dashboard can
	// surface "how many accounts are at the cap right now" per plan.
	// A non-zero rate is informational: a cap-hit account is
	// operating as designed (the customer hit the operator-set
	// monthly ceiling), not a failure mode.
	billingCapExceededTotal *prometheus.CounterVec
	// meterdFloorAppliedTotal: counter incremented by meterd's sample
	// tick every time the per-app min_instances GB-h floor was
	// applied and synthetic usage_minutes rows were appended
	// (ADR-060, issue #515). Labelled by plan ∈ {free, hobby, pro,
	// scale}. Closed-set via api.Plans — the {app} label is
	// unbounded cardinality and stays out. Increment once per
	// (app, tick) — the SyntheticFloor bool is the in-memory
	// lineage marker; storage shape unchanged. A non-zero rate
	// indicates a customer-configured floor is active (Hobby /
	// Pro / Scale only; PR-A's PATCH gate rejects Free).
	meterdFloorAppliedTotal *prometheus.CounterVec
	// meteredMBSecondsTotal (issue #1186 / M-2 / ADR-137 §Decision 1):
	// per-{mode,plan} MB-seconds counter fed by the sample loop's
	// live-instance branch. mode ∈ {normal, worker, service, job} —
	// the closed billable subset of pkg/state.InstanceMode; mirror
	// is filtered upstream by IsMeteredSkippableMode and never
	// reaches the counter. plan ∈ {free, hobby, pro, scale} so the
	// §12 dashboard can split worker idle-RAM from
	// request-driven RAM. Increment is Add(mbSeconds) — the
	// counter is cumulative MB-seconds (NOT count of billable
	// rows), so a dashboard query of
	// rate(metered_mb_seconds_total{mode="worker"}[5m]) yields
	// MB/sec, summing cleanly against the request-mode rate.
	meteredMBSecondsTotal *prometheus.CounterVec
	// auditOrgEvent: closed-vocab counter for org-action authorization
	// outcomes (issue #190 / IAM-6 / ADR-061, PR 6). Labelled by
	// `action` only — the 11-verb AllOrgActions vocabulary is a
	// closed set, so cardinality is bounded at 11 × {allowed,
	// denied} = 22 series. Do NOT label by org — 11 × ~10K orgs
	// balloons past 100K Prometheus series, defeating the
	// scraper's TSDB compression (the audit_log Postgres table is
	// the per-org record; this metric is the aggregate rate
	// answer to "is org.delete denying more than usual?").
	//
	// authzDenied counts every AuthorizeOrgAction / RequireOrgAction
	// deny path (including the LoadOrg guard's "<no active org>"
	// 403 — see AuthorizeOrgAction in pkg/authz/authorize.go).
	// authzAllowed counts every allow path. Useful dashboard
	// question: "is org.delete deny rate spiking today?" is
	// `rate(audit_org_event{action="org.delete",result="denied"}[5m])`.
	auditOrgEvent *prometheus.CounterVec
	authzDenied   *prometheus.CounterVec
	authzAllowed  *prometheus.CounterVec
	// imagedOCIPull: per-call latency of imaged's OCI registry pulls
	// (manifest, config, blob, above-base). Sized to api.OCIPullTimeoutSeconds
	// (60 s); the 5 s control-plane bucket is wrong for the multi-second
	// blob downloads.
	imagedOCIPull *prometheus.HistogramVec
	// issue #170 / PR-A: per-{app,node} instance-stats gauges. The
	// (app, node) label tuple is unbounded because it grows with the
	// customer count, so it cannot be pre-instantiated at boot.
	// Instead, ReplaceInstanceStats calls Reset() on each Tick and
	// re-emits the present (app, node) pairs. Three signals:
	//   - instanceCPUPct: max over live siblings (peaks are what
	//     scaling cares about).
	//   - instanceRSSMB: sum over live siblings (capacity rollup).
	//   - instanceInflightReqs: sum over live siblings (load rollup).
	// Per-instance cardinality is NOT used — issue #168 allows N
	// siblings of one app on one node, and per-instance rollups
	// would nondeterministically overwrite siblings on .Set. The
	// per-instance values live in pkg/sched/instancestats.Reader;
	// the wire only carries the {app,node} rollup.
	instanceCPUPct       *prometheus.GaugeVec
	instanceRSSMB        *prometheus.GaugeVec
	instanceInflightReqs *prometheus.GaugeVec
	// instanceCPUSecondsTotal (issue #279 / PR-B): cumulative
	// CPU-seconds per (app, node). Counter (not Gauge) because
	// the value is monotonic between cgroup regressions — the
	// rollup Adds the per-row delta. Pre-instantiated with the
	// empty (app, node) tuple so the help/TYPE surfaces in
	// /metrics from boot.
	instanceCPUSecondsTotal *prometheus.CounterVec
	// cpuSecondsLast (issue #279 / PR-B): per-(app, node) last
	// observed cumulative CPUSeconds. The rollup Adds
	// (curr - last) to the CounterVec; on regression
	// (curr < last) we reset the baseline and add 0, keeping
	// the counter monotonic. Mirrors the accountLabelSet
	// pointer-receiver pattern at the bottom of this file.
	cpuSecondsLast *cpuSecondsLastSeen
	// instanceStatsCollectDur: per-Tick wall-clock duration of the
	// instancestats poller. Sized to the 200 ms poller interval.
	instanceStatsCollectDur prometheus.Histogram
	// instanceStatsPartialErrors: per-node dial/decode failures.
	// Distinct from the per-op ops_total because the poller
	// intentionally prefers partial snapshots to aborting on a
	// single bad node.
	instanceStatsPartialErrors *prometheus.CounterVec
	// sidecarRestartTotal (issue #463 / ADR-069 / ADR-071 / PR-C
	// §4): per-(app, sidecar) restart-counter. Sidecar
	// supervisors (guest/init::Supervisor.OnCrash) fire this
	// whenever an essential sidecar crash restarts; the host
	// (cmd/vmmd::dispatchSidecarRestart) increments the
	// CounterVec via ObserveSidecarRestart. Cardinality is
	// bounded by apps × SidecarCapMax (max 2) so a worst-case
	// Scale plan with 100 apps × 2 sidecars = 200 series, well
	// under Prometheus' "tens of thousands of series per
	// metric" guideline. The counter is pre-instantiated with
	// the empty (app, sidecar) tuple so /metrics surfaces zero
	// at boot; the sidecar_label set is the bounded
	// {__unknown__, <custom name>} admission per the
	// accountLabelSet precedent at the bottom of this file.
	sidecarRestartTotal *prometheus.CounterVec
	// scaleUpDecisions: per-app scale-up trigger decisions (issue #169 /
	// #172). Counter labelled by app_id and outcome ∈ {admit,
	// reject_at_cap, no_signal}. App cardinality is bounded
	// by the number of apps with autoscale configured — the trigger
	// emits one row per decision. Outcomes are pre-instantiated so the
	// series surface in /metrics from boot (same precedent as
	// stripePushDur / buildDur).
	scaleUpDecisions *prometheus.CounterVec
	// scaleDownDecisions: per-app aggressive-reaper decisions
	// (issue #171). Counter labelled by app_id and outcome ∈ {park,
	// keep}; one observation per app per 10 s reaper tick that ran
	// the aggressive path. Symmetric with scaleUpDecisions — same
	// outcome-pre-instantiation, same app cardinality bound (apps
	// with autoscale configured OR apps with min_instances set).
	scaleDownDecisions *prometheus.CounterVec
	// floorReconcileDecisions: per-app proactive min-instances floor
	// decisions (issue #557 / ADR-071 §Decision 1). Counter labelled
	// by app_id and outcome ∈ {admit, floor_met, disabled,
	// at_capacity, ram_ceiling, cooldown_held, error, backoff_held}.
	// App cardinality is bounded by Hobby+ apps with
	// min_instances > 0; outcomes pre-instantiated so the series
	// surface in /metrics from boot. Same precedent as
	// scaleUpDecisions.
	floorReconcileDecisions *prometheus.CounterVec
	// floorReconcileErrors: per-app floor trigger error counter.
	// Labelled by app_id and kind ∈ {admit_denied, admit_error}.
	// Alert on `rate(...) > 0 for 5m` — sustained errors mean the
	// customer's floor isn't being satisfied.
	floorReconcileErrors *prometheus.CounterVec
	// floorInstancesAdmitted: global counter, incremented once per
	// successful proactive floor wake. Used as the "is the floor
	// working" baseline; sustained-zero on a tier with Hobby+
	// accounts is a quiet alarm (the dashboard "warm floor" panel).
	floorInstancesAdmitted prometheus.Counter
	// scaleUpAdmitRPS: per-instance RPS at the moment the trigger
	// admitted a new instance. Sized to the per-instance RPS target
	// range (1–1000); p95/p99 over this histogram is the spec §12
	// "scale-up aggressiveness" diagnostic. Unlabelled: every
	// observation has the same shape.
	scaleUpAdmitRPS prometheus.Histogram
	// sseClients: live count of open /v1/events SSE connections
	// (Move 3, M7.5 prep). Unlabelled — the §12 panel is "how many
	// concurrent dashboard viewers" and the per-plan split is
	// observable from existing apid_ops_total{op="events"} + the
	// plan from /v1/account, not a separate label. The gauge is
	// incremented in handlers_events.go at the top of the handler
	// and decremented via defer. Zero is the expected idle value.
	sseClients prometheus.Gauge
	// apidLogsEmittedTotal: per-app SSE log frame counter (issue #254,
	// Move 4). Labelled by app (apps.id, UUID). Registered on every
	// daemon's OpsMetrics so the struct stays a single-registry
	// (per memory wire-opsmetrics-single-registry) — only apid
	// increments via ObserveLogEmitted; non-apid daemons leave the
	// CounterVec at zero. The series pre-instantiation is bounded by
	// the per-account app quota (Hobby=5, Pro=25, Scale=100), so
	// cardinality stays well inside Prometheus' "tens of thousands
	// of series per metric" guideline.
	apidLogsEmittedTotal *prometheus.CounterVec
	// apidLogsDroppedTotal (issue #309 / tier-2 DX): per-reason
	// drop counter for log frames the platform filtered or the
	// ring dropped. Closed `reason` label set
	// {slow_subscriber, filter_grep, filter_level} — the three
	// drop sites:
	//   - slow_subscriber: pkg/fcvm/logbuf/ring.go::Write default
	//     branch when the ring is full (the LogSink consumer is
	//     behind; ring.Write drops the line and returns
	//     ErrRingFull).
	//   - filter_grep: schedd server's StreamAppLogs sink
	//     callback dropped the line because it did not match the
	//     customer-supplied --grep regex (issue #309).
	//   - filter_level: same site dropped the line because the
	//     heuristic --level matcher classified it below the floor.
	// 3 closed-set series; pre-instantiated so /metrics surfaces
	// zero from boot. Single-registry: registered on every
	// daemon; only vmmd (slow_subscriber) and schedd (filter_*)
	// increment via IncLogDropped. The metric name mirrors the
	// docs comment at pkg/fcvm/logbuf/ring.go:181,230 that
	// pre-dates this counter — the wire-side field formalises
	// what was previously a TODO marker.
	apidLogsDroppedTotal *prometheus.CounterVec
	// oauthDisabledTotal: issue #419 / ADR-046. Sign-in OAuth
	// disabled-redirect counter. Labelled by provider ∈
	// {google, github} — the closed set pkg/auth.SignInConfig
	// knows about. Pre-instantiated in NewOpsMetrics so both
	// rows surface in /metrics from the moment the daemon boots,
	// matching the provenance_writes_total precedent (a panel
	// querying `rate(apid_oauth_disabled_total[5m])` shows zero
	// on a healthy box and a non-zero rate when the dashboard
	// gating is bypassed or a customer hits a stale bookmark.
	// Single-registry: registered on every daemon; only apid
	// increments via ObserveOAuthDisabled.
	oauthDisabledTotal *prometheus.CounterVec
	// advisoryBatchesEmittedTotal: stateless-advisory forward
	// outcomes (Mega-PR B). Labelled by `result` ∈
	// {ok, dial_failed, rejected, unavailable_after_retry} — the
	// four observable outcomes from vmmd's AdvisoryClient
	// (pkg/vmmdgrpc/advisory_client.go). Closed set pre-instantiated
	// in NewOpsMetrics so the §12 dashboard panel surfaces zero on
	// a healthy box and surfaces a non-zero rate for any of the
	// three failure paths. Single-registry: registered on every
	// daemon; only vmmd increments via ObserveAdvisoryBatchResult.
	// Increments per BATCH (one ForwardStatelessAdvisory RPC), not
	// per event — the path-level taxonomy at vmmd is batch-grained.
	advisoryBatchesEmittedTotal *prometheus.CounterVec
	// apidStatelessAdvisoryEventsTotal: per-batch inbound advisory
	// counter on the receiver side (Mega-PR B). Labelled by
	// `severity` ∈ {high, warn, info} — mirrors the same vocabulary
	// the receiver already computes via advisoryBatchSeverity. Closed
	// set pre-instantiated in NewOpsMetrics so the panel surfaces
	// zero at boot. Single-registry: registered on every daemon;
	// only apid increments via ObserveStatelessAdvisory. Symmetric
	// pair with advisoryBatchesEmittedTotal — that one counts the
	// producer side (vmmd forward outcomes), this one counts the
	// consumer side (apid landed advisories).
	apidStatelessAdvisoryEventsTotal *prometheus.CounterVec
	// apidGithubdBridgeEnqueuedTotal: per-app build enqueue
	// counter from the githubd → apid bridge (issue #432 phase 5).
	// Labelled by `kind` ∈ {github} — the closed set of build
	// sources the githubd bridge produces. Pre-instantiated in
	// NewOpsMetrics so the row surfaces in /metrics from boot
	// (the §12 dashboard panel queries the rate to surface
	// github-triggered build volume). Single-registry: registered
	// on every daemon; only apid increments via
	// IncGithubdBridgeEnqueued.
	apidGithubdBridgeEnqueuedTotal *prometheus.CounterVec
	// egressDeny: per-CIDR drop counter for the nftables egress
	// denylist (PR-E). Labelled by (cidr, family) — the cidr label
	// is the DenyEntry.CounterName (e.g. "drop_v4_10_0_0_0_8") and
	// the family is the nft family keyword ("ip" / "ip6"). The vmmd
	// scrape adapter (cmd/vmmd/poller.go) reads `nft list counters`
	// every 15s and emits the per-counter delta so the Prometheus
	// series sees the rate of drops per CIDR. The imaged side uses
	// a separate metric (oci_egress_deny_total) wired in cmd/imaged
	// directly because the OCI dialer is user-space — nftables
	// counters do not see it. Cardinality bounded by the catalog
	// size (~12 v4 + 7 v6 = 19 series per renderer); closed set
	// pre-instantiated from netns.NewDefaultDenySet() at boot so the
	// panels surface even on an idle box.
	egressDeny *prometheus.CounterVec
	// ociEgressDeny: PR-E sister collector to egressDeny for the
	// user-space OCI dialer. Registered ONLY on the imaged OpsMetrics
	// (prefix = "imaged") so the metric surfaces as
	// imaged_oci_egress_deny_total{cidr,family}; on every other
	// daemon (vmmd, schedd, ...) the field stays nil. Disambiguating
	// the metric name from egressDeny is the operator's contract: a
	// "firewall blocked it" hit increments egressDeny on vmmd, a
	// "dialer refused it" hit increments ociEgressDeny on imaged,
	// and the two have different remediation paths (nftables rule
	// vs. denylist catalog edit). Cardinality is identical to
	// egressDeny — same catalog, same (cidr, family) label set.
	ociEgressDeny *prometheus.CounterVec
	// ownershipClamp / layerEntrySkipped: M-1 / ADR-136 §Decision 2
	// imaged-only counters. Registered ONLY when prefix == "imaged"
	// (mirrors ociEgressDeny); on every other daemon the fields
	// stay nil and the accessors below no-op. pkg/rootfs.ApplyLayer
	// increments via the public accessors in cmd/imaged/main.go.
	ownershipClamp    *prometheus.CounterVec
	layerEntrySkipped prometheus.Counter
	// egressSourceErrors: counter of per-instance sysfs read
	// failures from cmd/vmmd/network_poller.go (ADR-046, step
	// 7). The loop polls /sys/class/net/<vethHost>/statistics/
	// rx_bytes for every live instance on a 250 ms tick; any
	// read failure (veth gone between snapshot and read, kernel
	// counter unparseable, EACCES) increments this counter and
	// the cache baseline is preserved so the next successful
	// tick picks up where the failed one left off. A persistent
	// rate is the alert source — `vmmd_egress_source_errors > 0`
	// for 5m is a tripwire. The metric name matches the §12
	// conventions and the single-registry pattern (memory
	// wire-opsmetrics-single-registry); no per-instance label,
	// only the empty-label Counter so Prometheus cardinality
	// stays bounded. The byte data itself lives in
	// usage_minutes.net_tx_bytes (the canonical source per
	// ADR-046 §1); this counter is the failure-channel.
	egressSourceErrors prometheus.Counter
	// provenanceWrites: ADR-038 / Tier 3 / issue #197 B3.1
	// observability. Counter labelled by code ∈ {ok, error} so the
	// dashboard surfaces the populator success rate alongside the
	// builds counter (which it pairs with — every successful build
	// should land a provenance row). Unbounded cardinality risk is
	// closed by the closed `code` label set. Registered on every
	// daemon (the counter is unused except on builderd, but the
	// single-registry pattern — memory note wire/OpsMetrics —
	// demands the field be present on the shared struct).
	provenanceWrites *prometheus.CounterVec
	// deployScanDuration: issue #464 / ADR-055 — per-deploy (not
	// per-base) grype scan histogram. Measured from
	// runDeployScan entry to either the SUCCESS sink write or
	// the FAILED sink write (after the 1-retry backoff). Buckets
	// cover the 5-min SLA (AC #1) — a deploy that lands in the
	// top bucket is a SLO miss and the dashboard's "scan overdue"
	// chip will surface it (ADR-055 §3).
	deployScanDuration *prometheus.HistogramVec
	// deployStageDuration: per-deploy closed-6-stage histogram,
	// labelled by {stage, status} (ADR-117 §Production-ready
	// follow-on). One Observe per `transitionWithStage` write
	// (pkg/imaged/handler.go) — the duration is the wall-clock
	// between from and to (the row's started_at → ended_at delta
	// already on the jsonb row, surfaced via the
	// pkg/imaged.transitionWithStage seam). Buckets skew to the
	// long tail (up to 300s) so a stalled `image_build` shows up
	// as a top-bucket observation rather than getting dropped into
	// the +Inf bin. Pre-instantiated in NewOpsMetrics below for
	// every (stage, status) tuple so /metrics surfaces zero on
	// boot; only imaged increments via ObserveDeployStageDuration.
	deployStageDuration *prometheus.HistogramVec
	// deployScanTotal: scanned-deploy counter, labelled by
	// result ∈ {complete, failed, skipped}. The complete/failed
	// labels increment once per scan after the 1-retry backoff;
	// skipped comes from the pre-feature backfill (issue #464
	// migration 00135) plus a defensive increment on the per-app
	// grype toggle (imaged builds where scan is disabled by
	// feature flag). The counter is the read side for the §12
	// dashboard scan panel — sum(rate(deployScanTotal[5m])) by
	// (result) over a 1 h window.
	deployScanTotal *prometheus.CounterVec
	// deployScanVulns: per-deploy CVE counter, labelled by
	// severity ∈ {CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN}. Counts
	// each finding once per deploy scan (matching the
	// imageScanVulns semantics but on a per-deploy key). The
	// CRITICAL row is the per-deploy equivalent of the vmmd
	// admission gate's read side — increment-without-action,
	// surfaced-not-enforced (ADR-055 §2).
	deployScanVulns *prometheus.CounterVec
	// imageScanVulns: issue #299 / supply-chain scan observability.
	// Counter labelled by image (the OCI ref of the staged base
	// ext4, e.g. "ghcr.io/poyrazk/builder-base:latest" or
	// "ghcr.io/onebox-faas/runner-node22:v1.2.3") and severity ∈
	// {CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN}. Closed `severity`
	// set pre-instantiated at boot below — `image` is bounded by
	// the runtime count (~5) + a few builder-base variants.
	// The CRITICAL count is the vmmd admission gate's read side
	// (Manager.bringUpScanCheck at pkg/fcvm/manager.go refuses to
	// bring up an instance whose sidecar has CRITICAL > 0). The
	// counter is incremented once per Grype scan at imaged
	// base-stage time (pkg/imaged/base_stage.go) and re-read on
	// every cold-boot by vmmd.
	imageScanVulns *prometheus.CounterVec
	// liveMigrationDecisions: Tier A5 / ADR-066 cross-node
	// live-instance migration observability. Counter labelled by
	// outcome ∈ {migrated, conflict, no_headroom, no_eligibility,
	// lease_expired, peer_failure} — the closed set the
	// Engine.MigrateLiveInstances batch loop can land in. The
	// `migrated` label is the §12 dashboard panel (sum over 5m
	// for the rate; pairs with rebalanceDecisions{outcome=migrated}
	// to give a holistic "fleet cross-node moves / 5m" view that
	// distinguishes PARKED-app rebalance from LIVE-instance
	// migration). `lease_expired` is the tripwire for the
	// four-phase handoff timing out — a persistent rate means
	// MigrateLiveLeaseSeconds (pkg/api/limits.go) is too short for
	// the OCIRegistry backend's snapshot pull latency. `peer_failure`
	// is the tripwire for the new-owner vmmd failing the
	// AdoptMigratedInstance RPC — operationally distinct from
	// `conflict` (which is a peer claim race, not a transport
	// failure). Single-registry: registered on every daemon
	// (mirrors the rebalanceDecisions pattern); only schedd
	// increments via ObserveLiveMigration. Pre-instantiated in
	// NewOpsMetrics so the row surfaces in /metrics from boot.
	liveMigrationDecisions *prometheus.CounterVec
	// rebalanceDecisions: Tier A4 / ADR-064 cross-node app
	// rebalance observability. Counter labelled by outcome ∈
	// {migrated, conflict, no_headroom, cooldown,
	// no_eligibility} — the closed set the rebalancer's batch
	// loop can land in. The migrated label is the §12 dashboard
	// panel (renamed per outcome, sum over 5m for the rate);
	// the others surface in the operator runbook for triage.
	// Single-registry: registered on every daemon (the field is
	// unused except on schedd, but the shared struct demands it
	// be present so /metrics doesn't show a 404 for cmd/<other>
	// scrapes that incidentally probe the prefix).
	rebalanceDecisions *prometheus.CounterVec
	// appAtCapacityTotal: Tier A9 / ADR-087 capacity-pressure
	// trigger observability. Counter labelled by (app, kind)
	// where kind ∈ {wake, admit, scaleup, floor} — the closed set
	// of engine return sites that can produce
	// WakeResult{AtCapacity: true}. The `app` label is bounded by
	// the running app set on the owning schedd (AtCapacity is a
	// hot-app signal; the per-app series churns out as the app
	// cools). The metric is the §12 dashboard panel for the
	// pressure-rebalancer trigger rate; a sustained non-zero rate
	// per app is the indicator that the fleet needs tuning
	// (consider raising capacity on the owner schedd, or
	// spreading the customer across multi-app deployments).
	// Single-registry: registered on every daemon (mirrors the
	// rebalanceDecisions pattern); only schedd increments via
	// AppAtCapacityTotal (the engine callsite stamps
	// `app=appID, kind=branch`). Pre-instantiated in
	// NewOpsMetrics below — kind labels are pre-instantiated at
	// boot, app labels populate lazily on first wake.
	appAtCapacityTotal *prometheus.CounterVec
	// pressureReassignmentsTotal: Tier A9 / ADR-087
	// pressure-rebalancer observability. Counter labelled by
	// outcome ∈ {migrated, conflict, no_headroom,
	// no_eligibility, no_peer} — the closed set
	// the pressure-rebalancer's batch loop can land in once per
	// app per sweep. `migrated` is the §12 dashboard panel
	// (sum over 5m for the rate); the `peer_live_migrated`
	// label was removed in the Tier A10 follow-up PR because the
	// helper it gated always no-op'd (see NewOpsMetrics body for
	// the rationale); Tier A10.1 (peer-to-peer migrator) will
	// re-introduce it.
	// pressure path (a non-zero rate means the policy gate
	// opened the live migration window); `no_headroom` is the
	// tripwire for sustained full-cluster pressure (call the
	// operator). `no_peer` fires when the only active compute
	// node is the owner (single-box mode or fleet-wide drain).
	// Single-registry: registered on every daemon (mirrors the
	// rebalanceDecisions / liveMigrationDecisions pattern);
	// only schedd increments via PressureReassignments. Pre-
	// instantiated at boot so the rows surface in /metrics from
	// the moment schedd starts.
	pressureReassignmentsTotal *prometheus.CounterVec
	// overflowTargetSpillHitsTotal: Tier A10 / ADR-088
	// per-app overflow_node preference observability. Counter
	// labelled by outcome ∈ {used, unavailable, fallback_used} —
	// the closed set the pressure-rebalancer's overflow-peer
	// resolution path can land in once per app per sweep.
	// `used` = the customer's preferred overflow_node had
	// headroom and the engine reassigned to it; `unavailable` =
	// the preferred node was inactive / no headroom / no longer
	// exists (the engine fell through to the A9 first-peer-with-
	// headroom path); `fallback_used` = the fallback path
	// actually landed an assignment after an `unavailable`
	// observation. The first two are the tripwires for "is the
	// preference honoured?"; the third answers "did the
	// fallback save the sweep?" so a high
	// `unavailable` + low `fallback_used` rate is the
	// sustained-full-cluster-pressure tripwire. Single-registry:
	// registered on every daemon; only schedd increments via
	// OverflowTargetSpillHits.
	overflowTargetSpillHitsTotal *prometheus.CounterVec
	// migratingReconcileDecisions: Tier A6 / ADR-067
	// migrating-instance watchdog observability. Counter labelled
	// by outcome ∈ {reinvited, hard_deleted, conflict, error} —
	// the closed set the migration-reconcile loop can land in
	// once per stuck state='migrating' row. `reinvited` is the
	// happy path (active owner re-acked the same lease); the
	// other two surface in the operator runbook as tripwires
	// (a persistent `hard_deleted` rate means the new-owner vmmd
	// keeps dying mid-handoff; a persistent `conflict` rate
	// means peer schedds are racing hard on the same row).
	// Single-registry: registered on every daemon (mirrors the
	// rebalanceDecisions / liveMigrationDecisions pattern); only
	// schedd increments via ObserveMigratingReconcile. Pre-
	// instantiated in NewOpsMetrics so the row surfaces in
	// /metrics from boot.
	migratingReconcileDecisions *prometheus.CounterVec
	// deadNodeReconcileDecisions: dead-node billing reconciler.
	// schedd's Engine.ReconcileDeadNodeInstances increments this
	// once per RUNNING row found on a node that has been
	// unreachable past the staleness window. A non-zero `failed`
	// rate means a vmmd died without transitioning its rows and
	// customers were being billed for VMs that no longer exist —
	// the §12 dashboard treats a sustained non-zero rate as an
	// incident signal, not routine background repair.
	deadNodeReconcileDecisions *prometheus.CounterVec
	// githubdPathFilterTotal: issue #432 phase 5 / ADR-050
	// §109. Counter labelled by `mode` ∈ {paths, full_fallback,
	// truncated, error, breaker_open} — the closed set
	// githubd's push dispatch can land in. Incremented once per
	// inbound webhook AFTER lookupChangedFiles returns a mode.
	// Single-registry: registered on every daemon; only githubd
	// increments via ObserveGithubdPathFilter. The panel selector
	// is the §12 rate(`githubd_path_filter_total[5m]`) grouped by
	// mode, and a non-zero `mode="breaker_open"` rate for 10m is
	// the canary for the upstream compare-API outage (the
	// circuit breaker in pkg/githubd/changedfiles.go trips after
	// breakerFailureThreshold consecutive failures, then auto-
	// resets after breakerCooldown).
	githubdPathFilterTotal *prometheus.CounterVec
	// registryCredentialMarkUsedFailures: ADR-062 / issue #461.
	// Counts every failure of imaged's
	// store.MarkAppRegistryCredentialUsed call after a successful
	// authenticated pull. The deployment itself succeeds (mark-used
	// is intentionally non-fatal per ADR-062 §Decision 8) but the
	// counter is the operator's tripwire: a persistent rate means
	// `last_used_at` is lagging reality and the rotation heuristics
	// may rotate still-in-use credentials. No labels — closed set
	// (the failure is just "DB write refused" / "row vanished" /
	// "transient connection drop"), bounded cardinality. Pre-
	// instantiated at boot below so the row surfaces in /metrics
	// from the moment imaged starts.
	registryCredentialMarkUsedFailures prometheus.Counter
	// storageCacheStaleFallback: ADR-054 acceptance / multi-box
	// cache policy. Counts the times a LocalCacheBackend served a
	// last-known-good cached blob because the parent backend failed
	// AND FAAS_STORAGE_CACHE_SERVE_STALE=true (opt-in). No labels —
	// the policy is deployment-level; per-key labels would explode
	// cardinality for no operator value. A non-zero rate signals
	// "registry is down" — alertable via the §12 storage panel.
	// Pre-instantiated at boot below so the row surfaces in /metrics
	// from the moment the daemon starts. Only backends with the
	// read-through cache wrapped (oci mode by default since the
	// ADR-054 acceptance) emit on this counter.
	storageCacheStaleFallback prometheus.Counter
	// wakePhaseEmitted: issue #517 / PR-C / ADR-064. Per-(phase,
	// result) counter for pkg/events.Platform.Emit. phase is the
	// substring after `wake.` (e.g. "boot_started",
	// "readiness_200", "proxy_first_byte"); result ∈ {ok, failed}.
	// Closed phase set is pre-instantiated at boot (mirror of
	// stripePushDur) so the §12 wake-latency panel surfaces zero
	// on an idle daemon and the histogram has a series to bucket
	// into the moment the first wake fires. Single-registry:
	// registered on every daemon so the struct stays a single
	// registry — only schedd / vmmd / gatewayd-internal / builderd / apid
	// increment via Platform.Emit in production; other daemons
	// sit at zero. Closed set is the 15 phases from
	// pkg/events/wake.go (extended by ADR-098 C11 to surface
	// the three vmmd-side phase-decomposed wake timings).
	wakePhaseEmitted *prometheus.CounterVec
	// wakePhaseDur: lifecycle histogram for wake phases. Same
	// (phase, result) tuple as wakePhaseEmitted. Buckets sized
	// for the wake envelope: queue→admit <100ms; boot <30s;
	// readiness <60s; proxy <5s. The 60s tail catches
	// pathological stalls so the §12 panel can page on a
	// per-phase p99.
	wakePhaseDur *prometheus.HistogramVec
	// esmPollsTotal (issue #757 / ADR-118 commit 9): per-broker
	// poll outcomes, labelled by source ∈ {kafka, nats,
	// redis_streams, sqs_compat, queue, cron} and outcome ∈
	// {success, empty, error}. Closed set pre-instantiated in
	// NewOpsMetrics so the rows surface in /metrics from boot.
	// Single-registry: registered on every daemon; only schedd
	// increments via ObserveESMPoll.
	esmPollsTotal *prometheus.CounterVec
	// esmRecordsConsumedTotal (issue #757 / ADR-118 commit 9):
	// per-source record counter incremented after each
	// closeBatch — the rate is the §12 broker throughput panel.
	// source ∈ {kafka, nats, redis_streams, sqs_compat, queue, cron}.
	esmRecordsConsumedTotal *prometheus.CounterVec
	// esmLagSeconds (issue #757 / ADR-118 commit 9):
	// per-(source, shard) lag histogram. shard is bounded by the
	// 32-bucket cap with `_agg` overflow documented in ADR-118
	// §"Cardinality discipline" — the shard key space is the
	// topic×partition or stream×shard tuple, capped to keep
	// the Prometheus series count flat. Closed set pre-instantiated
	// at boot so the panel surfaces zero from process start.
	esmLagSeconds *prometheus.HistogramVec
	// auditLogWriteTotal (PR-#TBD / C5): per-(endpoint, kind)
	// counter incremented on every successful events-table
	// append at pkg/audit.Auditor.Emit. Splits the legacy
	// AuditWriteFailures counter along the success/failure
	// axis so /v1/admin/obs/health can report write throughput
	// directly. Labels: endpoint ∈ {apid, schedd, meterd,
	// gatewayd-internal}; kind is the closed operator-action
	// kind vocabulary (operator.action.<verb> /
	// operator.action.<verb>.outcome for verb ∈
	// {force_park, force_cold_boot, force_restart}) plus
	// "other" overflow. Pre-instantiated in NewOpsMetrics.
	auditLogWriteTotal *prometheus.CounterVec
	// auditLogWriteFailuresTotal (PR-#TBD / C5): same label set
	// as auditLogWriteTotal plus error_class ∈
	// {sqlstate_23514, sqlstate_23505, timeout, other}. Same
	// pre-instantiation grid.
	auditLogWriteFailuresTotal *prometheus.CounterVec
	// operatorActionTraceCompletenessRatio (PR-#TBD / C5):
	// per-kind gauge the schedd 60s tick sets to the 5-minute
	// ratio of operator.action.<verb>* audit rows whose
	// trace_id column is non-NULL. Closed kind set matches
	// auditLogWriteTotal. Lets the dashboard tile "trace_id
	// coverage per kind" alert before a 5% drop becomes a
	// stuck page.
	operatorActionTraceCompletenessRatio *prometheus.GaugeVec
	// operatorActionTraceCompletenessFirstTickCompleted is incremented once
	// after schedd completes its first successful completeness query. It
	// separates "no observation yet" from a real all-zero result.
	operatorActionTraceCompletenessFirstTickCompleted prometheus.Counter
	// operatorActionTraceCompletenessLastSuccessTimestamp records the last
	// successful schedd completeness query, independent of scrape time.
	operatorActionTraceCompletenessLastSuccessTimestamp prometheus.Gauge
	operatorActionTraceCompletenessFirstTickOnce        sync.Once
}

// NewOpsMetrics builds an OpsMetrics keyed on the per-daemon prefix — e.g.
// "vmmd" produces vmmd_ops_total{op,code} and vmmd_op_duration_seconds{op}.
// The returned registry is what serves the /metrics endpoint.
func NewOpsMetrics(prefix string) *OpsMetrics {
	reg := prometheus.NewRegistry()
	ops := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_ops_total",
		Help: "Count of operations, labelled by op name and terminal status code.",
	}, []string{"op", "code"})
	dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_op_duration_seconds",
		Help: "Operation latency in seconds, labelled by op name.",
		// Sub-millisecond control plane operations are common (the wake
		// path is queue-bound < 1 ms to hand the request off). Buckets
		// skewed toward [1ms..1s]; the long tail catches pathological
		// Firecracker stalls for alerting.
		Buckets: []float64{
			0.0005, 0.001, 0.0025, 0.005, 0.01,
			0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0,
		},
	}, []string{"op"})
	watchdogKills := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_watchdog_kills_total",
		Help: "Count of instances the §6.1 watchdog transitioned out of a stuck state, labelled by from→to state.",
	}, []string{"from_state", "to_state"})
	// warmSnapshotErrors (issue #470 / PR A / ADR-055). Counts
	// warm-tier snapshot failures vmm.WarmSnapshot raised. The fleet
	// is healthy if rate() stays under the warm-capture-error alert
	// (`monitoring/alerts/warm-snapshot.yml`); a sustained rate
	// implies imaged's snapshot_written subscriber is the next
	// place to look (no row → "snapshot_written" dropped).
	warmSnapshotErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_warm_snapshot_errors_total",
		Help: "Count of warm-tier snapshot failures (issue #470 / PR A / ADR-055), labelled by reason ∈ {vmm_call, store_write}. The vmm_call bucket is the dominant cause — disk-full, container-induced pause, or vmmd abort during /snapshot/create. The store_write bucket fires when CreateSnapshot rejected the staging row (schema mismatch, unique violation non-tier). The OpsMetrics.WarmSnapshotErrors() helper returns the per-reason counter.",
	}, []string{"reason"})
	warmSnapshotErrors.WithLabelValues("vmm_call")
	warmSnapshotErrors.WithLabelValues("store_write")
	// warmupErrors (Tier A8 / ADR-083). Counts probe failures on the
	// standby warm-up scraper (cmd/gatewayd-public/standby_warmup.go).
	// Labelled by app slug — the per-app cardinality is bounded by the
	// operator-managed FAAS_STANDBY_WARMUP_SLUGS_PATH list (default
	// ≤ 100 slugs / fleet). The PromQL
	// `rate(gatewayd_public_warmup_errors_total[5m]) > 0` panel is
	// the §12 standby-warmup alert's primary signal: a sustained
	// non-zero rate means gatewayd-internal is unhealthy on a
	// standby box (the new leader will cold-boot every request on
	// flip). OpsMetrics.WarmupErrors(slug) returns the per-slug
	// counter; nil-safe.
	warmupErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_warmup_errors_total",
		Help: "Count of standby warm-up probe failures (Tier A8 / ADR-083), labelled by app slug. Probe failures imply gatewayd-internal is unreachable or unhealthy on the standby box — the new leader will cold-boot every request on the active-passive flip. Sustained non-zero rate triggers the §12 standby-warmup alert.",
	}, []string{"slug"})
	// Pre-instantiate the empty-slug overflow row so the help/TYPE
	// surfaces in /metrics from boot, matching the precedent in
	// warmSnapshotErrors above and the (other, other) overflow rows
	// elsewhere in this constructor.
	warmupErrors.WithLabelValues("")
	// writeRedirectTotal (Tier A9 / ADR-084). The two label sets
	// are closed (no per-request-derived values); pre-instantiation
	// at every (outcome, auth_kind) pair keeps the /metrics body
	// honest from boot (the §12 panel queries a non-zero baseline
	// from t=0; a counter that only appears after the first
	// incident confuses the alert rule). The PromQL
	// `rate(gatewayd_internal_write_redirect_total{outcome="relayed"}[5m])`
	// is the cross-box hop health signal;
	// `{outcome="loop_prevented"}` is the redirect-storm DoS
	// alarm; `{outcome="leader_unreachable"}` alerts via the
	// existing §12 failover panel (per ADR-083 §Open follow-up
	// #2, this is the canary that closes ADR-083).
	writeRedirectTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_write_redirect_total",
		Help: "Count of write requests the cmd/gatewayd-internal writeGate classified (Tier A9 / ADR-084). outcome ∈ {relayed, redirect_307, same_box, cookie_blocked, leader_unreachable, loop_prevented, mTLS_failure, error}; auth_kind ∈ {bearer, cookie, anonymous}. The closed label sets keep TSDB cardinality bounded — see pkg/gateway/writegate for the classification rules.",
	}, []string{"outcome", "auth_kind"})
	writeRedirectOutcomes := writegate.AllWriteOutcomes
	writeRedirectAuthKinds := writegate.AllAuthKinds
	for _, outcome := range writeRedirectOutcomes {
		for _, kind := range writeRedirectAuthKinds {
			writeRedirectTotal.WithLabelValues(string(outcome), string(kind))
		}
	}
	// writeRedirectLatency (Tier A9 / ADR-084). Buckets sized for
	// the cross-box mTLS hop (overlay round-trip + leader apid
	// handler). The top bucket is generous — a degraded overlay
	// can stretch a hop to seconds before the
	// StandbyWriteRedirectTimeoutMS=5000 fires and the writeGate
	// degrades to 307.
	writeRedirectLatency := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: prefix + "_write_redirect_latency_seconds",
		Help: "Cross-box apid relay duration (Tier A9 / ADR-084). Only cross-box hops emit a sample; same-box and 307-fallback paths do not. The histogram's nil branch means a pure-local daemon never bumps a series.",
		Buckets: []float64{
			0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
		},
	})
	// Issue #554 / ADR-078: liveness restarts counter. Labelled by
	// (app, deployment) — the bounded per-deployment set keeps the
	// TSDB cardinality safe (Scale: ≤ 20 deployment per app; Hobby:
	// ≤ 5; Free: 0). The dashboard panel "liveness: restarts by
	// deployment (5m)" queries this; the liveness_exhausted park
	// alert (instances.parked_liveness_exhausted audit kind) is
	// the operator-facing signal. The (other, other) overflow row
	// is pre-instantiated so the dashboard panel selector
	// {deployment!="other"} never sees "no data" from boot.
	livenessRestarts := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_liveness_restarts_total",
		Help: "Count of liveness-driven destroy+cold-boot cycles (issue #554 / ADR-078), labelled by (app, deployment). The dashboard panel 'liveness: restarts by deployment (5m)' queries this; the liveness_exhausted park alert (instances.parked_liveness_exhausted audit kind) is the operator-facing signal. Per-deployment cardinality is bounded by the plan's deployed_apps cap (Hobby: 5, Pro: 25, Scale: 100 apps × ~2 deployments/app).",
	}, []string{"app", "deployment"})
	livenessRestarts.WithLabelValues("other", "other")
	// Cluster C / ADR-121: workload OOM-kills. Mirrors the
	// livenessRestarts shape; (other, other) sentinel pre-instantiated
	// so the dashboard panel will see a zero row from boot.
	workloadOOMKills := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_workload_oom_kills_total",
		Help: "Count of workload OOM-kill detections on the customer's per-VM cgroup v2 leaf (Cluster C / ADR-121), labelled by (app, deployment). The producer chain is guest-init cgroup.events listener → vsock DGRAM type 0x05 → Manager.ReportWorkloadOOM → Engine.DestroyForWorkloadOOMFailure. The dashboard panel pairs this with liveness_restarts_total to distinguish 'healthz is bad' (liveness) from 'RAM cap is too low' (workload OOM). Per-deployment cardinality is bounded by the plan's deployed_apps cap (same as livenessRestarts).",
	}, []string{"app", "deployment"})
	workloadOOMKills.WithLabelValues("other", "other")
	// Issue #573 / ADR-128: per-(daemon, version) restart counter.
	// Closed daemon set mirrors the cmd/ tree (Tier A7 split kept
	// gatewayd-public and gatewayd-internal as distinct units per
	// ADR-070). The version label is the current wire.Version —
	// the pre-instantiation runs once per NewOpsMetrics call so
	// each daemon exposes its own (daemon, thisVersion) row from
	// boot, plus an "other" overflow row for any daemon name we
	// might add later (defensive — operator shouldn't see this in
	// the closed set, but the pre-instantiation guarantees the
	// label set renders even before the first RecordDaemonRestart
	// call).
	daemonRestartCount := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_daemon_restart_count",
		Help: "Count of systemd-driven restarts of THIS daemon process (issue #573 / ADR-128), labelled by (daemon, version). Producer is wire.Daemon() reading $SYSTEMD_RESTARTS_ON_FAILURE at boot; alert rules prefer node_exporter's node_systemd_restart_count{name=~'faas-.*\\.service'} when the systemd collector is enabled (commit 6 of the cluster B mega-PR added --collector.systemd to the node_exporter unit). This counter is the backstop for environments where the systemd collector is disabled. Closed daemon set: apid, gatewayd-public, gatewayd-internal, schedd, vmmd, imaged, meterd, builderd, githubd, gregale.",
	}, []string{"daemon", "version"})
	for _, daemon := range []string{"apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged", "meterd", "builderd", "githubd", "gregale", "other"} {
		daemonRestartCount.WithLabelValues(daemon, Version)
	}
	// Issue #586 / ADR-129: per-daemon build info + uptime + ready.
	// Closed daemon set mirrors daemonRestartCount above (10 closed
	// + "other" overflow = 11). Pre-instantiated with the
	// current wire.Version, GitSHA, BuildTime so /metrics surfaces
	// the daemon identity from boot — the operator dashboard
	// "Daemon versions fleet-wide" panel renders a non-empty
	// row immediately after the daemon starts, not after the
	// first SetDaemonBuildInfo call. Uptime starts at 0; the
	// goroutine spawned by wire.Daemon() updates it every 1s.
	// Ready starts at 0 and is driven by the daemon's /readyz probe.
	daemonBuildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_daemon_build_info",
		Help: "Always-1 gauge that exposes the running binary's identity (issue #586 / ADR-129), labelled by (daemon, version, git_sha, build_time). The labels carry the signal — the gauge value is meaningless. Operator dashboards query this metric for the 'Daemon versions fleet-wide' heatmap panel. The closed daemon set (apid, gatewayd-public, gatewayd-internal, schedd, vmmd, imaged, meterd, builderd, githubd, gregale) is pre-instantiated at boot so /metrics surfaces the identity from process start.",
	}, []string{"daemon", "version", "git_sha", "build_time"})
	daemonUptimeSeconds := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_daemon_uptime_seconds",
		Help: "Process uptime in seconds (issue #586 / ADR-129), labelled by daemon. Updated every 1s by the wire.Daemon() boot goroutine. Operator dashboards query this for the 'Daemon uptime (1h)' timeseries panel. The closed daemon set is pre-instantiated at 0 so /metrics surfaces zero rows from idle.",
	}, []string{"daemon"})
	daemonReady := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_daemon_ready",
		Help: "Readiness gauge (issue #586 / ADR-129 / issue #571 PR-A2), labelled by daemon. 0 = initializing / draining / not yet serving traffic; 1 = ready. The daemon's ReadyzProbe observer mirrors the same aggregate state returned by /readyz; see pkg/wire/readiness.go. Operator dashboards query this for the 'Fleet readiness' panel.",
	}, []string{"daemon"})
	for _, daemon := range []string{"apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged", "meterd", "builderd", "githubd", "gregale", "other"} {
		daemonBuildInfo.WithLabelValues(daemon, Version, GitSHA, BuildTime).Set(1)
		daemonUptimeSeconds.WithLabelValues(daemon).Set(0)
		daemonReady.WithLabelValues(daemon).Set(0)
	}
	// faasDeployVersion (issue #586 / ADR-129 / cluster C commit 11)
	// is the platform-wide release identifier. Single label, `version`,
	// so cardinality is bounded by the number of releases currently
	// emitting metrics (one row per distinct version seen, well under
	// the Prometheus tens-of-thousands guideline). Pre-instantiated at
	// boot from wire.Version so /metrics surfaces the current release
	// from process start without any SetDeployVersion call; SetDeployVersion
	// re-stamps on version change (rolling deploy, hot-reload).
	faasDeployVersion := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_faas_deploy_version",
		Help: "Always-1 gauge that exposes the platform's current release identifier (issue #586 / ADR-129), labelled by version. One row per distinct version seen across the fleet — the gauge value is meaningless; the label carries the signal. Pre-instantiated at boot from wire.Version. Operator dashboards query this metric for the 'Releases fleet-wide' stat panel and to detect partial rollouts (a cluster with 2 versions visible is mid-rollout).",
	}, []string{"version"})
	faasDeployVersion.WithLabelValues(Version).Set(1)
	// ADR-127 §D3 (Layer 7) — bridgeFramingTotal. Three closed
	// label sets; pre-instantiate the full cross-product so the
	// bridge-protection dashboard panel renders a zero row from
	// boot (the §12 panel-at-day-1 contract).
	//
	// app_protocol    ∈ {http1, http2, grpc}     (pkg/api/app_protocol.go)
	// bridge_protocol ∈ {h1, h2c}
	// framing         ∈ {match, mismatch}        (the alert source)
	bridgeFramingTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_bridge_framing_total",
		Help: "Count of inbound bridge requests by (app_protocol, bridge_protocol, framing), where framing=mismatch means the operator's FAAS_BRIDGE_PROTOCOL env value does not match the app's app_protocol setting (the surgical-rollback signal per docs/ops/h2c-rollback.md). All three label sets are closed; the 12-series cross-product is pre-instantiated at boot so the bridge-protection Grafana dashboard (deploy/grafana/bridge-protection.json, ADR-127 §D3) renders from boot. Producer: vmmd-side increment on receipt of the X-Faas-Bridge-Framing response header from the bridge in pkg/vmmdgrpc/forward.go::forwardHTTPStreamV2 (the bridge is a separate process that doesn't import pkg/wire, so the counter is incremented on the vmmd side via response-header roundtrip; cmd/vmmd-stream-bridge::newHandler writes the header, vmmd extracts it via httpResp.Header.Get). The metric is exported by vmmd's /metrics handler.",
	}, []string{"app_protocol", "bridge_protocol", "framing"})
	for _, ap := range api.AppProtocolClosedSet {
		for _, bp := range []string{"h1", "h2c"} {
			for _, fr := range []string{"match", "mismatch"} {
				bridgeFramingTotal.WithLabelValues(ap, bp, fr)
			}
		}
	}
	// Issue #470 / PR C / ADR-074: guest-init duration histogram.
	// Buckets are spec §6.3 verbatim — see OpsMetrics field doc above.
	guestInitDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_guest_init_duration_seconds",
		Help:    "Wall-clock seconds between the vmmd DGRAM recv of the framework-ready signal and the Manager.MarkInstanceFrameworkReady return (issue #470 / PR C / ADR-074). Labelled by {app, runner} — the empty-tuple sentinel is pre-instantiated so dashboards render from boot. Bucket set is spec §6.3 verbatim.",
		Buckets: []float64{0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1, 1.5, 3, 5},
	}, []string{"app", "runner"})
	guestInitDuration.WithLabelValues("", "")
	// ADR-097 (P1B): schedd-side wake RPC duration histogram. Phase
	// ∈ {admit_to_rpc, rpc_call, rpc_to_running}. Bucket set is spec
	// §6.3 verbatim plus a 0.01 low-end bucket for admit_to_rpc.
	// Empty-app sentinel rows are pre-instantiated for every phase
	// value so the wake-latency decomposition dashboard surfaces a
	// zero row from boot (the §12 panel-at-day-1 contract that bit
	// PR #826). Engine.Wake attaches wake_id as a prometheus.Exemplar
	// on each observation; the observer returns a prometheus.Observer
	// that callers Observe(d) on — the wire.OpsMetrics.WakeRPCDuration
	// accessor below mirrors GuestInitDuration's nil-safe shape.
	//
	// Note: name is *_wake_rpc_duration_seconds, not the existing
	// *_wake_phase_duration_seconds (events platform, ADR-064 at
	// metrics.go:1951 — {phase, result} labels, covers the full
	// wake envelope). The two are correlated by wake_id exemplar
	// but cover different windows; renaming the existing one would
	// break the wake-latency panel that ships to day-1.
	wakeRPCDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_wake_rpc_duration_seconds",
		Help:    "Wall-clock seconds for each schedd-side wake RPC phase (ADR-097). phase ∈ {admit_to_rpc, rpc_call, rpc_to_running}; app label is apps.id. admit_to_rpc covers the gRPC handler → vmmd RPC start window (lock + admitGate + ledger + placement). rpc_call covers the vmmd Create{FromSnapshot,ColdBoot} round trip. rpc_to_running covers the RPC return → WAKING/COLD_BOOTING → RUNNING transition. wake_id is attached as a prometheus.Exemplar on each observation so operators can join to gateway_wake_latency_seconds and to the events table. Bucket set reuses spec §6.3 verbatim with a 0.01 low-end bucket for admit_to_rpc.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.2, 0.35, 0.5, 0.8, 1, 1.5, 3, 5},
	}, []string{"app", "phase"})
	wakeRPCDuration.WithLabelValues("", "admit_to_rpc")
	wakeRPCDuration.WithLabelValues("", "rpc_call")
	wakeRPCDuration.WithLabelValues("", "rpc_to_running")
	// Issue #470 / PR C / ADR-074: wake tier mix counter.
	wakeSnapshotTier := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_wake_snapshot_tier_total",
		Help: "Count of wakes that picked a given snapshot tier (issue #470 / PR C / ADR-074), labelled by tier ∈ {warm, init, cold_boot_fallback}. Pre-instantiated at boot so the wake-tier-mix Grafana panel has zero rows from idle fleet, non-zero as soon as production wakes happen.",
	}, []string{"tier"})
	wakeSnapshotTier.WithLabelValues("warm")
	wakeSnapshotTier.WithLabelValues("init")
	wakeSnapshotTier.WithLabelValues("cold_boot_fallback")
	// wakeFailure (issue #1059 / ADR-127) — see OpsMetrics.wakeFailure
	// field doc comment for the closed reason vocabulary. The box
	// label is resolved through boxLabel() at the call site (the
	// accessor returns the admission-set value); the constructor
	// here pre-instantiates every (box, reason) pair in the closed
	// cartesian — same precedent as warmSnapshotErrors above and
	// writeRedirectTotal's outcome × auth_kind cross product.
	wakeFailureReasons := []string{
		// vmmd-side closed vocab (issue #1059 / ADR-127). The
		// classifier at pkg/fcvm/wake_classify.go maps every
		// vmmd wake-failure hook site to exactly one of these
		// eight reasons.
		"snapshot_stale",
		"disk_full",
		"jailer_fail",
		"netns_fail",
		"cgroup_fail",
		"vsock_fail",
		"snapshot_restore_err",
		"mem_backend_err",
		// schedd-side audit-reason strings (issue #1059 / ADR-127
		// §3.6 — schedd parity, cluster A commit 3 of the
		// platform-observability mega-PR). The schedd's Engine
		// emits `vmm_boot_failed` from the vmm.Create{Restore,ColdBoot}
		// RPC error branch and `record_runtime_failed` from the
		// post-boot SetInstanceRuntime DB-write branch. Both
		// literals come straight off the events.BootFailed.Reason
		// field at pkg/sched/engine.go:2123 / :2194 — they are the
		// existing audit-reason strings, the metric just gains a
		// counter surface. schedd emits on its own
		// schedd_wake_failure_total registry (single-registry per
		// daemon); the cross-daemon reason union is intentional so
		// dashboards can use a single legend across the fleet.
		"vmm_boot_failed",
		"record_runtime_failed",
	}
	wakeFailureBoxes := []string{labelLocal, otherBoxLabel}
	// wakeFailureApps: the per-app pre-instantiation set (issue
	// #1059 / ADR-127 §3.5). We pre-instantiate the RESERVED
	// app labels (labelAppUnknown == "", otherAppLabel ==
	// "__other__") so /metrics surfaces zero rows from an idle
	// fleet, and rely on the appLabelSet admission (max
	// maxAppLabelValues = 256) to admit real app slugs as wake
	// failures land. The appLabel() accessor collapses overflow
	// to otherAppLabel so the Prometheus TSDB series set stays
	// bounded over the daemon's lifetime.
	wakeFailureApps := []string{labelAppUnknown, otherAppLabel}
	wakeFailure := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_wake_failure_total",
		Help: "Count of wake failures (issue #1059 / ADR-127), labelled by box (admission-bounded, overflow collapses to __other__), app (admission-bounded, overflow collapses to __other__), and reason ∈ {snapshot_stale, disk_full, jailer_fail, netns_fail, cgroup_fail, vsock_fail, snapshot_restore_err, mem_backend_err, vmm_boot_failed, record_runtime_failed}. The first 8 reasons are the vmmd-side closed vocabulary enforced by pkg/fcvm/wake_classify.go — every vmmd wake-failure hook site maps to exactly one of these. The last 2 reasons (vmm_boot_failed, record_runtime_failed) are schedd-side audit-reason strings emitted from the schedd's Engine wake-error branches at pkg/sched/engine.go:2123 / :2194 — cluster A commit 3 of the platform-observability mega-PR added schedd parity (ADR-127 §3.6). vmmd emits only the first 8; schedd emits only the last 2; the union is pre-instantiated in the constructor so /metrics surfaces zero rows from idle fleet regardless of which daemon hosts the registry. The box label is bounded by maxBoxLabelValues (64) and resolves to \"local\" until the Tier A multi-host rollout lands (ADR-062 / ADR-066 chain). The app label is bounded by maxAppLabelValues (256) and resolves to the call site's app identifier — empty input collapses to labelAppUnknown (\"\") to distinguish missing-app-slug calls from real app slugs that hit the admission cap (which collapse to otherAppLabel).",
	}, []string{"box", "app", "reason"})
	for _, box := range wakeFailureBoxes {
		for _, app := range wakeFailureApps {
			for _, reason := range wakeFailureReasons {
				wakeFailure.WithLabelValues(box, app, reason)
			}
		}
	}
	// wakeLatency (issue #1059 / ADR-127) — see OpsMetrics.wakeLatency
	// field doc comment for the closed phase set. Bucket set reuses
	// the existing vmmd_wake_phase_duration_seconds{phase} envelope
	// (spec §6.3 verbatim, ADR-074 §3.5) so the §12 dashboard panel
	// can swap fleet → per-box without changing the bucketing.
	wakeLatencyPhases := []string{"restore_ms", "netns_tap_ms", "guest_ready_ms"}
	wakeLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_wake_latency_seconds",
		Help:    "Wall-clock seconds for each per-box vmmd-side wake phase (issue #1059 / ADR-127). phase ∈ {restore_ms, netns_tap_ms, guest_ready_ms}; box label is admission-bounded (overflow → __other__). Per-box sibling of the fleet <prefix>_wake_phase_duration_seconds{phase} (pkg/fcvm/metrics.go) — same bucket set, same phase vocabulary. wake_id is attached as a prometheus.Exemplar on each observation. Bucket set is spec §6.3 verbatim with the 0.3/0.35 pair (ADR-074 §3.5).",
		Buckets: []float64{0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1, 1.5, 3, 5, 10},
	}, []string{"box", "phase"})
	for _, box := range wakeFailureBoxes {
		for _, phase := range wakeLatencyPhases {
			wakeLatency.WithLabelValues(box, phase)
		}
	}
	// Issue #667 / ADR-078: waitUntil tail histograms + counters.
	// Pre-instantiated at boot so the §12 tail-watchdog panel has
	// zero rows from idle fleet and non-zero as soon as production
	// tails fire. The hostname / runtime / outcome label sets are
	// closed (sourced from api.Plans + ADR-052 + the 0x04 envelope).
	// Buckets rationale is in the OpsMetrics field doc comment.
	guestTailSeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_guest_tail_seconds",
		Help: "Wall-clock seconds a waitUntil(promise) task ran from registration to terminal (issue #667 / ADR-078). Labelled by {plan, runtime, outcome} — plan ∈ api.Plans, runtime ∈ {node22, node24, python312, python313, go124}, outcome ∈ {completed, failed, timeout}. 60 series total. Buckets sized for the Free→Scale TailTimeoutS matrix (5…60s) plus a 180s bucket for a runaway tail and a 600s bucket matching buildDur's ceiling.",
		Buckets: []float64{
			0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 180, 600,
		},
	}, []string{"plan", "runtime", "outcome"})
	guestTailFailedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_guest_tail_failed_total",
		Help: "Count of waitUntil(promise) tasks that reached a non-clean terminal (issue #667 / ADR-078). Labelled by {plan, reason} — plan ∈ api.Plans, reason ∈ {timeout, handler_error, forced_at_park, unknown}. 16 series total.",
	}, []string{"plan", "reason"})
	// ADR-124 follow-up #2: gate-rescued-by-exclude counter. Fires
	// when a scan project's pre-exclude gate was blocked AND the
	// --exclude filter flipped canApply true (server invariant:
	// gateRescuedByExclude := !preCanApply && canApply in
	// cmd/apid/scan_service.go:864). The reason label is a
	// closed-set vocabulary derived from the templated reason
	// strings produceQuotaGate emits — see
	// cmd/apid/scan_service.go::gateRescueReason for the
	// canonical bucketing. Pre-instantiate 4 plans × 3 reasons =
	// 12 series so the §12 gate-rescue panel renders zero-row
	// from a fresh boot and bumps as soon as a real rescue fires.
	// The "unknown" bucket is the catch-all for future reason
	// strings and is pre-instantiated at "apps_over_limit" shape
	// (the most common case) so it surfaces in dashboards.
	planGateRescuedByExclude := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_plan_gate_rescued_by_exclude_total",
		Help: "Count of plans where --exclude flipped a blocked pre-exclude gate to allowed (ADR-124 follow-up #2). Labelled by {plan, reason} — plan ∈ api.Plans (4), reason ∈ {apps_over_limit, crons_over_limit, crons_not_allowed}. 12 series total. The §12 gate-rescue panel renders this; a sustained non-zero rate means customers routinely run into per-plan limits and --exclude is the workaround path.",
	}, []string{"plan", "reason"})
	for _, plan := range api.Plans {
		for _, reason := range []string{"apps_over_limit", "crons_over_limit", "crons_not_allowed"} {
			planGateRescuedByExclude.WithLabelValues(string(plan), reason)
		}
	}
	tailCapReached := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_tail_cap_reached_total",
		Help: "Count of times a customer tried to register the (ConcurrentTailsPerInstance + 1)-th in-flight waitUntil task (issue #667 / ADR-078). Labelled by {plan} only — the per-instance cap is the plan-matrix axis. 4 series total.",
	}, []string{"plan"})
	// Issue #475: per-tier eviction counter. Pre-instantiate the
	// closed set of {priority, reason} tuples so the §12
	// eviction-by-tier panel has zero rows from idle fleet and
	// non-zero rows as soon as the reaper parks any instance. The
	// closed label set keeps the TSDB series bounded; the loop
	// stamps priority from the InstanceInfo carrier and reason
	// from the parking branch (ReapIdle / ReapAggressive /
	// SelectEvictions).
	evictedPriority := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_evicted_priority_total",
		Help: "Count of instances the reaper parked, labelled by per-app tier (priority ∈ {best_effort, reserved}) and the reaper branch that parked them (reason ∈ {idle, eviction_aggressive, eviction_ram}). The idle bucket is the per-tier idle-park rate; the eviction_ram bucket is the cross-account RAM-pressure signal — best_effort≫reserved is the success criterion for issue #475.",
	}, []string{"priority", "reason"})
	evictedPriority.WithLabelValues("best_effort", "idle")
	evictedPriority.WithLabelValues("best_effort", "eviction_aggressive")
	evictedPriority.WithLabelValues("best_effort", "eviction_ram")
	evictedPriority.WithLabelValues("reserved", "idle")
	evictedPriority.WithLabelValues("reserved", "eviction_aggressive")
	evictedPriority.WithLabelValues("reserved", "eviction_ram")
	rebalanceDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_rebalance_decisions_total",
		Help: "Count of per-app decisions the Tier A4 cross-node rebalancer made on a drain event (ADR-064), labelled by outcome ∈ {migrated, conflict, no_headroom, cooldown, no_eligibility}. The migrated counter is the §12 rebalance-rate panel.",
	}, []string{"outcome"})
	// ADR-087 / Tier A9: per-(app, kind) AtCapacity trigger counter.
	// Labelled by (app, kind); kind is pre-instantiated at boot so the
	// kind=* rows surface in /metrics from the moment schedd starts
	// (matching the precedent at the requestTotal / requestFailures
	// counter initialiser pattern). The app label is populated lazily
	// on first wake — the per-app series churns out as the app cools,
	// keeping the active series count bounded by the per-schedd live
	// app set. Single-registry: registered on every daemon (mirrors
	// rebalanceDecisions); only schedd increments via
	// AppAtCapacityTotal.
	appAtCapacityTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_app_at_capacity_total",
		Help: "Count of WakeResult{AtCapacity: true} returns the engine produced, labelled by app id and the engine branch that returned the no-op (kind ∈ {wake, admit, scaleup, floor}). The per-app rate is the §12 dashboard panel for the pressure-rebalancer trigger (ADR-087); a sustained non-zero rate per app means the customer's wake load is bumping the owner's max_concurrency ceiling. Single-registry: registered on every daemon (mirrors rebalanceDecisions); only schedd increments via AppAtCapacityTotal.",
	}, []string{"app", "kind"})
	for _, kind := range []string{"wake", "admit", "scaleup", "floor"} {
		appAtCapacityTotal.WithLabelValues("__none__", kind)
	}
	// ADR-087 / Tier A9: pressure-rebalancer decision counter.
	// Labelled by outcome ∈ {migrated, conflict, no_headroom,
	// no_eligibility, no_peer}. The closed set is
	// pre-instantiated at boot so the rows surface in /metrics from
	// the moment schedd starts (matching the rebalanceDecisions /
	// liveMigrationDecisions precedent). Single-registry: registered
	// on every daemon (mirrors rebalanceDecisions); only schedd
	// increments via PressureReassignments.
	//
	// Note (ADR-087 / Tier A10 follow-up 2026-08-10): the
	// `peer_live_migrated` outcome label was removed in PR
	// #799 (Tier A10 follow-ups) because the
	// maybeMigrateLiveInstancesFor helper it gated always
	// no-op'd — it called Engine.MigrateLiveInstances with
	// `deadNodeID=e.ownerNodeID`, which the function's own
	// self-path early-return rejected (return 0, nil).
	// ADR-066's four-phase handoff only supports destination =
	// local schedd (active-passive HA, ADR-083); peer-to-peer
	// live migration on the pressure path is a Tier A10.1
	// follow-up. The migration policy knob keeps the closed set
	// {skip_live, migrate_after_1, migrate_after_2} so a future
	// PR can wire the policy to a real peer-to-peer migrator
	// without churn on the API surface.
	pressureReassignmentsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_pressure_reassignments_total",
		Help: "Cross-node capacity-pressure rebalance decisions (Tier A9 / ADR-087), labelled by outcome ∈ {migrated, conflict, no_headroom, no_eligibility, no_peer, overflow_target_unavailable}. `migrated` is the §12 dashboard panel (sum over 5m for the rate); `no_headroom` is the tripwire for sustained full-cluster pressure (call the operator); `overflow_target_unavailable` is the Tier A10 / ADR-088 tripwire when the customer's preferred spill target is full or inactive. Single-registry: registered on every daemon (mirrors rebalanceDecisions / liveMigrationDecisions); only schedd increments via PressureReassignments.",
	}, []string{"outcome"})
	for _, outcome := range []string{"migrated", "conflict", "no_headroom", "no_eligibility", "no_peer", "overflow_target_unavailable"} {
		pressureReassignmentsTotal.WithLabelValues(outcome)
	}
	// ADR-088 / Tier A10: per-app overflow_node preference
	// observability. New CounterVec (separate from
	// pressureReassignmentsTotal) so a Grafana panel can branch
	// "preference honoured?" vs "did the engine move the app at
	// all?" without reading the same series with two queries.
	// outcome ∈ {used, unavailable, fallback_used} — closed set
	// pre-instantiated at boot so the rows surface in /metrics
	// from the moment schedd starts (matching the
	// pressureReassignmentsTotal pattern above).
	overflowTargetSpillHitsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_overflow_target_spill_hits_total",
		Help: "Per-app overflow_node preference resolution outcomes (Tier A10 / ADR-088), labelled by outcome ∈ {used, unavailable, fallback_used}. `used` = the customer's preferred overflow_node had headroom and the engine reassigned to it; `unavailable` = the preferred node was inactive / no headroom / no longer exists (the engine fell through to the A9 first-peer-with-headroom path); `fallback_used` = the fallback path actually landed an assignment after an `unavailable` observation. The first two are the tripwires for 'is the preference honoured?'; the third answers 'did the fallback save the sweep?' so a high `unavailable` + low `fallback_used` rate is the sustained-full-cluster-pressure tripwire. Single-registry: registered on every daemon; only schedd increments via OverflowTargetSpillHits.",
	}, []string{"outcome"})
	for _, outcome := range []string{"used", "unavailable", "fallback_used"} {
		overflowTargetSpillHitsTotal.WithLabelValues(outcome)
	}
	eventsWriteFail := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_events_write_failures_total",
		Help: "Count of state-transitions whose events audit-log row could not be written. The transition itself succeeded; this is observation-only (the state row is the source of truth).",
	})
	auditWriteFail := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_audit_write_failures_total",
		Help: "Count of apid-side auth audit emits (IAM-4, ADR-035) whose events row could not be written, labelled by account_id. The handler has already returned 200; this is observation-only. account_id=\"__other__\" is the bounded admission overflow bucket — operators must check daemon slog for the original id (issue #278).",
	}, []string{"account_id"})
	// accountOrgMismatch (PR 3, issue #190, ADR-061). Closed label
	// set pre-instantiated at boot so /metrics surfaces zero from
	// the first scrape, matching the precedent at the requestTotal
	// / requestFailures counter initialiser pattern (kind
	// cardinality is a fixed 4). PR 4 / PR 6 emit on this counter
	// from the dual-write paths.
	accountOrgMismatch := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_account_org_mismatch_total",
		Help: "Count of writes where the account.* and personal-org mirror disagreed, labelled by kind ∈ {plan, status, provider_customer_id, stripe_subscription_item} (issue #190, ADR-061). Observation-only — writes never block on mismatch. PR 7's cutover gate is rate() == 0 for ≥1 metering cycle.",
	}, []string{"kind"})
	for _, kind := range []string{
		"plan", "status", "provider_customer_id", "stripe_subscription_item",
	} {
		accountOrgMismatch.WithLabelValues(kind)
	}
	// issue #286 — failed-login audit evidence (SOC 2 CC7.2). Counts
	// every failed login attempt on the dashboard auth surface
	// (`POST /login`, `POST /signup`, OAuth callback denial paths).
	// Labelled by source IP, bounded by ipLabelSet (maxIPLabelValues =
	// 10_000); overflow collapses to "__other__". Backs the
	// FaasFailedLoginSpike alert rule at 20/min/IP/5m. The same
	// apid-side emit path also writes an `events.kind =
	// "auth.login.failed"` row via the async-batched audit channel
	// (cmd/apid/audit.go) — the counter is the high-frequency
	// Prometheus signal, the audit row is the operator-row join.
	failedLoginTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_failed_login_total",
		Help: "Failed login attempts on the dashboard auth surface, labelled by source IP. Bounded by ipLabelSet (maxIPLabelValues=10_000); overflow collapses to ip=\"__other__\". Backs the FaasFailedLoginSpike alert (issue #286, SOC 2 CC7.2).",
	}, []string{"ip"})
	// failedLoginDropped: shed rows on the async-batched audit channel
	// (cmd/apid/audit.go::EmitFailedLogin). Unlabelled — the operator
	// only needs to know IF the flusher is the bottleneck. A non-zero
	// rate means the auth-limit bucket is shedding the attack but the
	// audit flusher is saturated; the customer-facing auth response
	// is unaffected (the channel write is non-blocking and the 401
	// returns regardless).
	failedLoginDropped := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_failed_login_audit_dropped_total",
		Help: "Failed-login audit rows dropped because the in-process buffered channel was full. Unlabelled — a non-zero rate is the canary for \"audit flusher is the bottleneck under a credential-stuffing burst\" (issue #286).",
	})
	// failedLoginAuditWriteFailures: failed-login audit writes
	// (cmd/apid/audit.go::flushOne) whose AppendEvent returned an
	// error. Distinct from audit_write_failures_total (success path
	// labelled by account_id) because the failed-login row's subject
	// is always nil — a "subject collapse" through that counter
	// would conflate this path with the success path's anonymous
	// failures and make the operator's triage harder. Unlabelled —
	// the per-IP breakage is not observable on this path (the row
	// is dropped before the per-IP Prometheus series is touched);
	// the operator needs only an aggregate signal.
	failedLoginAuditWriteFailures := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_failed_login_audit_write_failures_total",
		Help: "Failed-login audit rows whose AppendEvent could not be written (cmd/apid/audit.go::flushOne). Unlabelled — distinct from apid_audit_write_failures_total{account_id} because the failed-login row's subject is always nil, which would collapse to account_id=\"anonymous\" in the success-path counter and conflate the two surfaces (issue #286).",
	})
	// auditEventsDeletedTotal: rows pruned by the daily event-
	// retention cleanup loop (pkg/eventretention.Cleanup, ADR-075).
	// Unlabelled — the loop has one outcome (rows deleted) and the
	// per-kind breakdown lives on auditEventsVolumeTotal{kind_prefix}
	// instead. Backs the "is the prune loop keeping up?" runbook
	// question (docs/runbooks/FaasAuditRetentionExhaustion.md).
	// ADR-091 D20.3 / PR-B residual.
	auditEventsDeletedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_audit_events_deleted_total",
		Help: "Audit-event rows pruned by the daily cleanup loop (pkg/eventretention, ADR-075). Unlabelled — the loop has one outcome (rows deleted) and the per-kind breakdown lives on apid_audit_events_volume_total{kind_prefix}. ADR-091 D20.3 / PR-B residual.",
	})
	// auditEventsRetentionLagSeconds: gauge of (now − cutoff) at the
	// moment each retention pass runs. A positive value means the
	// loop is keeping up; a value pinned at 0 means the loop is
	// running but never deleting anything (operator action needed
	// if the events table is growing). Backs the same runbook as
	// auditEventsDeletedTotal. The gauge is set (not Inc) so the
	// value reflects the LATEST pass, not a cumulative sum.
	// ADR-091 D20.3 / PR-B residual.
	auditEventsRetentionLagSeconds := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_audit_events_retention_lag_seconds",
		Help: "Seconds elapsed between the most recent retention pass and the cutoff it used (pkg/eventretention, ADR-075). Zero means the loop is running but no rows are being deleted. ADR-091 D20.3 / PR-B residual.",
	})
	// domainDoctorOldestObservationSeconds (ADR-120 Tier A1):
	// fleet-wide gauge of (now − min(observed_at)) across every row
	// in domain_doctor_observations. The dns_poller (cmd/apid/
	// dns_poller.go::runDoctorOnce) emits Set(0) when the loop runs
	// but there are no rows (cold start), and Set(<age>) when rows
	// exist. Backs the FaasDomainDoctorStalled (page) and
	// FaasDomainDoctorStretched (warn) alerts at
	// deploy/ansible/roles/prometheus/files/faas.rules.yml.
	domainDoctorOldestObservationSeconds := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_domain_doctor_oldest_observation_seconds",
		Help: "Seconds elapsed since the oldest row in domain_doctor_observations was refreshed (cmd/apid/dns_poller.go::runDoctorOnce, ADR-120 Tier A1). Zero means the loop just ran against an empty table. Large values mean the poller is stalled. Backs FaasDomainDoctorStalled / FaasDomainDoctorStretched.",
	})
	// domainDoctorSkippedFlagDisabled (ADR-120 Tier A1):
	// counter of doctor passes the poller skipped because the
	// operator set FAAS_DOMAIN_DOCTOR_ENABLED=false. Unlabelled —
	// the single-registry pattern requires the field to exist on
	// every daemon's OpsMetrics, but only apid increments since
	// only apid runs the doctor loop.
	domainDoctorSkippedFlagDisabled := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_domain_doctor_skipped_flag_disabled_total",
		Help: "Doctor passes skipped because FAAS_DOMAIN_DOCTOR_ENABLED was unset/false at the dns_poller tick (cmd/apid/dns_poller.go, ADR-120 Tier A1). Unlabelled — single-registry pattern, only apid increments.",
	})
	// auditEventsVolumeTotal{kind_prefix}: counts emit calls to the
	// events table by kind prefix (auth.*, key.*, secret.*,
	// account.*, stateless.*, webhook.*, edge_rule.*, cron.*,
	// "" / unmatched). kind_prefix is the bounded-admission label
	// — overflow collapses to "__other__" via the wire admission
	// helper so Prometheus series stay bounded even if a future
	// kind namespace blows up. Backs the "is the audit write rate
	// healthy per kind?" dashboard panel. ADR-091 D20.3 / PR-B
	// residual.
	auditEventsVolumeTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_audit_events_volume_total",
		Help: "Audit-event emit calls by kind prefix (auth.*, key.*, secret.*, account.*, stateless.*, webhook.*, edge_rule.*, cron.*, other). Overflow collapses to __other__ via the bounded admission helper so Prometheus series stay bounded. ADR-091 D20.3 / PR-B residual.",
	}, []string{"kind_prefix"})
	// deploymentAuditGCRowsDeletedTotal: rows pruned by the
	// deployment_audit retention cron
	// (pkg/meter.RetentionOnceDeploymentAudit, 90-day window).
	// SAFE-RELEASES production-leveling Stream D (issue #976 /
	// ADR-122 post-merge audit). Unlabelled — the loop has one
	// outcome (rows deleted); per-kind breakdown stays on the
	// deployment_audit_at_gc_idx index. Backs the "is the
	// deployment_audit prune loop keeping up?" runbook
	// question (sibling of audit_events_deleted_total).
	deploymentAuditGCRowsDeletedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_deployment_audit_gc_rows_deleted_total",
		Help: "Deployment_audit rows pruned by the 90-day retention cron (pkg/meter.RetentionOnceDeploymentAudit, SAFE-RELEASES Stream D). Unlabelled — the loop has one outcome (rows deleted); per-kind breakdown stays on the deployment_audit_at_gc_idx index.",
	})
	// Pre-instantiate the closed kind_prefix label set so the
	// counter's HELP/TYPE and zero-valued series surface in /metrics
	// from boot (same precedent as auditWriteDur's result label set).
	// The empty string is the "unmatched prefix" bucket — kept so a
	// future emit site that bypasses the prefix matcher still has a
	// zero series rather than a missing one.
	for _, kp := range []string{
		"auth", "key", "secret", "account", "stateless",
		"webhook", "edge_rule", "cron", "other",
	} {
		auditEventsVolumeTotal.WithLabelValues(kp)
	}
	auditWriteDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_audit_write_failures_duration_seconds",
		Help: "Latency of state.Store.AppendEvent on the apid audit seam, labelled by terminal result {ok, failed}. Sized for the single-row INSERT round-trip (issue #278).",
		// Sub-millisecond for cached/healthy calls; the long tail (1s,
		// 2.5s, 5s) catches Postgres stalls so the operator can
		// distinguish a transient insert race from a database outage.
		Buckets: []float64{
			0.001, 0.005, 0.01, 0.025, 0.05,
			0.1, 0.25, 0.5, 1, 2.5, 5,
		},
	}, []string{"result"})
	// Pre-instantiate the closed result label set so the histogram's
	// HELP/TYPE and zero-valued buckets surface in /metrics from
	// boot — same precedent as stripePushDur / buildDur.
	for _, result := range []string{"ok", "failed"} {
		auditWriteDur.WithLabelValues(result)
	}
	cronFireNowDispatchDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_cron_fire_now_dispatch_duration_seconds",
		Help: "End-to-end fire-now dispatch latency (issue #791 PR-D / ADR-090 §Sub-decision 7): from the row insert at apid (requested_at) to the schedd terminal stamp. Labelled by result ∈ {succeeded, failed}. The 100ms bucket catches the fast-path notify; 5s catches the typical wake + wake-ticket; 30s/60s catch full build-VM cold-boot paths.",
		// Sized for three distinct failure modes documented in the
		// ADR: notify-wake stall (<5s), scheduler bypass / dispatcher
		// capacity rejection (5-30s), and the full-build-VM path
		// (30-60s). Buckets straddle stripePushDur's
		// {0.5,1,2,5,10,20,30,45,60} — diverging here keeps the
		// build-path noise out of the cron read shape, since
		// cron fire-now can warm a brand-new snapshot on first
		// dispatch.
		Buckets: []float64{0.1, 0.5, 1, 5, 30, 60},
	}, []string{"result"})
	// Pre-instantiate the closed result label set (mirrors
	// auditWriteDur + stripePushDur + buildDur).
	for _, result := range []string{"succeeded", "failed"} {
		cronFireNowDispatchDur.WithLabelValues(result)
	}
	requestFailures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_request_failures_total",
		Help: "HTTP requests completed with status >= 400, labelled by account_id, the route template, and code ∈ {ok, err} (issue #278, PR #336, ADR-039). code is \"err\" for every increment today (the counter only fires on status >= 400); the label is added so the failure counter mirrors requestTotal and the per-account error-rate view derives from a single source. account_id=\"anonymous\" is the unauthenticated path; account_id=\"__other__\" is the bounded admission overflow bucket. route is r.Pattern (e.g. \"GET /v1/apps/{slug}\") or \"unmatched\" for paths the mux did not dispatch.",
	}, []string{"account_id", "route", "code"})
	requestTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_request_total",
		Help: "HTTP requests completed, labelled by account_id, route, and code (issue #303, ADR-039). The counter is the per-request total — paired with requestFailures (status >= 400 only) for the per-account error-rate view. account_id flows through the same accountLabelSet as requestFailures so a customer is represented by their real id in both, or by \"__other__\" in both. code ∈ {ok, err} (ok on 2xx/3xx, err on 4xx/5xx). route is r.Pattern or \"unmatched\". Backed by the §12 traffic-anomaly recording rules (faas_apid_request_rate_5m, _3d_baseline, _ratio).",
	}, []string{"account_id", "route", "code"})
	// Issue #601 / ADR-131: CVE-vs-SBOM check + open CVE counters
	// pushed from the cve-check workflow via meterd. Closed-set
	// pre-instantiation (12 rows for cveCheckTotal; severity × 0
	// dep rows for cvesOpenTotal).
	cveCheckTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_cve_check_total",
		Help: "Per-CVE-check-run counter (issue #601 / ADR-131), labelled by result ∈ {pass, fail, new_cve} and severity ∈ {low, medium, high, critical}. Pushed by meterd from the .github/workflows/cve-check.yml artifact. Pre-instantiated at the 12-cartesian so dashboards render from boot. The Prometheus alert FaasNewCve reads result=\"new_cve\" rows to page on a fresh CVE.",
	}, []string{"result", "severity"})
	cvesOpenTotal := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_cves_open",
		Help: "Per-(severity, dep) gauge (issue #601 / ADR-131) that exposes the currently-open CVE list. Pushed by meterd from grype's filtered output. severity is the closed-set vocabulary; dep is the package name + version (single-digit thousands at worst). The §12 dashboard's 'Open CVEs by severity' stack panel reads from this metric.",
	}, []string{"severity", "dep"})
	for _, result := range []string{"pass", "fail", "new_cve"} {
		for _, sev := range []string{"low", "medium", "high", "critical"} {
			cveCheckTotal.WithLabelValues(result, sev)
		}
	}
	// Reserved label values: anonymous for unauthenticated traffic,
	// __other__ for the bounded overflow. Both are admitted at boot
	// without consuming capacity, and both are always re-admitted on
	// collision-free lookups (accountLabelSet reservedAllow).
	auditWriteFail.WithLabelValues(anonymousAccountLabel)
	auditWriteFail.WithLabelValues(otherAccountLabel)
	// issue #286: pre-instantiate the two reserved IP labels so the
	// counter's HELP/TYPE and zero-valued series surface in /metrics
	// from boot — same precedent as auditWriteFail above. The "anonymous"
	// series is the unparseable-IP fallback (pkg/middleware.defaultClientIP
	// returns "unknown" — that collapses to anonymous here so the
	// operator can distinguish a missing/garbled RemoteAddr from an
	// overflow).
	failedLoginTotal.WithLabelValues(anonymousIPLabel)
	failedLoginTotal.WithLabelValues(otherIPLabel)
	requestFailures.WithLabelValues(anonymousAccountLabel, "unmatched", "err")
	requestFailures.WithLabelValues(otherAccountLabel, "unmatched", "err")
	// Pre-instantiate the closed (account_id, route, code) tuples for
	// requestTotal so the §12 traffic-anomaly panels and alert rules
	// never see "no data" on an idle daemon. The same reserved
	// account_id / route pairs used for requestFailures, plus the two
	// closed code values.
	requestTotal.WithLabelValues(anonymousAccountLabel, "unmatched", "ok")
	requestTotal.WithLabelValues(anonymousAccountLabel, "unmatched", "err")
	requestTotal.WithLabelValues(otherAccountLabel, "unmatched", "ok")
	requestTotal.WithLabelValues(otherAccountLabel, "unmatched", "err")
	stripePushDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_stripe_push_duration_seconds",
		Help: "Per-push latency to Stripe, labelled by terminal result code (ok on success, or a stripe.ClassifyPushError label on failure).",
		// Sized for Stripe's documented SLA: p99 ≈ 5 s, p99.9 ≈ 30 s,
		// 60 s ceiling = documented API timeout.
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 45, 60},
	}, []string{"result"})
	paddlePushDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_paddle_push_duration_seconds",
		Help: "Per-push latency to Paddle, labelled by terminal result code (ok on success, or a paddle.ClassifyPushError label on failure).",
		// Sized for Paddle's catalog POSTs: price handle lookups on the
		// first call dominate (≈1–2 s); subsequent flushes are <500 ms
		// since the catalog is hot. The 60 s ceiling matches the SDK's
		// default timeout. Same bucket boundaries as stripePushDur so
		// the §12 dashboard panels align horizontally between providers
		// — the closed label set diverges, but the latency shape is
		// comparable.
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 45, 60},
	}, []string{"result"})
	polarPushDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_polar_push_duration_seconds",
		Help:    "Per-push latency to Polar event ingestion, labelled by terminal result code.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 45, 60},
	}, []string{"result"})
	buildDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_build_duration_seconds",
		Help: "Wall-clock duration of a builder-VM build, in seconds (ADR-030). Labelled by outcome {cache_hit,ok,failed} so the §12 panels can slice out cache-hit noise (<1 s); success/failure classification lives on ops_total{op=\"build\",code}.",
		// Sized for the build envelope: cache hits land in seconds, real
		// builds run up to the 10-min (600 s) BuildTimeoutSeconds cap.
		Buckets: []float64{5, 15, 30, 60, 120, 240, 360, 480, 600},
	}, []string{"outcome"})
	// Pre-instantiate every outcome label so the histogram's HELP/TYPE and
	// zero-valued buckets surface in /metrics from boot (ADR-030, same
	// precedent as the stripe-push histogram pre-instantiation above).
	for _, outcome := range []string{"cache_hit", "ok", "failed"} {
		buildDur.WithLabelValues(outcome)
	}
	buildQueueWait := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: prefix + "_build_queue_wait_seconds",
		Help: "Seconds a build waited between enqueue (apid) and dequeue (builderd start), spec §12 target < 60 s, warn > 300 s (ADR-030).",
		// Sized to the §12 alert thresholds: healthy < 60 s, page at > 300 s.
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600},
	})
	residentGBPerCustomer := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_resident_gb_per_customer",
		Help: "Monthly GB-RAM-hours divided by paying-customer count, per plan (ADR-031). Spec §12 target 0.305 (≈312 MB/customer); > 0.45 warns. Emitted by meterd once per ResidencyInterval.",
	}, []string{"plan"})
	billingCapExceededTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_billing_cap_exceeded_total",
		Help: "Count of meterd quota ticks where accounts.overage_cap_cents was met and the overage-row insert was skipped (issue #279). Per-plan label so the §12 dashboard can split cap hits across plans. A non-zero rate is informational: a cap-hit account is operating as designed (the customer hit the operator-set monthly ceiling), not a failure mode.",
	}, []string{"plan"})
	meterdFloorAppliedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_meterd_floor_applied_total",
		Help: "Count of meterd sample ticks where the per-app min_instances GB-h floor was applied and synthetic usage_minutes rows were appended (ADR-060, issue #515). Incremented once per (app, tick) when live instance count is below ScalingPolicy.MinInstances. Per-plan label so the §12 dashboard can split floor-applied apps across plans. Floor is Hobby/Pro/Scale only; PR-A's PATCH gate rejects Free.",
	}, []string{"plan"})
	// meteredMBSecondsTotal (issue #1186 / M-2 / ADR-137 §Decision 1):
	// cumulative MB-seconds fed by the sample loop's live-instance
	// branch, broken out by execution mode + plan. Dashboards query
	// `rate(metered_mb_seconds_total{mode="worker"}[5m])` to chart
	// worker idle-RAM bandwidth independently from request-mode.
	// Mirror-mode rows are dropped upstream by IsMeteredSkippableMode
	// and never reach this counter; the {mode} label set is the
	// closed billable {normal,worker,service,job} subset.
	meteredMBSecondsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_metered_mb_seconds_total",
		Help: "Cumulative MB-seconds the meterd sample loop appended to usage_minutes for live billable instances, labelled by execution_mode ∈ {normal, worker, service, job} and plan ∈ {free, hobby, pro, scale}. Mirror-mode rows are dropped upstream by IsMeteredSkippableMode and never reach this counter. The counter is cumulative MB-seconds (not row-count) so dashboards can split worker idle-RAM from request-driven RAM via rate(_total[5m]). Worker / service / job rate the same as request-mode — billing formula is unchanged (ADR-138 §Decision 1), only the label splits the dashboard view.",
	}, []string{"mode", "plan"})
	// auditOrgEvent (PR 6 / issue #190): closed 11-verb counter per
	// outcome. The increment site lives in pkg/authz/authorize.go
	// (deny paths) — apid emits one Counter per deny + one per allow;
	// schedd / meterd / gatewayd-internal do not call AuthorizeOrgAction
	// today, so their registries have the counters registered but
	// always-zero (matches the single-registry pattern in
	// [wire-opsmetrics-single-registry]). result ∈ {allowed, denied}.
	auditOrgEvent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_audit_org_event_total",
		Help: "Count of AuthorizeOrgAction invocations, labelled by action (closed 11-verb OrgAction vocabulary) and result (allowed | denied). Deny rate by action is the dashboard signal for 'is some action suddenly rejecting every caller?'; allow rate is the floor. Per-org breakdown lives in the audit_events Postgres table — Prometheus cardinality would explode past 110K series if labelled by org.",
	}, []string{"action", "result"})
	// authzDenied / authzAllowed are the simpler split that the rest
	// of the codebase uses to keep allow-only and deny-only dashboards
	// (e.g. one GRAFANA panel each). They share the
	// audit_org_event_total time series internally — registered as
	// separate collectors to keep the metric name readable for the
	// common queries. Same metric, two collectors: that double-counts
	// the storage cost (one extra series per action), which is why we
	// keep the explicit pair rather than going to a single
	// result-labelled counter.
	authzDenied := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_authz_denied_total",
		Help: "Per-action count of AuthorizeOrgAction deny paths (action: closed 11-verb OrgAction vocabulary). The dashboard panel is rate(_denied[5m]) by action.",
	}, []string{"action"})
	authzAllowed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_authz_allowed_total",
		Help: "Per-action count of AuthorizeOrgAction allow paths (action: closed 11-verb OrgAction vocabulary). The dashboard panel is rate(_allowed[5m]) by action.",
	}, []string{"action"})
	wakeIDV4Fallback := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_wake_id_v4_fallback_total",
		Help: "Count of wake_id mints where uuid.NewV7 returned an error and the engine fell back to uuid.New (v4). Any non-zero rate indicates a broken crypto/rand subsystem and breaks the time-ordering invariant the instances_wake_id_app_idx partial index is built on. Should never increment in production.",
	})
	// snapshot_disk_drift (PR scale-out readiness #3): every detected
	// disk-vs-DB discrepancy under <SnapDir>/<depID>/ increments the
	// counter. Sweep is read-only; rate(snapshot_disk_drift_total[5m])
	// alerts on a non-zero rate.
	snapshotDiskDrift := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_snapshot_disk_drift_total",
		Help: "Count of disk-vs-DB discrepancies observed by the read-only /srv/fc/snap drift sweep (PR scale-out readiness #3). Each Tick increments once per missing file, size mismatch, unexpected entry, or non-regular entry under <SnapDir>/<depID>/. Repeated sweeps increment the counter while the discrepancy remains; rate(snapshot_disk_drift_total[5m]) alerts on a non-zero rate. Sweep never writes — diagnostic only.",
	})
	// capacity_signature_rejected (ADR-053 §3): every ReportCapacity
	// frame that fails schedule.VerifyNodeSignature increments the
	// counter. The handler rejects the whole stream (not per-frame)
	// with codes.Unauthenticated, so a single bad frame contributes
	// exactly one increment + closes the stream. A non-zero rate is
	// the canary for a stale or rotated vmmd node key, a clock-skew
	// induced canonical-payload mismatch, or a hostile publisher
	// probing the stream. Unlabelled — the closure of the stream is
	// the natural bound on cardinality (one increment per stream,
	// not per frame), and the node_id is already in the audit log
	// via Warn emission.
	capacitySignatureRejected := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_capacity_signature_rejected_total",
		Help: "Count of CapacityReport streams rejected by scheddgrpc.Server.ReportCapacity because sched.VerifyNodeSignature returned ErrUnknownNodeKey, ErrEmptySignature, or ErrSignatureMismatch (ADR-053 §3). One increment per rejected stream (the handler rejects the whole stream on the first bad frame, not per-frame). A non-zero rate is the canary for a stale/rotated node key, a clock-skew-induced canonical-payload mismatch, or a hostile publisher. Unlabelled — the node_id is in the audit log via the Warn emission; cardinality is bounded by stream-rate.",
	})

	alertEvalSkippedDegradedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_alert_eval_skipped_degraded_total",
		Help: "Count of alert-rule evaluations skipped because pkg/appmetrics returned a degraded source (Prometheus unreachable, invalid app id, etc). Unlabelled. A non-zero rate indicates a Prometheus outage or a customer-supplied rule with a malformed app id; pair with alertEvalFiredTotal for the dashboard's alert-evaluation-health panel.",
	})
	alertEvalFiredTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_alert_eval_fired_total",
		Help: "Count of alert-rule evaluations where the comparison crossed the threshold AND the cool-down claim won. Unlabelled. Paired with alertEvalSkippedDegradedTotal for the dashboard panel.",
	})
	// Issue #976 / ADR-122 / SAFE-RELEASES-A: canary_progression
	// meterd tick counters. Single-registry: registered on every
	// daemon's OpsMetrics; only meterd increments. Unlabelled
	// advanced counter (fleet rollup) + errors counter labelled by
	// reason ∈ {advance, list_in_flight}
	// (closed vocabulary, cardinality budget = 2).
	canaryProgressionAdvancedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_canary_progression_advanced_total",
		Help: "Count of canary step boundaries crossed by the meterd canary_progression tick (the atomic APID canary advance accepted). Unlabelled — fleet-level rollup. A non-zero rate is the heartbeat; a stalled rate combined with canaryProgressionErrorsTotal('advance') is the §12 dashboard tripwire for an APID outage.",
	})
	canaryProgressionErrorsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_canary_progression_errors_total",
		Help: "Count of per-row errors inside the canary_progression tick, labelled by reason ∈ {advance, list_in_flight} (closed vocabulary). advance covers atomic APID transition failures; list_in_flight signals a Postgres SELECT failure (fleet-wide tripwire).",
	}, []string{"reason"})
	for _, reason := range []string{"advance", "list_in_flight"} {
		canaryProgressionErrorsTotal.WithLabelValues(reason)
	}
	// SAFE-RELEASES code-review hardening (migration 00517):
	// tripwire counter for the canary_progression tick seeing a
	// zero canary_step_started_at. Post-00517 the column is NOT NULL
	// DEFAULT NOW(), so a non-zero rate means a write path bypassed
	// the schema default — exactly the silent-soak-bypass hole
	// finding #1 was worried about. Unlabelled (fleet rollup; per-
	// deployment detail lives in the deploy.traffic_changed audit
	// row).
	canaryProgressionZeroTimestampTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_canary_progression_zero_timestamp_total",
		Help: "Count of canary_progression tick rows whose canary_step_started_at was the zero time (post-00517 the column is NOT NULL DEFAULT NOW(), so a non-zero rate is the tripwire for a write path bypassing the apid CreateDeployment stamp). Unlabelled — fleet-level rollup.",
	})
	alertDeliveryAttemptsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_alert_delivery_attempts_total",
		Help: "Count of dispatched alert-rule webhook attempts, labelled by outcome ∈ {delivered, failed} (closed vocabulary, cardinality budget = 2). The counter surfaces the dispatcher's success rate without exposing per-customer detail — the audit events table is the per-customer detail.",
	}, []string{"outcome"})
	for _, outcome := range []string{"delivered", "failed"} {
		alertDeliveryAttemptsTotal.WithLabelValues(outcome)
	}
	alertActionExecutedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_alert_action_executed_total",
		Help: "Count of alert-rule firings that executed an in-process action (rollback / demote / promote) on the deployment the rule was scoped to. Labelled by action ∈ {rollback, demote, promote} (closed vocabulary). 'webhook' is intentionally absent — webhook fan-out is the legacy path and is counted under alert_delivery_attempts_total. A non-zero rate is the §12 dashboard's auto-rollback / auto-promote tripwire; pair with the alert.delivered audit kind for per-customer detail.",
	}, []string{"action"})
	for _, action := range []string{"rollback", "demote", "promote"} {
		alertActionExecutedTotal.WithLabelValues(action)
	}
	// PR-P4 — Paddle webhook hardening counters. Single-registry:
	// registered on every daemon's OpsMetrics; only apid increments.
	// Unlabelled — the per-event detail lives in the journal line
	// + the audit row, both of which carry the event_id.
	paddleWebhookVerifyFailedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_paddle_webhook_verify_failed_total",
		Help: "Count of Paddle webhook signature verify failures (cmd/apid/handlers_ext.go::paddleWebhook). Unlabelled — the per-event detail (event_id, err, tolerance) is in the journal line emitted alongside the increment. The counter is the fleet-level tripwire for 'wrong webhook secret in dashboard' or 'clock skew beyond tolerance' (PR-P4).",
	})
	paddleWebhookReplaySuppressedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_paddle_webhook_replay_suppressed_total",
		Help: "Count of Paddle webhook events rejected by pkg/webhookdedupe (PR-P4). Unlabelled — the per-event delivery_id is in the existing webhook.replay_rejected audit row. The counter is the fleet-level tripwire for 'Paddle is redelivering' — a sustained rate over 30 min is the alert.",
	})
	alertEvaluatorEnabled := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_alert_evaluator_enabled",
		Help: "1 when the pkg/alerts.Evaluator tick is wired and running on this meterd process; 0 when it isn't (Prometheus not configured, FAAS_HOST_AGE_IDENTITY_PATH empty, no host.age on disk, or meterd's caller skipped the loop). Unlabelled. Pair with alertEvalFiredTotal / alertEvalSkippedDegradedTotal for the dashboard's alert-evaluation-health panel; alert rule 'alertEvalDisabled' queries this gauge for the §12 self-healing alert.",
	})
	// Initialise to 0 so /healthz reports "evaluator disabled" from
	// boot until cmd/meterd explicitly enables it — the absence of a
	// gauge series would otherwise look like "never scraped", which
	// Prometheus treats as a missing time series rather than zero.
	alertEvaluatorEnabled.Set(0)

	// Issue #1233 / ADR-123 — alert-preset signal gauges.
	meterdAccountSpendEur := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_account_spend_eur",
		Help: "Per-{account_id} MTD EUR spend, recomputed by the meterd tick loop every AlertEvalInterval. Backs the alert preset spend_eur_20. Cardinity is bounded by account count.",
	}, []string{"account_id"})
	apidTenantSurfaceCertExpirySeconds := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_tenant_surface_cert_expiry_seconds",
		Help: "Per-{account_id, app_id, hostname} remaining-seconds gauge for the meterd_tenant_surface_cert_expiry_state walker (CLAUDE.md ownership rule: meter daemon owns the writer side; apid reads via state.MinCertExpiryForApp). Backs the alert preset cert_expiring_14d. Cardinity is bounded by per-tenant surface count.",
	}, []string{"account_id", "app_id", "hostname"})
	apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_tenant_surface_cert_expiry_refresher_walk_complete_total",
		Help: "Per-{result} walker status counter for the meterd_tenant_surface_cert_expiry refresher (issue #1233 / ADR-123). Closed vocabulary {ok, error}. Surfaces a healthy vs failing walker for the §12 self-healing alert.",
	}, []string{"result"})
	for _, r := range []string{"ok", "error"} {
		apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal.WithLabelValues(r)
	}
	// Issue #1233 / ADR-123 PR-B — `api_down` signal gauge.
	// Writer: cmd/meterd/api_reachability_sweep.go::APIReachabilitySweepLoop
	// stamps {account_id, app_id} = 1.0 (reachable) or 0.0 (no
	// successful invocation in the last 5 min). Evaluator at
	// pkg/alerts/evaluator.go reads the same data via
	// state.WasInvokedSuccessfullySince; the gauge is the operator-
	// visible mirror. Cardinality bounded by per-account app count
	// (Hobby=5, Pro=25, Scale=100).
	meterdAPIReachable := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_api_reachable",
		Help: "Per-{account_id, app_id} reachability gauge: 1.0 when the app served a successful invocation within the last 5 minutes, 0.0 when it has not. Backs the alert preset api_down. Mirrors pkg/alerts/evaluator.go's inline WasInvokedSuccessfullySince lookup.",
	}, []string{"account_id", "app_id"})
	// Issue #1233 / ADR-123 PR-B — `deploy_failed` signal counter.
	// Writer: cmd/meterd/deployment_failure_sweep.go::DeploymentFailureSweepLoop
	// bumps the counter by the delta of new failures since the
	// previous sweep. Evaluator at pkg/alerts/evaluator.go reads the
	// same data via state.CountFailedDeploymentsSince; the counter is
	// the operator-visible mirror. Cardinality bounded by per-account
	// app count (Hobby=5, Pro=25, Scale=100).
	apidDeploymentFailedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_deployment_failed_total",
		Help: "Per-{account_id, app_id} delta counter of deployments whose status transitioned to 'failed' since the previous sweep. Backs the alert preset deploy_failed. Mirrors pkg/alerts/evaluator.go's inline CountFailedDeploymentsSince lookup.",
	}, []string{"account_id", "app_id"})
	imagedOCIPull := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_oci_pull_duration_seconds",
		Help: "Latency of imaged's OCI registry pulls (manifest, config, blob, above-base), in seconds. Sized to api.OCIPullTimeoutSeconds (60 s).",
		// OCI manifest/config are fast (10–500 ms); blob downloads can run
		// multi-second for big layers; 60 s ceiling = OCIPullTimeoutSeconds.
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 45, 60},
	}, []string{"op", "result"})
	// issue #170 / PR-A: per-{app,node} instance-stats gauges. Sized
	// for the poller’s 200 ms cadence — the per-tick histogram tops
	// out at the 200 ms interval so a regression that doubles the
	// interval surfaces immediately.
	instanceCPUPct := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_instance_cpu_pct",
		Help: "Host cgroup CPU percent, per (app, node) — max over live siblings of that app on that node (issue #170 / PR-A). Peaks are what scaling cares about.",
	}, []string{"app", "node"})
	instanceRSSMB := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_instance_rss_mb",
		Help: "Cgroup memory.current in MiB, per (app, node) — sum over live siblings (issue #170 / PR-A). Capacity rollup.",
	}, []string{"app", "node"})
	instanceInflightReqs := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_instance_inflight_requests",
		Help: "Outbound ForwardHTTP count in flight, per (app, node) — sum over live siblings (issue #170 / PR-A). Load rollup.",
	}, []string{"app", "node"})
	instanceStatsCollectDur := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    prefix + "_instance_stats_collect_seconds",
		Help:    "Per-Tick wall-clock duration of the instancestats poller (issue #170 / PR-A). Buckets sized to the 200 ms polling interval.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1.0},
	})
	// cpuStatsCollectDur: introduced for issue #279 / PR-B / ADR-039.
	// Wall-clock duration of the CPU-rate-and-accumulator read path
	// on the vmmd wire (the per-instance cpustats.Cache.Lookup loop
	// in vmmdgrpc.Stats) and on the schedd wire (the per-row
	// SnapshotAll + proto-build loop in scheddgrpc.ListInstanceStats).
	// Distinct from instanceStatsCollectDur (which covers the
	// whole 200 ms schedd poller tick — much longer) because the
	// CPU hot path is per-RPC, not per-tick, and we want bucket
	// resolution at the cache.Lookup scale (sub-ms per row,
	// ~µs at 100 instances). nil on daemons that don't expose the
	// path (apid, imaged, builderd, gatewayd-internal, meterd, githubd,
	// faas CLI).
	var cpuStatsCollectDurLocal prometheus.Histogram
	switch prefix {
	case "vmmd":
		cpuStatsCollectDurLocal = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_stats_cpu_collect_seconds",
			Help:    "Per-RPC wall-clock duration of the CPU-rate-and-accumulator read path on the vmmd side (issue #279 / PR-B / ADR-039). Buckets sized to per-row cpustats.Cache.Lookup cost.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1},
		})
	case "schedd":
		cpuStatsCollectDurLocal = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "schedd_list_instance_stats_collect_seconds",
			Help:    "Per-RPC wall-clock duration of ListInstanceStats on the schedd side (issue #279 / PR-B / ADR-039). Buckets sized to per-row proto-build cost.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1},
		})
	}
	instanceStatsPartialErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_instance_stats_partial_errors_total",
		Help: "Per-node dial/decode failures during an instancestats poller Tick (issue #170 / PR-A). The poller logs and continues on partial failures; a non-zero rate points at a sick node.",
	}, []string{"node"})
	// Issue #463 / ADR-069 / ADR-071 / PR-C §4: per-(app,
	// sidecar) restart-counter. vmmd increments this on every
	// dispatchSidecarRestart; schedd is the canonical writer
	// of the corresponding events table row (SidecarRestart).
	// The metric is exposed from BOTH daemons so a host-side
	// observation doesn't depend on a schedd restart. The
	// metric name ships as `<prefix>_sidecar_restart_total`
	// (vmmd → "vmmd_sidecar_restart_total"; schedd likewise);
	// the dashboard sums the two via `sum(rate(...))` so the
	// daemon-owned increment is invisible to operators. See
	// ADR-071 for the cardinality bound (apps × SidecarCapMax
	// ≤ 200 worst-case).
	sidecarRestartTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_sidecar_restart_total",
		Help: "Count of sidecar restart cycles, per (app, sidecar) — incremented by vmmd's dispatchSidecarRestart (PR-C §4) on every guest-init Supervisor.OnCrash event for an essential sidecar. Bounded by apps × SidecarCapMax (issue #463 / ADR-069 cap = 2).",
	}, []string{"app", "sidecar"})
	// Issue #279 (PR-B, CPU-hour visibility): cumulative
	// CPU-seconds per (app, node), sourced from the vmmd
	// cpu_seconds wire field. Sum rollup (cumulative work,
	// not peak). Counter (not Gauge) because the value is
	// monotonic between cgroup regressions. On a regression
	// the wire reports a smaller value; the rollup Adds the
	// delta only (curr - prev) and the counter stays
	// monotonic — the same shape as the spec §12 "log
	// scale" guidance for cumulative CPU work. Bounded
	// structurally by #apps × #nodes, ADR-036.
	instanceCPUSecondsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_instance_cpu_seconds_total",
		Help: "Cumulative CPU-seconds consumed by live instances, per (app, node) — sum over live siblings, source is vmmd's cpu_seconds wire field (issue #279 / PR-B).",
	}, []string{"app", "node"})
	// issue #301 / ADR-044: per-app CPU-throttle counter (the
	// dashboard top-N view) + per-slice throttle ratio gauge (the
	// FaasCpuStarvation alert source). See the field doc comments
	// on throttleSecondsTotal and throttleRatio for the cardinality
	// contract and the two-tier admission via topAppSet.
	throttleSecondsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_cpu_throttle_seconds_total",
		Help: "Cumulative CPU-seconds the cgroup spent throttled, per (account_id, app_id) — sum over live siblings, source is vmmd's cpu.stat throttled_usec (issue #301, ADR-044). Bounded at topAppSetCap (100) real cached pairs plus the (\"other\", \"other\") overflow; the (account_id, app_id) tuple is layered above accountLabelSet so a customer id at the 10k level can still be demoted past the top-100 here. Default overflow label is (\"other\", \"other\"); the panel selector {app_id!=\"other\"} excludes the overflow bucket. The FaasCpuStarvation alert reads the sibling throttleRatio gauge instead.",
	}, []string{"account_id", "app_id"})
	throttleRatio := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_cpu_throttle_ratio",
		Help: "Per-slice throttle ratio (throttle_delta / (throttle_delta + usage_delta)) over the 5s sampler window (issue #301, ADR-044). Labelled by slice ∈ {tenant-free, tenant-hobby, tenant-pro, tenant-scale}; the closed plan set is pre-instantiated at boot so the dashboard surfaces 'no data' → '0' on an idle box. The FaasCpuStarvation alert reads slice=~\"tenant-.*\" > 0.8 for 5m. Ratio is the operationally meaningful number — a counter delta alone would conflate 'lots of CPU, low throttle' with 'no CPU, no throttle'.",
	}, []string{"slice"})
	// Issue #169 / #172: scale-up trigger observability. Outcome
	// label set is closed ({admit, reject_at_cap, no_signal,
	// cooldown_held, min_floor_already, overage_cap_reached});
	// pre-instantiated below so the rows surface in /metrics from
	// boot. App label is per-app (bounded by apps with autoscale
	// configured) — the closed outcome set means the total series
	// cardinality is O(autoscale-enabled apps × 6) (P1A: × 4 → × 6
	// after adding min_floor_already and overage_cap_reached).
	//   - admit (Engine.admitGate wakeAdmit, pkg/sched/engine.go:4890).
	//   - reject_at_cap (admitGate wakeRejectAtCap, :4856-4860).
	//   - no_signal (scaleup/trigger.go:decide short-circuit on no
	//     RPS/CPU signal yet).
	//   - cooldown_held (admitGate wakeCooldownHeld, :4862-4867;
	//     PR-C issue #462 wake-gate path when Concurrency(appID) > 0
	//     AND time.Since(apps.last_scale_out_at) < ScaleOutCooldownS).
	//   - min_floor_already (admitGate wakeMinFloorAlready, :4868-4873;
	//     ScalingPolicy.MinInstances already met, no traffic signal).
	//   - overage_cap_reached (admitGate wakeOverageCapReached,
	//     :4876-4888; issue #561 OverageChecker reports OverageReached).
	//
	// P1A: only the first four are emitted by the scaleup/trigger.go
	// `decide` function; the last three (cooldown_held,
	// min_floor_already, overage_cap_reached) are emitted by
	// Engine.admitGate when called via the Wake / AdmitInstance path
	// through admitAndDispatch (engine.go:1238). The
	// `admitAndDispatchForDeployment` (engine.go:1085) path bypasses
	// admitGate by design — see that function's doc comment for the
	// asymmetry rationale (PR-C invariant: ledger caps, not lock caps).
	scaleUpDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_scale_up_decisions_total",
		Help: "Per-app scale-up trigger decisions. outcome ∈ {admit, reject_at_cap, no_signal, cooldown_held, min_floor_already, overage_cap_reached}; app label is the apps.id.",
	}, []string{"app", "outcome"})
	scaleUpAdmitRPS := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: prefix + "_scale_up_admit_rps",
		Help: "Per-instance RPS at the moment the trigger admitted a new instance. Sized to the per-instance RPS target range (1..1000); p95/p99 is the spec §12 'scale-up aggressiveness' diagnostic.",
		// Sized for per-instance RPS, not fleet RPS. Hobby RAM tiers
		// hit ~50 RPS/inst; Pro's higher RAM and CPU hit ~250;
		// Scale is bounded by plan MaxConcurrency = 20 × per-instance
		// ≈ 1000. 1..1000 covers the realistic range; the 2000
		// ceiling catches pathological cases.
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2000},
	})
	// issue #171: aggressive-reaper scale-down decision counter.
	// Symmetric with scaleUpDecisions — same (app, outcome) label
	// shape, same outcome pre-instantiation. "park" fires once per
	// app per 10s reaper tick when at least one instance is parked;
	// "keep" fires when the aggressive path decided to hold the
	// line. min_floor_already (PR-C, issue #462) is a semantic
	// upgrade over "keep" emitted by the reaper when
	// Concurrency(appID) >= ScalingPolicy.MinInstances AND the
	// aggressive path would otherwise have parked an instance.
	// cooldown_held (P1C) fires when the per-app scale-in cooldown
	// consult in ReapAggressive (pkg/sched/reaper.go) skipped the
	// entire app — the app is absent from the park slice and the
	// caller never iterates over it. Idle branch (ReapIdle) emits
	// the same three of the four outcomes (cooldown_held, park,
	// min_floor_already) since P1D; `keep` is intentionally omitted
	// because ReapIdle has no traffic-signal consult (no
	// desiredByApp).
	scaleDownDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_scale_down_decisions_total",
		Help: "Per-app aggressive-reaper decisions (issue #171). outcome ∈ {park, keep, min_floor_already, cooldown_held}; app label is the apps.id.",
	}, []string{"app", "outcome"})
	// Issue #557 / ADR-071 §Decision 1: proactive min-instances
	// floor reconciler observability. Closed outcome set
	// ({admit, floor_met, disabled, at_capacity, ram_ceiling,
	// cooldown_held, error, backoff_held}); pre-instantiated below
	// so the rows surface in /metrics from boot.
	floorReconcileDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_floor_reconcile_decisions_total",
		Help: "Per-app proactive min-instances floor reconciler decisions (issue #557 / ADR-071). outcome ∈ {admit, floor_met, disabled, at_capacity, ram_ceiling, cooldown_held, error, backoff_held}; app label is the apps.id.",
	}, []string{"app", "outcome"})
	// Issue #557 / ADR-071 §Decision 4: per-app error counter. Kind
	// ∈ {admit_denied, admit_error}; sustained rate > 0 means the
	// customer's floor isn't being satisfied — alertable.
	floorReconcileErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_floor_reconcile_errors_total",
		Help: "Per-app proactive min-instances floor reconciler errors (issue #557 / ADR-071). kind ∈ {admit_denied, admit_error}; app label is the apps.id. Alert on rate > 0 for 5m — sustained errors mean the customer's floor isn't being satisfied.",
	}, []string{"app", "kind"})
	// Issue #557 / ADR-071 §Decision 1: global counter incremented
	// once per successful proactive floor wake. Baseline for the
	// "warm floor" dashboard panel.
	floorInstancesAdmitted := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_floor_instances_admitted_total",
		Help: "Total number of instances admitted by the proactive min-instances floor reconciler (issue #557 / ADR-071). Global counter; sustained-zero on a tier with Hobby+ accounts is a quiet alarm.",
	})
	sseClients := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_sse_clients",
		Help: "Number of currently open /v1/events SSE connections (Move 3, M7.5 prep). The dashboard's per-page EventSource is one connection; the CLI's faas tail is another. Zero is the idle value.",
	})
	egressDeny := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_egress_deny_total",
		Help: "Per-CIDR drop counter for the nftables egress denylist (PR-E, spec §11 + §12). The cidr label is the DenyEntry.CounterName (e.g. \"drop_v4_10_0_0_0_8\") and the family label is the nft family keyword (\"ip\" / \"ip6\"). The vmmd scrape adapter (cmd/vmmd/poller.go) reads `nft list counters` every 15s and emits the per-counter delta so the Prometheus series sees the rate of drops per CIDR. The imaged-side mirror is oci_egress_deny_total on cmd/imaged's registry because the OCI dialer is user-space — nftables counters do not see it.",
	}, []string{"cidr", "family"})
	// Issue #300: per-tenant RPS gauge. Sampled 5s by the daemon's
	// topNSampler goroutine (cmd/apid/topn.go). Bounded at
	// topAccountSetCap (1000) + "other" via topAccountSet — see
	// pkg/wire/topn.go for the contract. The "other" label is
	// distinct from the counter-level "__other__" overflow so a
	// dashboard panel can filter one without filtering the other.
	topTenantRPS := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_top_tenant_rps",
		Help: "Per-tenant 5s RPS sampled by the daemon's topNSampler goroutine (issue #300). Labelled by account_id; bounded at the top 1000 customers by 24h request count with the remainder collapsed to account_id=\"other\" (the panel selector is {account_id!=\"other\"} for the top-N view). The \"other\" label here is distinct from the counter-level \"__other__\" overflow — see topAccountSet docs (pkg/wire/topn.go) for the two-tier cardinality contract. FaasTenantAbuse alert (spec §12) fires when the gauge exceeds 500 rps sustained for 10m. NOTE: the very first /metrics scrape after a daemon restart surfaces the cumulative request count divided by the 5s sample interval (because the sampler has no prior tick to diff against); the value converges to a true 5s delta on the second tick. This is a presentation-view approximation, not a counter drift.",
	}, []string{"account_id"})
	// apid_logs_emitted_total — Move 4 (issue #254). Counter
	// shape and naming match the §12 conventions; the `app`
	// label is the apps.id (UUID) and is bounded by per-plan
	// app quotas (Hobby=5 / Pro=25 / Scale=100) so cardinality
	// stays inside Prometheus' guideline. Registered on every
	// daemon (single-registry pattern, memory
	// wire-opsmetrics-single-registry); only apid increments
	// in production via ObserveLogEmitted.
	apidLogsEmittedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_logs_emitted_total",
		Help: "Per-app SSE log frames emitted to clients (issue #254, Move 4). One Increment per `event: log` frame written to the SSE response body in cmd/apid/handlers_ext.go::writeAppLogEvent. The series is registered on every daemon so the struct is a single-registry; only apid's production path increments. Use this rate with apid_ops_total{op=\"app_logs\"} to break out per-app log throughput — that op label is already on every streamAppLogs handler entry.",
	}, []string{"app"})
	// apid_logs_dropped_total (issue #309 / tier-2 DX). Single
	// CounterVec labelled by reason ∈
	// {slow_subscriber, filter_grep, filter_level} — the three
	// drop sites the platform introduces. The `app` label is
	// intentionally absent: a customer asking "why are my logs
	// not coming through?" doesn't need per-app series (the
	// appid is recoverable from the logs-emitted side) and
	// per-app series would multiply cardinality by the per-plan
	// app quota (Hobby=5 / Pro=25 / Scale=100). Pre-instantiated
	// below so /metrics surfaces zero from boot. Single-registry:
	// registered on every daemon — only schedd and vmmd
	// increment via IncLogDropped; other daemons sit at zero.
	apidLogsDroppedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_logs_dropped_total",
		Help: "Log frames the platform dropped or filtered, labelled by reason ∈ {slow_subscriber, filter_grep, filter_level} (issue #309 / tier-2 DX). slow_subscriber increments in pkg/fcvm/logbuf/ring.go::Write when the ring is full; filter_grep and filter_level increment in pkg/scheddgrpc/server.go::StreamAppLogs when the customer's --grep regex or --level floor doesn't match. The metric name matches the existing comments at pkg/fcvm/logbuf/ring.go:181,230 so the wire field formalises what was previously a TODO marker.",
	}, []string{"reason"})
	// Pre-instantiate the closed (reason) label set so the
	// dashboard panel `rate(apid_logs_dropped_total[5m])`
	// surfaces a non-zero series the moment the first drop
	// fires. The label set is the three drop sites documented
	// in the field comment on apidLogsDroppedTotal: the ring-
	// full path, the --grep filter path, and the --level filter
	// path. Adding a new drop reason requires extending both
	// this loop and the closed-set guard in IncLogDropped
	// (pkg/wire/metrics.go) — the switch in IncLogDropped is
	// the load-bearing guard that prevents accidentally
	// creating a new series.
	for _, reason := range []string{"slow_subscriber", "filter_grep", "filter_level"} {
		apidLogsDroppedTotal.WithLabelValues(reason)
	}
	// egressSourceErrors: per-instance sysfs read failures
	// (ADR-046, step 7). The network poller in cmd/vmmd reads
	// /sys/class/net/<vethHost>/statistics/rx_bytes for every
	// live instance on a 250 ms tick; a read failure increments
	// this Counter. The metric name is intentionally bare
	// (no labels) so it does not blow the Prometheus
	// cardinality budget — the failure instance is recoverable
	// from the cache's last-known vethHost. Persistent rate
	// is the alert source. Registered on every daemon so the
	// single-registry pattern (memory wire-opsmetrics-single-
	// registry) holds; only vmmd's production path increments.
	egressSourceErrors := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_egress_source_errors_total",
		Help: "Count of per-instance sysfs read failures from the vmmd network poll adapter (ADR-046, step 7). Each increment corresponds to one (live instance, 250ms tick) where reading /sys/class/net/<vethHost>/statistics/rx_bytes returned an error; the cache baseline for that instance is preserved so the next successful tick picks up where the failed one left off. Alert: rate > 0 for 5m on vmmd.",
	})
	// issue #419 / ADR-046: sign-in OAuth disabled-redirect counter.
	// Closed `provider` label set {google, github} — the two values
	// pkg/auth.SignInConfig knows about. One increment per click on a
	// consent route when the provider is SignInProviderDisabled (so
	// the handler returns 503 oauth_provider_unavailable). Single-
	// registry pattern (memory wire-opsmetrics-single-registry):
	// registered on every daemon even though only apid increments.
	// Distinguishing "dead OAuth button" from the historical 500
	// *_oauth_misconfigured path is the operator's first signal that
	// either (a) the dashboard gating (PR's second half) didn't take,
	// or (b) a customer has a stale bookmark to a now-disabled button.
	oauthDisabledTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_oauth_disabled_total",
		Help: "Click-through counter for sign-in OAuth consent routes when the provider is disabled at boot (issue #419 / ADR-046). provider ∈ {google, github}. One increment per click — handler returns 503 oauth_provider_unavailable. A non-zero rate means either the dashboard gating is bypassed (operators see this scrape first) or a customer is hitting a stale bookmark. Distinct from the historical 500 *_oauth_misconfigured which is now unreachable at boot (the half-configured pair fails the load).",
	}, []string{"provider"})
	// Mega-PR B / stateless-advisory observability. Two counters
	// sharing the same single-registry pattern as
	// oauthDisabledTotal: registered on every daemon, only the
	// forward daemon (vmmd) / the receiver daemon (apid) increment
	// in production. See the field doc comments on
	// advisoryBatchesEmittedTotal / apidStatelessAdvisoryEventsTotal
	// for the closed-set semantics.
	//
	// Note: HELP strings must NOT mention another daemon's metric
	// name verbatim — TestOpsMetrics_IndependentRegistries greps for
	// "vmmd_" / "builderd_" / etc. in the body to detect registry
	// leaks, and every daemon registers every metric per the
	// single-registry pattern (memory wire-opsmetrics-single-registry),
	// so cross-daemon names in HELP would false-positive the test.
	// The "this counter's pair" relationship is documented here in
	// the field comment instead, where the leak-grep doesn't reach.
	advisoryBatchesEmittedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_stateless_advisory_batches_emitted_total",
		Help: "Per-batch stateless-advisory forward outcomes (Mega-PR B). result ∈ {ok, dial_failed, rejected, unavailable_after_retry}. One increment per ForwardStatelessAdvisory RPC at the forward daemon, not per underlying fanotify event (the forward-side taxonomy is batch-grained). Single-registry: registered on every daemon; only the forward daemon increments via ObserveAdvisoryBatchResult.",
	}, []string{"result"})
	apidStatelessAdvisoryEventsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_stateless_advisory_events_total",
		Help: "Per-batch stateless-advisory inbound counter (Mega-PR B). severity ∈ {high, warn, info}. One increment per ForwardStatelessAdvisory RPC at the receiver daemon whose audit row was written. Mirrors advisoryBatchesEmittedTotal on the forward side. Single-registry: registered on every daemon; only the receiver daemon increments via ObserveStatelessAdvisory.",
	}, []string{"severity"})
	// issue #432 phase 5: githubd → apid per-app build enqueue
	// counter. Single CounterVec with a closed `kind` label set
	// ({github} — the only value the githubd bridge currently
	// produces; the label is forward-compat for future
	// daemon-to-daemon build sources). Pre-instantiated so the
	// row surfaces in /metrics from boot (the §12 dashboard panel
	// queries `rate(apid_githubd_bridge_enqueued_total[5m])` to
	// surface github-triggered build volume per app/account).
	// Single-registry: registered on every daemon; only apid
	// increments via IncGithubdBridgeEnqueued.
	apidGithubdBridgeEnqueuedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_githubd_bridge_enqueued_total",
		Help: "Per-app build enqueue counter from the githubd → apid bridge (issue #432 phase 5). kind ∈ {github}. One increment per EnqueueBuild RPC that lands a build row. Zero on a healthy box with no GitHub App bound; a non-zero rate is the cap on github-triggered build volume.",
	}, []string{"kind"})
	// issue #432 phase 5 / ADR-050 §109: path-filter mode
	// counter. Labelled by mode ∈ {paths, full_fallback,
	// truncated, error, breaker_open} — the closed set
	// githubd's push dispatch can land in. Incremented once
	// per inbound webhook in pkg/githubd/service.go at the
	// call site that picks the filterMode. Pre-instantiated
	// below so every row surfaces in /metrics from boot; the
	// §12 dashboard panel groups the rate by mode.
	githubdPathFilterTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_path_filter_total",
		Help: "Path-filter mode counter for githubd push dispatch (issue #432 phase 5, ADR-050 §109). mode ∈ {paths, full_fallback, truncated, error, breaker_open}. One increment per inbound webhook after lookupChangedFiles picks a mode. mode=paths is the optimistic path; the others collapse into the rebuild-all fallback for that push. The single-registry pattern demands the field is present on every daemon's OpsMetrics — only githubd increments via ObserveGithubdPathFilter.",
	}, []string{"mode"})
	// PR-E sister collector for the user-space OCI dialer. Only
	// registered when prefix == "imaged" — on every other daemon the
	// field stays nil and the imaged-side hook in cmd/imaged/main.go
	// must nil-check the accessor (EgressDenySeries / OCIEgressDeny)
	// before calling. The metric name is oci_egress_deny_total so an
	// operator can disambiguate "firewall blocked it" (this metric on
	// vmmd) from "dialer refused it" (this metric on imaged) — they
	// have different remediation paths.
	var ociEgressDeny *prometheus.CounterVec
	// M-1 / ADR-136 §Decision 2: imaged-only ownership clamp +
	// layer-entry-skipped counters. Declared as nil on every other
	// daemon (the accessors nil-check before WithLabelValues /
	// Write, mirroring OCIEgressDeny).
	var ownershipClamp *prometheus.CounterVec
	var layerEntrySkipped prometheus.Counter
	// Issue #517 / PR-C / ADR-064 — wake-phase collector pair.
	// Counter gauges per-phase emit counts; histogram buckets
	// the per-phase duration. Both labelled by the same closed
	// (phase, result) tuple; the closed 13-phase set is
	// pre-instantiated below so the §12 wake-latency panel exists
	// from boot. The histogram buckets are sized for the wake
	// envelope: queue→admit <100ms; boot <30s; readiness <60s;
	// proxy <5s; the 60s tail catches pathological stalls.
	wakePhaseEmitted := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_wake_phase_emitted_total",
		Help: "Count of wake-timeline events emitted via pkg/events.Platform, labelled by phase (the substring after `wake.`, e.g. `boot_started`, `readiness_200`, `proxy_first_byte`) and result ∈ {ok, failed} (issue #517 / PR-C, ADR-064). Single-registry: registered on every daemon; only schedd / vmmd / gatewayd-internal / builderd / apid increment via Platform.Emit. The closed 13-phase set is pre-instantiated at boot so the §12 wake-latency panel surfaces zero on an idle daemon.",
	}, []string{"phase", "result"})
	wakePhaseDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_wake_phase_duration_seconds",
		Help: "Latency of pkg/events.Platform.Emit (the AppendEvent round-trip), labelled by phase and result (issue #517 / PR-C, ADR-064). Sized for the wake envelope: queue→admit <100ms; boot <30s; readiness <60s; proxy <5s; the 60s tail catches pathological stalls.",
		Buckets: []float64{
			0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
		},
	}, []string{"phase", "result"})
	// commonCollectors is the per-daemon collector set that every
	// prefix registers. PR-E adds ociEgressDeny to the set when
	// prefix == "imaged" — keeping the common slice as a single source
	// of truth (review finding #5 on PR #332) means a future collector
	// only needs to be added here, not in two parallel MustRegister
	// calls that would silently drift apart.
	commonCollectors := []prometheus.Collector{
		ops, dur, watchdogKills, warmSnapshotErrors, warmupErrors, livenessRestarts, workloadOOMKills, daemonRestartCount, daemonBuildInfo, daemonUptimeSeconds, daemonReady, faasDeployVersion, bridgeFramingTotal, guestInitDuration, wakeSnapshotTier, wakeFailure, wakeLatency, guestTailSeconds, guestTailFailedTotal, tailCapReached, eventsWriteFail, auditWriteFail, cveCheckTotal, cvesOpenTotal,
		writeRedirectTotal, writeRedirectLatency,
		auditWriteDur, cronFireNowDispatchDur, accountOrgMismatch, requestFailures, requestTotal, stripePushDur, paddlePushDur, polarPushDur,
		buildDur, buildQueueWait, residentGBPerCustomer, billingCapExceededTotal,
		meterdFloorAppliedTotal, meteredMBSecondsTotal,
		// ADR-123 alert-preset signal series — PR-A (3) + PR-B (2). Each
		// backs one of the 8 alert_presets catalog rows. Without
		// registration the Vecs are created but never reach /metrics.
		meterdAccountSpendEur,
		apidTenantSurfaceCertExpirySeconds,
		apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal,
		meterdAPIReachable,
		apidDeploymentFailedTotal,
		paddleWebhookVerifyFailedTotal, paddleWebhookReplaySuppressedTotal,
		auditOrgEvent, authzDenied, authzAllowed,
		wakeIDV4Fallback,
		snapshotDiskDrift,
		imagedOCIPull, instanceCPUPct, instanceRSSMB, instanceInflightReqs,
		instanceCPUSecondsTotal,
		instanceStatsCollectDur, instanceStatsPartialErrors,
		sidecarRestartTotal,
		scaleUpDecisions, scaleDownDecisions, scaleUpAdmitRPS, sseClients,
		egressDeny,
		failedLoginTotal, failedLoginDropped,
		failedLoginAuditWriteFailures,
		auditEventsDeletedTotal,
		auditEventsRetentionLagSeconds,
		auditEventsVolumeTotal,
		deploymentAuditGCRowsDeletedTotal,
		topTenantRPS,
		apidLogsEmittedTotal,
		apidLogsDroppedTotal,
		oauthDisabledTotal,
		advisoryBatchesEmittedTotal,
		apidStatelessAdvisoryEventsTotal,
		apidGithubdBridgeEnqueuedTotal,
		githubdPathFilterTotal,
		throttleSecondsTotal, throttleRatio,
		egressSourceErrors,
		wakePhaseEmitted, wakePhaseDur,
		// ADR-124 follow-up #2: plan_gate_rescued_by_exclude counter
		// (12 pre-instantiated series). See planGateRescuedByExclude
		// field declaration at line 292.
		planGateRescuedByExclude,
	}
	if cpuStatsCollectDurLocal != nil {
		commonCollectors = append(commonCollectors, cpuStatsCollectDurLocal)
	}
	// Issue #250 — off-host Postgres backup observability. Same
	// pre-instantiate-to-0 precedent as alertEvaluatorEnabled above:
	// without a tick, the gauge must still surface from boot so the
	// alert rule's `time() - pg_backup_last_pushed_seconds` query
	// has a series to compare against. Without this, a freshly-booted
	// box would look identical to one with no basebackup root —
	// both return NaN, and the alert is silently skipped.
	pgBackupLastPushed := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_pg_backup_last_pushed_seconds",
		Help: "Age of the newest Postgres basebackup tarball in /var/lib/pgsql/basebackup/, in seconds (issue #250). 0 when the directory is empty. The PgBackupStale alert (deploy/ansible/roles/prometheus/files/pg_backup.rules.yml) queries `time() - pg_backup_last_pushed_seconds > 86400` to surface a stuck push timer; the value is stamped once per minute by cmd/apid's pgBackupPushedSampler goroutine (60s tick).",
	})
	pgBackupLastPushed.Set(0)
	commonCollectors = append(commonCollectors, pgBackupLastPushed)
	// ADR-038 / Tier 3 / issue #197 B3.1: build_provenance
	// populator counter. Single CounterVec with a closed `code`
	// label set ({ok, error}); pre-instantiated below so both rows
	// surface in /metrics from boot. Unlabelled cardinality is
	// ZERO — every daemon gets the same field, only builderd
	// increments in production.
	provenanceWrites := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_provenance_writes_total",
		Help: "Build provenance populator outcomes (ADR-038). code ∈ {ok, error}. Every successful build should land an `ok` row; a growing `error` rate indicates the populator WARN-logged failures that surfaced as 404 on GET /v1/builds/{id}/provenance.",
	}, []string{"code"})
	commonCollectors = append(commonCollectors, provenanceWrites)
	if prefix == "imaged" {
		ociEgressDeny = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_oci_egress_deny_total",
			Help: "Per-CIDR user-space dialer denial counter (PR-E, spec §11 + §12). Same (cidr, family) label set as egress_deny_total, but counts dialer refusals (oci.EgressDialContext returned ErrImageEgressDenied) rather than kernel-layer nftables drops. The two metrics together let an operator see whether a tenant's blocked pull hit the firewall first (egress_deny_total) or the user-space check (oci_egress_deny_total) — different levers.",
		}, []string{"cidr", "family"})
		commonCollectors = append(commonCollectors, ociEgressDeny)
		// M-1 / ADR-136 §Decision 2: per-layer-entry ownership clamp
		// counter. reason ∈ {out_of_range, unparseable_uid,
		// unparseable_gid}; bounded by the closed set so cardinality
		// is safe. pkg/rootfs.ApplyLayer calls this from
		// parseOwnershipInt for every uid/gid that falls outside the
		// [0, 65534] preserve-range (the cap keeps a customer image
		// from naming a uid that vmmd hands out to a guest — ADR-019).
		ownershipClamp = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_ownership_clamp_total",
			Help: "Layer entries whose declared uid/gid fell outside the preserved range (ADR-136 §Decision 2). reason ∈ {out_of_range, unparseable_uid, unparseable_gid}. The clamp is silent on the file itself — entries land under the imaged daemon uid/gid — but a non-zero rate is the Grafana tripwire for misbuilt base images.",
		}, []string{"reason"})
		commonCollectors = append(commonCollectors, ownershipClamp)
		// M-1: char/block/fifo entries dropped by applyEntry. The
		// counter is closed-labelled (no labels) — every increment
		// is the same shape. Tripwire for hostile or misbuilt
		// layers that ship device entries.
		layerEntrySkipped = prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_layer_entry_skipped_total",
			Help: "Layer entries dropped by applyEntry (char/block/fifo). A non-zero rate is a tripwire for hostile or misbuilt layers that ship device entries.",
		})
		commonCollectors = append(commonCollectors, layerEntrySkipped)
	}
	// issue #299: Grype scan findings, per (image, severity). The
	// `image` label is the OCI ref of the staged base ext4; the
	// `severity` label is the closed Grype {CRITICAL, HIGH,
	// MEDIUM, LOW, UNKNOWN} set. Incremented once per Grype scan
	// at imaged base-stage time. The CRITICAL count is the vmmd
	// admission gate's read side — see Manager.bringUpScanCheck at
	// pkg/fcvm/manager.go. Registered on every daemon because the
	// counter is shared via the single-registry pattern (memory
	// note wire/OpsMetrics), even though only vmmd / imaged
	// increment it in production.
	imageScanVulns := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_trivy_image_vulns_total",
		Help: "Per-image Grype finding counts, labelled by image (the OCI ref of the staged base ext4) and severity ∈ {CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN} (issue #299). The CRITICAL count is the vmmd admission gate's read side — a non-zero rate means vmmd refused to bring up an instance whose staged ext4 had a CRITICAL finding. The counter is incremented once per Grype scan at imaged base-stage time.",
	}, []string{"image", "severity"})
	commonCollectors = append(commonCollectors, imageScanVulns)
	// ADR-055: per-deploy grype scan metrics. Labelled by
	// severity (the Grype closed set) — the same vocabulary
	// imageScanVulns uses, so anyone familiar with the base-ext4
	// scan dashboards can read the per-deploy dashboards.
	// deployScanDuration uses the conventional latency buckets
	// (1s..600s) plus an explicit 300s SLA bucket so the SLO
	// burn is visible in a single Prom query.
	deployScanDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_deploy_scan_duration_seconds",
		Help:    "Per-deploy grype scan wall-clock duration in seconds (issue #464 / ADR-055). Measured from runDeployScan entry to the final sink write (complete or failed after the 1-retry backoff). The 300s SLO bucket makes the 5-min SLA burn visible without further PromQL.",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 180, 240, 300, 420, 600},
	}, []string{"app"})
	commonCollectors = append(commonCollectors, deployScanDuration)
	// ADR-117 §Production-ready follow-on: per-deploy stage
	// duration histogram, labelled by {stage, status}. The
	// closed-6 stage vocabulary mirrors pkg/state.AllStageNames;
	// status ∈ {completed, failed}. Pre-instantiated below so
	// every (stage, status) row surfaces in /metrics from boot
	// (the customer-facing panel needs zero-bucket observations
	// to render the p95/p99 panels without a "no data" state).
	// Only imaged increments via ObserveDeployStageDuration.
	deployStageDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_deploy_stage_duration_seconds",
		Help:    "Per-deploy stage wall-clock duration in seconds (ADR-117 §Production-ready follow-on). One observation per `transitionWithStage` write (pkg/imaged/handler.go). stage ∈ {source_download, dependency_restore, image_build, security_scan, snapshot_prepare, readiness}; status ∈ {completed, failed}. Buckets skew to the long tail (300s) so a stalled image_build surfaces as a top-bucket observation rather than +Inf.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"stage", "status"})
	commonCollectors = append(commonCollectors, deployStageDuration)
	for _, stage := range []string{"source_download", "dependency_restore", "image_build", "security_scan", "snapshot_prepare", "readiness"} {
		for _, status := range []string{"completed", "failed"} {
			deployStageDuration.WithLabelValues(stage, status)
		}
	}
	deployScanTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_deploy_scan_total",
		Help: "Per-deploy grype scan outcomes, labelled by result ∈ {complete, failed, skipped} (issue #464 / ADR-055). One increment per deploy after the 1-retry backoff; skipped comes from the pre-feature backfill or a feature-flag-off imaged build.",
	}, []string{"app", "result"})
	commonCollectors = append(commonCollectors, deployScanTotal)
	deployScanVulns := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_deploy_scan_vulns_total",
		Help: "Per-deploy grype CVE finding counts, labelled by severity ∈ {CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN} (issue #464 / ADR-055). Mirrors the existing base-ext4 _trivy_image_vulns_total semantic but on the per-deploy key. The CRITICAL row is the per-deploy equivalent of the vmmd admission gate's read side.",
	}, []string{"app", "severity"})
	commonCollectors = append(commonCollectors, deployScanVulns)
	// ADR-066 / Tier A5: live-instance migration decision counter.
	// Labelled by outcome ∈ {migrated, conflict, no_headroom,
	// no_eligibility, lease_expired, peer_failure}. The migrated
	// label is the §12 dashboard panel (sum over 5m); pairs with
	// rebalanceDecisions to distinguish PARKED-app rebalance from
	// LIVE-instance migration. Pre-instantiated at boot so the row
	// surfaces in /metrics from the moment schedd starts. Only
	// schedd increments via ObserveLiveMigration in production, but
	// the field is on the shared struct (single-registry pattern,
	// memory wire/OpsMetrics) so /metrics doesn't show a 404 for
	// cmd/<other> scrapes that incidentally probe the prefix.
	liveMigrationDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_live_migration_decisions_total",
		Help: "Cross-node LIVE-instance migration decisions (Tier A5 / ADR-066), labelled by outcome ∈ {migrated, conflict, no_headroom, no_eligibility, lease_expired, peer_failure}. `migrated` is the §12 dashboard panel. `lease_expired` is the tripwire for the four-phase handoff timing out; `peer_failure` is the tripwire for the new-owner vmmd failing the AdoptMigratedInstance RPC. Single-registry: registered on every daemon (mirrors rebalanceDecisions); only schedd increments via ObserveLiveMigration.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, liveMigrationDecisions)
	// ADR-096: customer-facing automatic error grouping ingest
	// counter. Labelled by outcome ∈ {ok, redaction_failed,
	// rate_limited, db_error}. `ok` is the §12 customer-error-
	// ingest panel (rate over 5m); `redaction_failed` is the
	// tripwire for pkg/redact panicking — must stay at 0.
	// `rate_limited` is the LRU-cardinality backstop firing
	// (CardinalityLimit hit). `db_error` is the publisher's
	// 5th-consecutive-failure drop. Single-registry: registered
	// on every daemon (mirrors liveMigrationDecisions); only
	// gatewayd-internal + apid increment via
	// ObserveAppErrorsRecorded.
	appErrorsRecorded := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_app_errors_recorded_total",
		Help: "Customer-facing automatic error grouping ingest outcomes (ADR-096), labelled by outcome ∈ {ok, redaction_failed, rate_limited, db_error}. `ok` is the §12 customer-error-ingest panel (rate over 5m). `redaction_failed` is the tripwire for pkg/redact panicking — MUST stay at 0. `rate_limited` is the LRU-cardinality backstop firing when an app exceeds CardinalityLimit fingerprints. `db_error` is the publisher's per-row drop signal — incremented for EVERY row of a failed flush batch (the batch is drained before flushBatch is called, so failures always lose data; per-row observe gives the §12 panel an accurate outage timeline rather than only the 5th-consecutive-failure tripwire). Single-registry: registered on every daemon; only gatewayd-internal + apid increment via ObserveAppErrorsRecorded.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, appErrorsRecorded)
	// ADR-127 PR-B: production debugger ingest outcomes.
	// outcome ∈ {inserted, rate_limited, db_error}. `inserted`
	// is the customer-telemetry-ingest panel; `rate_limited` is
	// the per-account token bucket overflow; `db_error` is the
	// per-row INSERT failure path. Single-registry: registered on
	// every daemon; only apid increments via
	// IncrementRequestTelemetryRecorded.
	requestTelemetryRecorded := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_request_telemetry_recorded_total",
		Help: "Production debugger per-request telemetry ingest outcomes (ADR-127 PR-B), labelled by outcome ∈ {inserted, rate_limited, db_error}. `inserted` is the customer-telemetry-ingest panel (rate over 5m). `rate_limited` is the per-account token-bucket overflow path (DebugTelemetryRequestsPerMinute exceeded). `db_error` is the per-row INSERT failure path. Single-registry: registered on every daemon; only apid increments via IncrementRequestTelemetryRecorded.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, requestTelemetryRecorded)
	// ADR-127 PR-D: gatewayd-public OTel spans writer ingest
	// outcomes. outcome ∈ {inserted, rate_limited, db_error,
	// shape_invalid}. `inserted` is the customer-telemetry-spans
	// panel; `rate_limited` is the per-account token-bucket
	// overflow (DebugTelemetryRequestsPerMinute exceeded);
	// `db_error` is the apid WriteSpansSummary RPC failure;
	// `shape_invalid` is the OTLP body parser rejection
	// (malformed JSON, missing trace_id, etc). Single-registry:
	// registered on every daemon; only gatewayd-public
	// increments via IncrementGatewaydPublicOtelSpansIngested.
	gatewaydPublicOtelSpansIngested := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_gatewayd_public_otel_spans_ingested_total",
		Help: "OTel spans writer ingest outcomes on the gatewayd-public POST /v1/otel/v1/traces handler (ADR-127 PR-D), labelled by outcome ∈ {inserted, rate_limited, db_error, shape_invalid}. `inserted` is the customer-telemetry-spans panel (rate over 5m). `rate_limited` is the per-account token-bucket overflow path (DebugTelemetryRequestsPerMinute exceeded). `db_error` is the apid WriteSpansSummary RPC failure path. `shape_invalid` is the OTLP body parser rejection (malformed JSON, missing trace_id, etc). Single-registry: registered on every daemon; only gatewayd-public increments via IncrementGatewaydPublicOtelSpansIngested.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, gatewaydPublicOtelSpansIngested)
	// ADR-127 PR-D: truncation tripwire. Fires once per OTLP
	// POST when the inbound body has more spans than the
	// plan's DebugTelemetrySpansPerTrace ceiling. Unlabelled —
	// single-registry pattern; only gatewayd-public increments
	// via IncrementGatewaydPublicOtelSpansTruncated.
	gatewaydPublicOtelSpansTruncated := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_gatewayd_public_otel_spans_truncated_total",
		Help: "OTel spans writer truncation tripwire on the gatewayd-public POST /v1/otel/v1/traces handler (ADR-127 PR-D). Fires once per OTLP POST when the inbound body has more spans than the plan's DebugTelemetrySpansPerTrace ceiling (Hobby=50/Pro=200/Scale=1000). Sustained non-zero means the customer's app is chatty enough that the slowest-N selection is dropping signal — alertable via SpansTruncationRateSlo. Unlabelled — single-registry pattern; only gatewayd-public increments via IncrementGatewaydPublicOtelSpansTruncated.",
	})
	commonCollectors = append(commonCollectors, gatewaydPublicOtelSpansTruncated)
	// ADR-127 PR-D: OTel auth failures (apid AuthenticateKey
	// RPC rejections). reason ∈ {unauthenticated, plan_disabled,
	// internal}. Single-registry: registered on every daemon;
	// only gatewayd-public increments via
	// IncrementGatewaydPublicOtelAuthFailures.
	gatewaydPublicOtelAuthFailures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_gatewayd_public_otel_auth_failures_total",
		Help: "OTel spans writer auth failures on the gatewayd-public POST /v1/otel/v1/traces handler (ADR-127 PR-D), labelled by reason ∈ {unauthenticated, plan_disabled, internal}. `unauthenticated` is the apid AuthenticateKey RPC rejecting the bearer token (hash miss, expired, revoked). `plan_disabled` is the customer's plan not including DebugTelemetryEnabled. `internal` is the apid RPC error (Postgres down, etc). Single-registry: registered on every daemon; only gatewayd-public increments via IncrementGatewaydPublicOtelAuthFailures.",
	}, []string{"reason"})
	commonCollectors = append(commonCollectors, gatewaydPublicOtelAuthFailures)
	// ADR-127 PR-D: apid-side SpansWriter RPC outcomes. outcome
	// ∈ {inserted, rate_limited, db_error}. Single-registry:
	// registered on every daemon; only apid increments via
	// IncrementSpansWriteOutcome.
	spansWriteOutcomes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_otel_spans_writes_total",
		Help: "Apid-side spans_writer RPC outcomes (ADR-127 PR-D), labelled by outcome ∈ {inserted, rate_limited, validation_error, db_error}. `inserted` is the successful UPDATE on request_telemetry.spans_summary. `rate_limited` is the per-account token-bucket overflow on the write path. `validation_error` is a client-side rejection (bad trace_id regex, invalid JSON, malformed account_id, DB CHECK violation) — PR-D code-review #7 split this from `db_error` so dashboards distinguish. `db_error` is a real Postgres failure (connection trip, transaction rollback). Single-registry: registered on every daemon; only apid increments via IncrementSpansWriteOutcome.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, spansWriteOutcomes)
	// ADR-127 PR-B: regression cron tick gauge.
	// Single-registry: registered on every daemon; only apid
	// increments via DebugRegressionOldestPassSeconds.
	debugRegressionOldestPassSeconds := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_debug_regression_oldest_pass_seconds",
		Help: "Seconds elapsed since the most-recent debug_regression_observations row was refreshed by the regression cron (cmd/apid/debug_regression_cron.go, ADR-127 PR-B). Zero means the loop just ran against an empty table. Large values mean the cron is stalled. Backs FaasDebugRegressionStalled.",
	})
	commonCollectors = append(commonCollectors, debugRegressionOldestPassSeconds)
	// ADR-127 PR-B: regression cron skip counter.
	// Single-registry: registered on every daemon; only apid
	// increments via DebugRegressionSkippedFlagDisabled.
	debugRegressionSkippedFlagDisabled := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_debug_regression_skipped_flag_disabled_total",
		Help: "Regression cron passes skipped because the operator flipped FAAS_DEBUG_TELEMETRY_ENABLED=false OR DebugTelemetryEnabled was off for every enumerated account (cmd/apid/debug_regression_cron.go, ADR-127 PR-B). Unlabelled — single-registry pattern, only apid increments.",
	})
	commonCollectors = append(commonCollectors, debugRegressionSkippedFlagDisabled)
	// ADR-127 Debugger UX v1: regression upsert counter.
	// Single-registry, unlabelled — only apid increments via
	// DebugRegressionDetected. Bumped on every successful
	// UpsertRegressionObservation (PRIMARY KEY (app_id,
	// deployment_id, route) upsert). A new regression that
	// crossed debugRegressionMinAffected for the first time
	// fires the counter once; a regression that persists across
	// cron passes re-fires every 5m. PagerDuty dedupes a
	// sustained-fire alert into one ongoing incident. Backs
	// FaasDebugRegressionDetected.
	debugRegressionDetected := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_debug_regression_detected_total",
		Help: "debug_regression_observations PRIMARY KEY upserts persisted by the regression cron (cmd/apid/debug_regression_cron.go, ADR-127 Debugger UX v1). Each successful UpsertRegressionObservation increments the counter; an existing regression that persists across cron passes re-fires every 5m. Backs FaasDebugRegressionDetected.",
	})
	commonCollectors = append(commonCollectors, debugRegressionDetected)
	// ADR-096: in-process LRU fingerprint cache hit counter.
	// Unlabelled (no cardinality risk). Single-registry:
	// registered on every daemon; only gatewayd-internal
	// increments via ObserveAppErrorsFingerprintCacheHit.
	appErrorsFingerprintCacheHits := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_app_errors_fingerprint_cache_hits_total",
		Help: "In-process LRU fingerprint cache hits in the gatewayd-internal error recorder (ADR-096). Hit rate over 5m divided by requestFailures is the §12 fingerprint-cache-effectiveness panel; sustained <80% means the LRU is too small for the customer's traffic shape. No labels; no cardinality risk. Single-registry: registered on every daemon; only gatewayd-internal increments via ObserveAppErrorsFingerprintCacheHit.",
	})
	commonCollectors = append(commonCollectors, appErrorsFingerprintCacheHits)
	// ADR-096: server-side dedupe-merge counter. Fires once per
	// ON CONFLICT DO UPDATE that bumps an existing row's count
	// + last_seen_at within AppErrorsDedupeWindowSeconds.
	// Unlabelled. Single-registry: registered on every daemon;
	// only apid increments via ObserveAppErrorsDedupeMerge.
	appErrorsDedupeMerges := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_app_errors_dedupe_merges_total",
		Help: "Server-side dedupe-merge hits in the apid gRPC handler (ADR-096). Increments once per ON CONFLICT (account_id, app_id, fingerprint) DO UPDATE that bumps an existing row's count + last_seen_at within AppErrorsDedupeWindowSeconds. Merge rate vs new-fingerprint insert rate is the §12 dedupe-effectiveness panel. No labels. Single-registry: registered on every daemon; only apid increments via ObserveAppErrorsDedupeMerge.",
	})
	commonCollectors = append(commonCollectors, appErrorsDedupeMerges)
	// ADR-096: per-flush wall-clock duration histogram for the
	// gatewayd-internal publisher. Unlabelled. Single-registry:
	// registered on every daemon; only gatewayd-internal
	// increments via ObserveAppErrorsFlushDuration.
	appErrorsFlushDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    prefix + "_app_errors_flush_duration_seconds",
		Help:    "Per-flush wall-clock duration in the gatewayd-internal app_errors publisher (ADR-096). One observation per drain of the ringbuffer (FlushInterval or FlushBatchSize, whichever first). Bucket set {0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}: the 1ms bucket catches the empty-drain case (FlushInterval fires with nothing to do); the 5s bucket catches a stuck DB connection (alertable via FlushDurationP99Slo). No labels. Single-registry: registered on every daemon; only gatewayd-internal increments via ObserveAppErrorsFlushDuration.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
	})
	commonCollectors = append(commonCollectors, appErrorsFlushDuration)
	// ADR-096: retention cron outcome counter (apid-side).
	// Labelled by outcome ∈ {ok, no_accounts, failed}. `ok` is
	// the §12 retention-enforcement panel (rate over 24h, MUST
	// be >0 once an account iterator lands); `no_accounts` is
	// the signal that the cron ran but had no accounts to walk
	// (PR-A ships with no iterator wired, so this fires every
	// 24h until PR-B); `failed` is the tripwire for a SQL-level
	// failure (alertable). Pre-instantiated at boot so the row
	// surfaces in /metrics from the moment apid starts (single-
	// registry rule).
	appErrorsPurges := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_app_errors_purges_total",
		Help: "Retention cron outcomes for the customer-facing error grouping store (ADR-096), labelled by outcome ∈ {ok, no_accounts, failed}. `ok` is the §12 retention-enforcement panel (rate over 24h, MUST be >0 once an account iterator lands — Free 1d / Hobby 7d / Pro 30d / Scale 90d ceiling). `no_accounts` is the signal that the cron ran but had no accounts to walk (PR-A ships with no iterator wired, so this fires every 24h until PR-B lands the account iterator alongside the reader-path handlers). `failed` is the tripwire for a SQL-level failure (alertable). Single-registry: registered on every daemon; only apid increments via ObserveAppErrorsPurge.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, appErrorsPurges)

	// --- ADR-098 connection-aware execution (PR-C) -----------
	//
	// Three new metrics for the meterd probe loop. Labels
	// carry ONLY the host_redacted_hash (8-char prefix from
	// logsanitize.HashShort) — the plaintext host NEVER
	// appears in any label. The metric set is the §12
	// "data placement" panel; schedd's chooser bias (PR-D)
	// also reads from the same data_upstream_probes table
	// rather than scraping these metrics.
	dataUpstreamRTT := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_data_upstream_rtt_ms",
		Help:    "Observed RTT (ms) bucketed per (kind, host_redacted_hash, region). Buckets cover 1ms..3s — a healthy TLS handshake completes in <100ms; the 1s+ tail catches TCP retries. The label set is closed: kind ∈ {postgres, redis, mongo, ...}; host_redacted_hash is the 8-hex-char prefix of sha256(salt||host); region is the meterd node's compute_nodes.region. The plaintext host is NEVER in the labels (ADR-098 §11). §12 data-placement dashboard panel.",
		Buckets: []float64{1, 5, 10, 50, 100, 500, 1000, 3000},
	}, []string{"kind", "host_redacted_hash", "region"})
	dataUpstreamProbes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_data_upstream_probes_total",
		Help: "Probe outcomes (TCP+TLS via crypto/tls.Dial). Closed-set labels: outcome ∈ {ok, timeout, refused, tls_handshake, dns, unreachable}. §12 data-placement panel; pre-instantiated so the rows surface from a fresh process.",
	}, []string{"outcome"})
	dataUpstreamProbeDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    prefix + "_data_upstream_probe_duration_seconds",
		Help:    "Per-probe wall-clock duration (TCP+TLS handshake). Buckets cover the 1ms..3s range — a healthy TLS handshake completes in <100ms; the 1s+ tail catches TCP retries. §12 data-placement panel.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 3},
	})
	commonCollectors = append(commonCollectors, dataUpstreamRTT, dataUpstreamProbes, dataUpstreamProbeDuration)
	for _, o := range []string{"ok", "timeout", "refused", "tls_handshake", "dns", "unreachable"} {
		dataUpstreamProbes.WithLabelValues(o)
	}
	// Pre-instantiate the outcome set so /metrics surfaces the
	// rows from a fresh process (Prometheus skips zero-valued
	// CounterVec series by default). Extending the outcome
	// vocabulary requires extending this loop in lock-step with
	// the cmd/apid/app_errors_purge.go::Run dispatch.
	for _, outcome := range []string{"ok", "no_accounts", "failed"} {
		appErrorsPurges.WithLabelValues(outcome)
	}
	// ADR-095 PR-C: preview teardown janitor outcomes. Mirror of
	// appErrorsPurges for the preview-row sweep. The closed-set
	// outcome vocabulary keeps cardinality bounded per
	// ObserveAppErrorsPurge convention.
	previewJanitorOutcomes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_preview_janitor_outcomes_total",
		Help: "Preview teardown cron outcomes (ADR-095 PR-C, issue #272), labelled by outcome ∈ {ok, failed, torn_down}. `ok` fires every tick the sweep ran cleanly (whether or not it tombstoned a row); `torn_down` fires per-row when a preview app reaches torn_down and is soft-deleted (the §12 teardown-rate panel sums this over 1h to chart PR-close cadence); `failed` is the tripwire for a SQL-level failure (alertable). Single-registry: registered on every daemon; only apid increments via ObservePreviewJanitor.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, previewJanitorOutcomes)
	for _, outcome := range []string{"ok", "failed", "torn_down"} {
		previewJanitorOutcomes.WithLabelValues(outcome)
	}
	// ADR-067 / Tier A6: migrating-instance watchdog reconcile counter.
	// Labelled by outcome ∈ {reinvited, hard_deleted, conflict, error}.
	// The reinvited label is the §12 dashboard panel (sum over 5m for
	// the rate of "new owner vmmd gracefully re-acked the in-flight
	// lease"); the hard_deleted label is the tripwire for the new-owner
	// vmmd persistently dying mid-handoff (operators must inspect
	// `events` kind='migration_reconciled' for the failing node).
	// Pre-instantiated at boot so the row surfaces in /metrics from
	// the moment schedd starts. Only schedd increments via
	// ObserveMigratingReconcile in production, but the field is on the
	// shared struct (single-registry pattern, memory wire/OpsMetrics)
	// so /metrics doesn't show a 404 for cmd/<other> scrapes that
	// incidentally probe the prefix.
	migratingReconcileDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_migrating_reconcile_total",
		Help: "Cross-node migrating-instance watchdog reconcile decisions (Tier A6 / ADR-067), labelled by outcome ∈ {reinvited, hard_deleted, conflict, error}. `reinvited` is the §12 dashboard panel (active owner re-acked the same lease); `hard_deleted` is the tripwire for the new-owner vmmd dying mid-handoff. Single-registry: registered on every daemon (mirrors rebalanceDecisions / liveMigrationDecisions); only schedd increments via ObserveMigratingReconcile.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, migratingReconcileDecisions)
	// ADR-083 / Tier A8: active-passive HA fail-over counter.
	// Labelled by outcome ∈ {dns_flipped, dns_stale,
	// peer_unreachable, manual_drain}. dns_flipped is the §12
	// dashboard panel (the active-passive flip succeeded inside
	// HADNSRecordStaleSeconds). Pre-instantiated at boot so the
	// row surfaces in /metrics from the moment gatewayd-public
	// starts. Only gatewayd-public increments in production, but
	// the field is on the shared struct (single-registry pattern,
	// memory wire/OpsMetrics) so /metrics doesn't show a 404 for
	// cmd/<other> scrapes that incidentally probe the prefix.
	activePassiveFailoversTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_active_passive_failovers_total",
		Help: "Active-passive HA fail-over decisions (Tier A8 / ADR-083), labelled by outcome ∈ {dns_flipped, dns_stale, peer_unreachable, manual_drain}. `dns_flipped` is the §12 dashboard panel (the active-passive flip succeeded inside HADNSRecordStaleSeconds); `dns_stale` is the tripwire for the DNS provider failing UpsertRecord after retries (operationally distinct from `peer_unreachable` which is the pg_notify consumer falling behind); `manual_drain` is the operator-initiated path via the runbook. Single-registry: registered on every daemon (mirrors rebalanceDecisions / liveMigrationDecisions / migratingReconcileDecisions); only gatewayd-public increments via ActivePassiveFailovers.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, activePassiveFailoversTotal)
	// ADR-083 / Tier A8: standby-state enum gauge. Unlabelled
	// gauge held at the current StandbyState value (1=warming,
	// 2=warm, 3=draining). Pre-instantiated to 1 so the row
	// surfaces in /metrics from the moment the daemon starts
	// (precedent: alertEvaluatorEnabled above). The
	// `FaasStandbyStateWarmingTooLong` alert rule queries this
	// gauge (deploy/ansible/roles/prometheus/files/ha_failover.rules.yml).
	standbyState := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_standby_state",
		Help: "Current standby state for this gatewayd-public (Tier A8 / ADR-083 / §14 M8). 1=warming (boot warm-up incomplete), 2=warm (active or standby-warm), 3=draining (operator-initiated drain in flight, in-flight requests bounded by HADNSRecordStaleSeconds), 4=drained (terminal success — DNS flipped, in-flight reached zero, box is safe to shut down), 5=failed (terminal failure — DNS provider exhausted retries OR peer_unreachable stuck; operator intervention required). The FaasStandbyStateWarmingTooLong alert fires when this gauge holds at 1 for > 60s on a node where compute_nodes.active=true. Unlabelled — single-box per-node state.",
	})
	standbyState.Set(StandbyStateWarming)
	commonCollectors = append(commonCollectors, standbyState)
	// Dead-node billing reconciler counter. Single-registry pattern
	// (mirrors migratingReconcileDecisions): registered on every
	// daemon, only schedd increments it in production.
	deadNodeReconcileDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_dead_node_reconcile_total",
		Help: "Dead-node billing reconciler decisions, labelled by outcome ∈ {failed, conflict, error}. `failed` counts RUNNING instances terminated because their compute_node stopped heartbeating past the staleness window — each one was billing the customer for a VM that no longer existed and holding §6.2-2 RAM ceiling. A sustained non-zero `failed` rate is an incident signal (a vmmd is dying without transitioning its rows), not routine repair. `conflict` is the benign peer-wins/node-recovered path.",
	}, []string{"outcome"})
	commonCollectors = append(commonCollectors, deadNodeReconcileDecisions)
	// ADR-087 / Tier A9: pressure-rebalancer decision counter.
	// Single-registry: registered on every daemon (mirrors
	// deadNodeReconcileDecisions); only schedd increments via
	// PressureReassignments in production.
	commonCollectors = append(commonCollectors, pressureReassignmentsTotal)
	// ADR-088 / Tier A10: per-app overflow_node preference
	// observability. Single-registry: registered on every
	// daemon; only schedd increments via OverflowTargetSpillHits
	// in production.
	commonCollectors = append(commonCollectors, overflowTargetSpillHitsTotal)
	// ADR-087 / Tier A9: per-(app, kind) AtCapacity trigger counter.
	// Single-registry: registered on every daemon (mirrors
	// deadNodeReconcileDecisions); only schedd increments via
	// AppAtCapacityTotal in production.
	commonCollectors = append(commonCollectors, appAtCapacityTotal)
	// ADR-062 / issue #461: registry-credential mark-used failure
	// counter. Unlabelled Counter (no cardinality risk); pre-
	// instantiated at boot so the row surfaces in /metrics from
	// the moment imaged starts. Only imaged increments it in
	// production, but the field is on the shared struct (single-
	// registry pattern, memory wire/OpsMetrics).
	registryCredentialMarkUsedFailures := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_registry_credential_mark_used_failures_total",
		Help: "Count of imaged's store.MarkAppRegistryCredentialUsed failures after a successful authenticated pull (ADR-062, issue #461). The deployment itself succeeds — mark-used is intentionally non-fatal per ADR-062 §Decision 8 — but a persistent non-zero rate means `last_used_at` is lagging reality and rotation heuristics may evict still-in-use credentials. No labels; failure shape is closed (DB write refused, row vanished, transient connection drop).",
	})
	commonCollectors = append(commonCollectors, registryCredentialMarkUsedFailures)
	// ADR-054 acceptance: stale-cache fallback counter. Unlabelled
	// (deployment-level policy; closed-set cardinality). Pre-
	// instantiated at boot so the row surfaces in /metrics from the
	// moment the daemon starts. Only backends with the read-through
	// cache wrapped emit on this counter; on a single-box local
	// install the counter stays at zero forever.
	storageCacheStaleFallback := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_storage_cache_stale_fallback_total",
		Help: "Count of LocalCacheBackend stale-cache fallback serves (ADR-054 acceptance). Fires once per Get that returned a last-known-good cached blob because the parent backend failed AND FAAS_STORAGE_CACHE_SERVE_STALE=true. No labels (deployment-level policy; per-key labels would explode cardinality). A non-zero rate means 'registry is down' — alertable via the §12 storage panel.",
	})
	commonCollectors = append(commonCollectors, storageCacheStaleFallback)
	// ADR-097 (P1B): schedd-side wake RPC duration histogram. Same
	// MustRegister pattern as the legacy wakePhaseDur at line 1998
	// (events-platform ADR-064) but a distinct metric name and label
	// set — see metrics.go:1208-1227 for the naming rationale.
	commonCollectors = append(commonCollectors, wakeRPCDuration)
	// Issue #587 / PR-A: per-daemon graceful-shutdown drain
	// observability. Two series with a closed label set:
	//
	//   * gatewayDrainWaitSeconds histogram {daemon, outcome} —
	//     wall-clock seconds the drain waited before every
	//     in-flight request goroutine finished (or the deadline
	//     fired). outcome ∈ {clean, deadline_exceeded, ctx_cancelled}
	//     so an operator can tell "drained fast" from "we
	//     force-cut requests" without re-reading the daemon log.
	//     Bucket set covers <100ms (idle) up to the full
	//     DrainGrace=25s ceiling.
	//
	//   * gatewayInflightRequests gauge {daemon, op} — current
	//     in-flight Begin() count. op ∈ {http, upgrade, control}
	//     (NO plan or app label — Prometheus cardinality
	//     discipline per the cluster plan's "Decisions baked in"
	//     §2). Operators who want per-plan or per-app in-flight
	//     counts can add them later if a real incident needs them.
	//
	// Both metrics are pre-instantiated for the closed label
	// cross-product so /metrics surfaces rows from boot —
	// matches the pre-instantiation pattern used by every other
	// per-daemon collector (see e.g. writeRedirectTotal at
	// metrics.go:1136-1142).
	gatewayDrainWaitSeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prefix + "_drain_wait_seconds",
		Help:    "Wall-clock seconds the graceful-shutdown drain (issue #587 / PR-A / pkg/gateway/drain) waited before every in-flight request goroutine finished. Labelled by {daemon, outcome}; outcome ∈ {clean, deadline_exceeded, ctx_cancelled} so an operator can tell a fast clean drain from a forced one without re-reading the daemon log. Bucket set covers <100ms idle drain up to the full DrainGrace=25s ceiling.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 20, 25},
	}, []string{"daemon", "outcome"})
	gatewayDrainWaitSeconds.WithLabelValues("gatewayd-internal", "clean")
	gatewayDrainWaitSeconds.WithLabelValues("gatewayd-internal", "deadline_exceeded")
	gatewayDrainWaitSeconds.WithLabelValues("gatewayd-internal", "ctx_cancelled")
	gatewayDrainWaitSeconds.WithLabelValues("gatewayd-public", "clean")
	gatewayDrainWaitSeconds.WithLabelValues("gatewayd-public", "deadline_exceeded")
	gatewayDrainWaitSeconds.WithLabelValues("gatewayd-public", "ctx_cancelled")
	gatewayInflightRequests := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_inflight_requests",
		Help: "Current in-flight request count tracked by the per-daemon drain.Tracker (issue #587 / PR-A / pkg/gateway/drain). Labelled by {daemon, op}; op ∈ {http, upgrade, control}. NO plan or app label — Prometheus cardinality discipline (per the cluster plan's 'Decisions baked in' §2).",
	}, []string{"daemon", "op"})
	gatewayInflightRequests.WithLabelValues("gatewayd-internal", "http")
	gatewayInflightRequests.WithLabelValues("gatewayd-internal", "upgrade")
	gatewayInflightRequests.WithLabelValues("gatewayd-internal", "control")
	gatewayInflightRequests.WithLabelValues("gatewayd-public", "http")
	gatewayInflightRequests.WithLabelValues("gatewayd-public", "upgrade")
	gatewayInflightRequests.WithLabelValues("gatewayd-public", "control")
	commonCollectors = append(commonCollectors, gatewayDrainWaitSeconds, gatewayInflightRequests)
	// Issue #757 / ADR-118 commit 9: ESM metric collectors. All
	// three are pre-instantiated at boot from the closed sets
	// below so the rows surface in /metrics from process start —
	// same precedent as rebalanceDecisions + appAtCapacityTotal
	// (Tier A4 / ADR-087).
	//
	// source values are the closed trigger-kind vocabulary
	// (see pkg/events/trigger.go + pkg/gregalemanifest).
	esmSourceClosedSet := []string{
		"kafka", "nats", "redis_streams", "sqs_compat", "queue", "cron",
	}
	esmOutcomeClosedSet := []string{"success", "empty", "error"}
	esmPollsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_esm_polls_total",
		Help: "Count of broker poll cycles the dispatcher ran, labelled by source and outcome ∈ {success, empty, error}. Single-registry: registered on every daemon (mirrors rebalanceDecisions); only schedd increments via ObserveESMPoll. The rate per source is the §12 broker-poll-health panel; a sustained `outcome=\"error\"` rate per source is the operator-debug tripwire for that broker having connectivity issues.",
	}, []string{"source", "outcome"})
	esmRecordsConsumedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_esm_records_consumed_total",
		Help: "Count of broker records the dispatcher accepted past closeBatch, labelled by source. Records that the filter rejected (commit 6) do NOT increment this counter — the byte cost lands in broker_egress (commit 8) but the record was never consumed. Single-registry: registered on every daemon; only schedd increments via ObserveESMRecords. The rate per source is the §12 broker-throughput panel.",
	}, []string{"source"})
	esmLagSeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_esm_lag_seconds",
		Help: "Per-record lag — seconds between ReceivedAt and the dispatch tick — labelled by source and shard. Buckets skewed for the broker envelope (Kafka commit latency + per-partition fetch). Single-registry: registered on every daemon; only schedd increments via ObserveESMLag. shard is bounded by a 32-bucket cap with `_agg` overflow (ADR-118 §Cardinality discipline); the panel selector is `histogram_quantile(0.99, sum by (source, le) (rate(schedd_esm_lag_seconds_bucket[5m])))`.",
		Buckets: []float64{
			0.001, 0.005, 0.025, 0.1, 0.25,
			1.0, 2.5, 5.0, 10.0, 30.0,
		},
	}, []string{"source", "shard"})
	for _, source := range esmSourceClosedSet {
		for _, outcome := range esmOutcomeClosedSet {
			esmPollsTotal.WithLabelValues(source, outcome)
		}
		esmRecordsConsumedTotal.WithLabelValues(source)
		// Pre-instantiate the `_agg` bucket so the histogram
		// surfaces in /metrics from boot. The 32-bucket cap
		// lives in dispatch_triggers.go (commit 9 wiring).
		esmLagSeconds.WithLabelValues(source, "_agg")
	}
	commonCollectors = append(commonCollectors, esmPollsTotal, esmRecordsConsumedTotal, esmLagSeconds)

	// PR-#TBD / C5 — operator-action observability layer
	// (PR #1106 P2d follow-on). Four new series feed the
	// /v1/admin/obs/health endpoint (C7). Closed label sets
	// pre-instantiated below so /metrics surfaces zero from
	// boot.
	//
	// auditLogWriteTotal / auditLogWriteFailuresTotal share
	// the same endpoint × kind grid. The grid is bounded at
	// 4 endpoints × 7 kinds × {success, [error_class]} = a
	// small constant. kind is the closed operator-action
	// vocabulary: 3 verbs × {request, outcome} = 6 plus
	// "other" overflow. The emit path routes non-operator
	// kinds (cron, wake, deployment, etc.) to "other" so a
	// typo in audit.emit() callers cannot blow up
	// cardinality — the audit-log layer's bounded admission
	// set already enforces this in spirit.
	//
	// Closed label sets are declared locally so they sit next
	// to the constructors that consume them. Extending these
	// slices also extends the pre-instantiation grid (no
	// separate label-value to keep in sync).
	auditEndpointClosedSet := []string{"apid", "schedd", "meterd", "gatewayd-internal"}
	auditKindClosedSet := []string{
		"force_park",
		"force_cold_boot",
		"force_restart",
		"force_park.outcome",
		"force_cold_boot.outcome",
		"force_restart.outcome",
		// apid request-side instance-oriented aliases
		// (pkg/audit.auditKindMetricLabel maps them onto the
		// verb-oriented labels above). The auditLogWriteTotal
		// + auditLogWriteFailuresTotal counters are
		// pre-instantiated for both shapes so /obs/health's
		// join doesn't break on first emit.
		"park_instance",
		"park_instance.outcome",
		"restart_instance",
		"restart_instance.outcome",
		"other",
	}
	auditErrorClassClosedSet := []string{
		"sqlstate_23514", // check_violation (events.trace_id regex at 00486)
		"sqlstate_23505", // unique_violation
		"timeout",
		"other",
	}
	// Shared by the operatorIntentOutcomeMissingTotal counter
	// and the operatorActionTraceCompletenessRatio gauge. The
	// gauge's kind-grouped query (pkg/sched.operator_intent_
	// completeness.go::observeTraceCompletenessRatio) strips
	// the "operator.action." prefix from raw audit kinds, so
	// the gauge reads BOTH the verb-oriented forms (schedd
	// outcome emits) AND the instance-oriented forms (apid
	// request emits) — pre-instantiating both keeps the gauge
	// grid complete. The counter pre-instantiates both too
	// (the closed-set semantics keep cardinality bounded; the
	// counter just stays at 0 for the instance-oriented forms
	// since schedd never writes those — that's intentional).
	operatorIntentKindClosedSet := []string{
		"force_park",
		"force_cold_boot",
		"force_restart",
		"park_instance",
		"restart_instance",
	}
	auditLogWriteTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_audit_log_write_total",
		Help: "Count of events-table appends the audit emit path completed, labelled by endpoint and kind. /v1/admin/obs/health reads this via PromQL `sum(increase(audit_log_write_total[5m]))` to report 5-minute throughput. Single-registry: registered on every daemon; only the audit emit site (pkg/audit.Auditor.Emit / EmitResult) increments via AuditLogWriteTotal.",
	}, []string{"endpoint", "kind"})
	auditLogWriteFailuresTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_audit_log_write_failures_total",
		Help: "Count of events-table appends that failed, labelled by endpoint, kind, and error_class ∈ {sqlstate_23514, sqlstate_23505, timeout, other}. The audit emit path surfaces SQLSTATE 23514 (check_violation — the regex on events.trace_id at 00486) and 23505 (unique_violation) as labelled buckets; everything else collapses to 'other'. /v1/admin/obs/health reports the failure rate vs audit_log_write_total — a sustained non-zero failure rate implies the events table is degraded.",
	}, []string{"endpoint", "kind", "error_class"})
	operatorActionTraceCompletenessRatio := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prefix + "_operator_action_trace_completeness_ratio",
		Help: "5-minute trailing ratio (0.0..1.0) of operator.action.<verb>* audit rows whose events.trace_id column is non-NULL. Labelled by kind ∈ {force_park, force_cold_boot, force_restart, force_park.outcome, force_cold_boot.outcome, force_restart.outcome}. Single-registry: registered on every daemon; only schedd sets the value via SetOperatorActionTraceCompleteness (60s tick). A drop below 0.95 is the obs-coverage alert tripwire — every force-action should carry a trace_id end-to-end (PR-#TBD C1-C4 contract).",
	}, []string{"kind"})
	operatorActionTraceCompletenessFirstTickCompleted := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_operator_action_trace_completeness_first_tick_completed_total",
		Help: "Whether schedd has completed at least one successful operator-action trace completeness query; increments once after the first successful observation.",
	})
	operatorActionTraceCompletenessLastSuccessTimestamp := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_operator_action_trace_completeness_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful operator-action trace completeness query.",
	})
	for _, endpoint := range auditEndpointClosedSet {
		for _, kind := range auditKindClosedSet {
			auditLogWriteTotal.WithLabelValues(endpoint, kind)
			for _, ec := range auditErrorClassClosedSet {
				auditLogWriteFailuresTotal.WithLabelValues(endpoint, kind, ec)
			}
		}
	}
	// Pre-instantiate the operator-action gauge for the request-side
	// instance-oriented aliases (park_instance / restart_instance)
	// in addition to the verb-oriented forms. No
	// operatorIntentOutcomeMissingTotal counter is registered here
	// anymore — the Stuck-running counter was removed in the
	// PR-#TBD / fix-cluster E (the counter raced the safety tick
	// AND duplicated the Store.OperatorIntentOutcomeMissingCounts
	// surface used by the operator /obs/health endpoint; that
	// Store method is now the single source of truth).
	for _, kind := range operatorIntentKindClosedSet {
		operatorActionTraceCompletenessRatio.WithLabelValues(kind)
	}
	commonCollectors = append(commonCollectors,
		auditLogWriteTotal,
		auditLogWriteFailuresTotal,
		operatorActionTraceCompletenessRatio,
		operatorActionTraceCompletenessFirstTickCompleted,
		operatorActionTraceCompletenessLastSuccessTimestamp,
	)
	reg.MustRegister(commonCollectors...)
	// Pre-instantiate the closed (op,result) set for the OCI-pull
	// histogram so its HELP/TYPE and zero-valued buckets surface in
	// /metrics from the moment the daemon boots — same precedent as
	// the buildDuration and stripePush pre-instantiation above. The
	// canonical op label set lives next to the observer; if you add
	// a new op there, extend this loop too.
	for _, op := range []string{"manifest", "config", "blob", "above_base"} {
		for _, result := range []string{"ok", "err"} {
			imagedOCIPull.WithLabelValues(op, result)
		}
	}
	// ADR-038: pre-instantiate the closed provenance_writes_total
	// `code` label set so the rows surface in /metrics from boot —
	// same precedent as every other CounterVec on this struct.
	// Closed set: ok, error. Extending this loop is the right
	// place to add a new code (e.g. "permission_denied"); match
	// the same change in ObserveProvenanceWrite below.
	for _, code := range []string{"ok", "error"} {
		provenanceWrites.WithLabelValues(code)
	}
	// issue #419 / ADR-046: closed `provider` label set is the
	// two sign-in OAuth providers `pkg/auth.SignInConfig` knows
	// about. Extending this loop is the right place to add a
	// future provider (e.g. "gitlab"); match the same change in
	// ObserveOAuthDisabled below.
	for _, provider := range []string{"google", "github"} {
		oauthDisabledTotal.WithLabelValues(provider)
	}
	// Mega-PR B / stateless-advisory observability. Closed label
	// sets: result ∈ {ok, dial_failed, rejected,
	// unavailable_after_retry} for the vmmd-side forward outcome
	// counter; severity ∈ {high, warn, info} for the apid-side
	// inbound counter. Pre-instantiation means the §12 dashboard
	// panel surfaces zero at boot. Extending either set is the
	// right place to add a new label (e.g. "rate_limited" on the
	// forward side) — match the same change in the switch in
	// ObserveAdvisoryBatchResult / ObserveStatelessAdvisory below
	// so the closed-set guard doesn't silently drop the value.
	for _, result := range []string{"ok", "dial_failed", "rejected", "unavailable_after_retry"} {
		advisoryBatchesEmittedTotal.WithLabelValues(result)
	}
	for _, sev := range []string{"high", "warn", "info"} {
		apidStatelessAdvisoryEventsTotal.WithLabelValues(sev)
	}
	// issue #432 phase 5: pre-instantiate the githubd bridge
	// `kind` label set ({github, preview} — push → github,
	// pull_request → preview, issue #272 / ADR-094). Same pattern
	// as the pre-instantiations above so the row surfaces in
	// /metrics from boot. Mirrors the constants in this file
	// (GithubdBridgeKind* below).
	for _, kind := range []string{"github", "preview"} {
		apidGithubdBridgeEnqueuedTotal.WithLabelValues(kind)
	}
	// issue #432 phase 5 / ADR-050 §109: pre-instantiate the
	// path-filter `mode` label set. Five values cover every
	// observable outcome in pkg/githubd/service.go's
	// lookupChangedFiles + the new circuit breaker (the
	// breaker_open value is incremented when the breaker is
	// tripped, before the lookup ever calls GitHub). The
	// labels are constants in this file (PathFilterMode*)
	// so the dashboard and the closed-set switch share the
	// same vocabulary.
	for _, mode := range []string{PathFilterModePaths, PathFilterModeFullFallback, PathFilterModeTruncated, PathFilterModeError, PathFilterModeBreakerOpen} {
		githubdPathFilterTotal.WithLabelValues(mode)
	}
	// issue #299: pre-instantiate the closed `severity` label set
	// for imageScanVulns so the rows surface in /metrics from boot
	// — same precedent as every other CounterVec on this struct.
	// The `image` label carries a sentinel `<unknown>` here so a
	// Grafana panel can filter on `image=~"<unknown>"` to plot the
	// "vulns posted by a misconfigured imaged" row separately from
	// the per-image rows that ObserveImageScanVuln adds after the
	// first scan. Using an empty-string sentinel would emit a row
	// with `image=""` whose label Grafana's templating collapses
	// with the placeholders from prometheus's exporter interface,
	// making the row indistinguishable from a scrape artifact.
	// Closed set: CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN — matches
	// the Grype severity vocabulary exactly so an operator can
	// alert on the raw label without remapping. Extending the
	// Grype severity set requires extending this loop in lock-step.
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN"} {
		imageScanVulns.WithLabelValues("<unknown>", sev)
	}
	// Pre-instantiate every outcome in the closed set so the
	// counter's HELP/TYPE and zero-valued rows surface in
	// `/metrics` from the moment schedd boots — even before the
	// first four-phase handoff resolves. Matches the rebalance
	// decisions pre-instantiation shape (one row per outcome;
	// Prometheus skips zero-valued CounterVec series by default,
	// so without this loop the dashboard's "live-migration
	// throughput" panel would render "no data" until the first
	// commit). Extending the outcome vocabulary requires
	// extending this loop in lock-step with the ADR-066
	// Engine.MigrateLiveInstances dispatch.
	for _, outcome := range []string{
		"migrated", "conflict", "no_headroom",
		"no_eligibility", "lease_expired", "peer_failure",
	} {
		liveMigrationDecisions.WithLabelValues(outcome)
	}
	// Pre-instantiate the A6 migrating-instance reconcile outcome
	// set so the §12 panel renders zero on a healthy box (rather
	// than "no data" until the first dead-owner event). Extending
	// the outcome vocabulary requires extending this loop in lock-
	// step with the ADR-067 Engine.ReconcileExpiredMigrations
	// dispatch.
	for _, outcome := range []string{
		"reinvited", "hard_deleted", "conflict", "error",
	} {
		migratingReconcileDecisions.WithLabelValues(outcome)
	}
	// Pre-instantiate the Tier A8 / ADR-083 active-passive
	// fail-over outcome set so the §12 panel renders zero on a
	// healthy box (rather than "no data" until the first
	// drain). Extending the outcome vocabulary requires
	// extending this loop in lock-step with the dns_handoff
	// orchestrator (cmd/gatewayd-public/dns_handoff.go).
	for _, outcome := range []string{
		"dns_flipped", "dns_stale", "peer_unreachable", "manual_drain",
	} {
		activePassiveFailoversTotal.WithLabelValues(outcome)
	}
	// Pre-instantiate the dead-node reconciler's closed outcome set
	// so the §12 panel reads zero on a healthy fleet rather than
	// absent — an absent series and a zero series look identical in
	// a graph but only one proves the reconciler is wired.
	for _, outcome := range []string{"failed", "conflict", "error"} {
		deadNodeReconcileDecisions.WithLabelValues(outcome)
	}
	// Issue #517 / PR-C / ADR-064: pre-instantiate the closed
	// 15-phase × 2-result label set for wakePhaseEmitted and
	// wakePhaseDur so the §12 wake-latency panel surfaces zero
	// on an idle daemon (mirrors the buildDuration / stripePush
	// pre-instantiation precedents above). The phase list mirrors
	// the constants in pkg/events/wake.go — extending the
	// platform vocabulary requires extending this loop in
	// lock-step. result ∈ {ok, failed}.
	for _, phase := range []string{
		"queue_accepted", "admitted", "boot_started", "boot_completed",
		"boot_failed", "readiness_200", "proxy_first_byte",
		"park_started", "park_completed", "stalled",
		"build_succeeded", "build_failed", "deploy_failed",
		// ADR-098 C11: vmmd-side phase-decomposed wake timings
		// (mirrors the three typed scalars on
		// api/proto/onebox/faas/vmmd/v1/vmmd.proto WakeResponse).
		// result=ok on measurement, result=failed on the boundary
		// exception path. result=failed on `restore_ms` is a
		// separate signal from result=failed on `boot_failed` —
		// restore_ms surfaces the /snapshot/load sub-window so
		// the §12 panel can split "restore slow" from "guest
		// init slow" without collapsing them.
		"restore_ms", "netns_tap_ms", "guest_ready_ms",
	} {
		for _, result := range []string{"ok", "failed"} {
			wakePhaseEmitted.WithLabelValues(phase, result)
			wakePhaseDur.WithLabelValues(phase, result)
		}
	}
	// Issue #667 / ADR-078: pre-instantiate the (plan × runtime ×
	// outcome) cartesian for guestTailSeconds, the (plan × reason)
	// cartesian for guestTailFailedTotal, and the plan set for
	// tailCapReached. All three are closed sets; runtime is the
	// ADR-052 hard-coded list of 5 images (bumping this list is
	// the load-bearing step when a new runtime is added). An idle
	// box with no waitUntil traffic would otherwise render the
	// §12 tail-watchdog panels as "no data" until the first tail
	// fires. 60 + 16 + 4 = 80 series; the cardinality test pins
	// the bound.
	for _, plan := range api.Plans {
		for _, runtime := range []string{
			"node22", "node24", "python312", "python313", "go124",
		} {
			for _, outcome := range []string{"completed", "failed", "timeout"} {
				guestTailSeconds.WithLabelValues(string(plan), runtime, outcome)
			}
		}
		for _, reason := range []string{
			"timeout", "handler_error", "forced_at_park", "unknown",
		} {
			guestTailFailedTotal.WithLabelValues(string(plan), reason)
		}
		tailCapReached.WithLabelValues(string(plan))
	}
	// Pre-instantiate every label in the closed result set so the
	// histogram's HELP/TYPE and zero-valued buckets surface in
	// `/metrics` from the moment the daemon boots — even before the
	// first Stripe push. Prometheus' default exposition skips
	// HistogramVec series with zero observed label tuples, which would
	// render the dashboard's stripe-push panel as "no data" until at
	// least one push happened (a real ops hazard). The label set is
	// the canonical closed list from stripe.PushResultLabels —
	// adding a label there must also extend this loop. ADR-024.
	for _, label := range stripe.PushResultLabels() {
		stripePushDur.WithLabelValues(label)
	}
	// Pre-instantiate every label in the closed result set for the
	// Paddle push histogram so its HELP/TYPE and zero-valued buckets
	// surface in /metrics from the moment the daemon boots — even
	// before the first Paddle push on a Paddle-enabled deployment.
	// Without this, a deployments that boot FAAS_BILLING_PROVIDER=paddle
	// would render the dashboard panel as "no data" until at least
	// one push happened (a real ops hazard). The label set is the
	// canonical closed list from paddle.PushResultLabels — adding a
	// label there must also extend this loop. ADR-032.
	for _, label := range paddle.PushResultLabels() {
		paddlePushDur.WithLabelValues(label)
	}
	for _, label := range polar.PolarPushResultLabels() {
		polarPushDur.WithLabelValues(label)
	}
	// Pre-instantiate the closed plan set for the residentGBPerCustomer
	// gauge so its HELP/TYPE and zero-valued samples surface in /metrics
	// from the moment the daemon boots — same precedent as the histogram
	// pre-instantiation above. An idle box with zero paying customers
	// would otherwise render the dashboard panel as "no data" until at
	// least one plan tick has fired (ADR-031).
	for _, plan := range api.Plans {
		residentGBPerCustomer.WithLabelValues(string(plan))
	}
	// Pre-instantiate the closed plan set for the billingCapExceeded
	// counter (issue #279). Same precedent as residentGBPerCustomer
	// above. An idle box with no cap-hits would otherwise render the
	// §12 dashboard panel as "no data" until the first cap hit.
	for _, plan := range api.Plans {
		billingCapExceededTotal.WithLabelValues(string(plan))
		meterdFloorAppliedTotal.WithLabelValues(string(plan))
	}
	// Pre-instantiate the closed (mode, plan) cartesian for
	// meteredMBSecondsTotal (M-2 / ADR-137 §Decision 1). The closed
	// mode set is the billable subset of pkg/state.InstanceMode
	// ({normal,worker,service,job}) — mirror is filtered upstream.
	// An idle box with no live instances would otherwise render the §12
	// "MB-seconds by mode" panel as "no data" until at least one tick
	// fires, defeating the dashboard alert.
	for _, mode := range []string{"normal", "worker", "service", "job"} {
		for _, plan := range api.Plans {
			meteredMBSecondsTotal.WithLabelValues(mode, string(plan))
		}
	}
	// Pre-instantiate every (cidr, family) label tuple from the egress
	// denylist catalog so the counter's HELP/TYPE and zero-valued series
	// surface in /metrics from the moment the daemon boots — same
	// precedent as the histogram and gauge pre-instantiation above. PR-E:
	// the catalog is closed and bounded (~12 v4 + ~7 v6 entries),
	// sourced from netns.NewDefaultDenySet(); the cidr label is the
	// DenyEntry.CounterName (the canonical name that vmmd's nft-poll
	// adapter looks up in the `nft list counters` JSON output) and the
	// family label is the nft family keyword ("ip" / "ip6") matching
	// DenyEntry.Family.String(). Without this loop, an idle box would
	// render the egress-deny panel as "no data" until at least one
	// drop had been observed (a real ops hazard — operators want to see
	// the panel exist on day one).
	for _, e := range netns.NewDefaultDenySet().Entries {
		egressDeny.WithLabelValues(e.CounterName, e.Family.String())
	}
	// PR-E: pre-instantiate the imaged-side mirror counter
	// (oci_egress_deny_total) with the catalog entries. The OCI-only
	// extras (loopback / 0.0.0.0/8 / IETF-assigned / benchmarking /
	// reserved — see pkg/oci/egress.go) are pre-instantiated from
	// cmd/imaged/main.go so pkg/wire doesn't need to import pkg/oci.
	// The firewall-side counter above uses the SAME catalog tuples, so
	// the two metrics share the catalog-portion of the label set.
	if ociEgressDeny != nil {
		for _, e := range netns.NewDefaultDenySet().Entries {
			ociEgressDeny.WithLabelValues(e.CounterName, e.Family.String())
		}
	}
	// Pre-instantiate the closed outcome label set for the scale-up
	// decisions counter — same precedent as the build / Stripe /
	// Paddle histograms above. The (app="") tuple is NEVER used
	// (the trigger always emits a real app_id); the empty-app row
	// is a placeholder so the help/TYPE surfaces in /metrics before
	// the first decision fires. Real per-app rows are added by
	// ObserveScaleUp below.
	//
	// P1A: include `min_floor_already` and `overage_cap_reached` in
	// the closed set. Both are written by Engine.admitGate at
	// pkg/sched/engine.go:4868-4873 (min_floor_already, when
	// ScalingPolicy.MinInstances is already met and no traffic
	// signal) and pkg/sched/engine.go:4876-4888 (overage_cap_reached,
	// issue #561, when OverageChecker reports OverageReached). The
	// closed-set loop previously omitted these two — they were still
	// emitted by admitGate, but only surfaced in /metrics after the
	// first gate fire per app, breaking the §12 panel-at-day-1 contract
	// (PR #826 precedent: pre-instantiation required).
	for _, outcome := range []string{
		"admit", "reject_at_cap", "no_signal",
		"cooldown_held", "min_floor_already", "overage_cap_reached",
	} {
		scaleUpDecisions.WithLabelValues("", outcome)
	}
	// Issue #300: pre-instantiate the ("other",) row of the per-tenant
	// RPS gauge so the help/TYPE surfaces in /metrics from boot, before
	// the first 5s sampler tick fires. Same precedent as the closed
	// scale-up outcome / egress-deny catalog loops above. Real customer
	// ids are added by TopTenantRPSFor (cmd/apid/topn.go / cmd/gatewayd-internal/
	// listener.go), which routes through topAccountSet and demotes past
	// top-1000 into this bucket.
	topTenantRPS.WithLabelValues(topAccountOtherLabel)
	// issue #301 / ADR-044: pre-instantiate the closed "slice"
	// label set for throttleRatio (the FaasCpuStarvation alert
	// source) so the gauge surfaces "no data" → "0" on an idle
	// box. The slice values are the api.Plan.SliceName() set
	// (tenant-free, tenant-hobby, tenant-pro, tenant-scale);
	// the alert's `slice=~"tenant-.*"` matcher stays stable
	// for any future tenant-customer slice hierarchy.
	for _, slice := range []string{"tenant-free", "tenant-hobby", "tenant-pro", "tenant-scale"} {
		throttleRatio.WithLabelValues(slice)
	}
	// Pre-instantiate the ("other", "other") overflow bucket for
	// throttleSecondsTotal so the dashboard's
	// {app_id!="other"} selector excludes a series that exists
	// from boot. Real (account_id, app_id) pairs are added by
	// ObserveTopAppThrottle (the vmmd-side sampler, fed via
	// ReplaceInstanceStats) which routes through topAppSet and
	// demotes past top-100 into this bucket.
	throttleSecondsTotal.WithLabelValues(topAppOtherAccountLabel, topAppOtherLabel)
	// issue #171: pre-instantiate the {park, keep, min_floor_already,
	// cooldown_held} outcome rows for the empty-app label so the
	// help/TYPE surfaces in /metrics from boot, mirroring the scale-up
	// pattern above.
	//   - min_floor_already (PR-C, issue #462): per-app "would have
	//     parked, but min_instances is reached".
	//   - cooldown_held (P1C): per-app scale-in cooldown consult in
	//     ReapAggressive (pkg/sched/reaper.go) skipped the entire app;
	//     the app is absent from the park slice. Idle branch
	//     (ReapIdle) emits no decision metrics today — adding a
	//     parallel emission there is a separate change.
	for _, outcome := range []string{"park", "keep", "min_floor_already", "cooldown_held"} {
		scaleDownDecisions.WithLabelValues("", outcome)
	}
	// Issue #557 / ADR-071 §Decision 1: pre-instantiate the eight
	// closed outcome values for the proactive floor reconciler so
	// the rows surface in /metrics from boot. Same precedent as
	// the scale-up / scale-down pre-instantiation loops.
	for _, outcome := range []string{
		"admit", "floor_met", "disabled", "at_capacity",
		"ram_ceiling", "cooldown_held", "error", "backoff_held",
	} {
		floorReconcileDecisions.WithLabelValues("", outcome)
	}
	for _, kind := range []string{"admit_denied", "admit_error"} {
		floorReconcileErrors.WithLabelValues("", kind)
	}
	// issue #279 (PR-B, CPU-hour visibility): pre-instantiate the
	// empty (app, node) row so the help/TYPE surfaces in /metrics
	// from boot. Same precedent as the scale-up / scale-down
	// outcome rows above. Real per-(app, node) rows are added by
	// the rollup in ReplaceInstanceStats.
	instanceCPUSecondsTotal.WithLabelValues("", "")
	// PR-C §4 (issue #463 / ADR-069 / ADR-071): pre-instantiate
	// the empty (app, sidecar) row so the help/TYPE surfaces in
	// /metrics from boot. Real per-(app, sidecar) rows are added
	// by vmmd's dispatchSidecarRestart via ObserveSidecarRestart.
	// The empty tuple (bound default: unknown unknown) is the
	// overflow bucket operators inspect when an unlabeled sidecar
	// leaks through (should never happen — guest-init always
	// stamps the sidecar's name).
	sidecarRestartTotal.WithLabelValues("", "")
	// issue #301 (ADR-043, per-plan CPU fairness observability):
	// pre-instantiate the ("other", "other") overflow row so the
	// dashboard panel selector {app_id!="other"} never sees "no
	// data" — same precedent as instanceCPUSecondsTotal above and
	// the vmmd_top_tenant_rps gauge (issue #300). The (other,
	// other) overflow is also re-emitted by EmitTopAppThrottle
	// every 5s; this pre-instantiation covers the boot window
	// before the first sampler tick.
	throttleSecondsTotal.WithLabelValues(topAppOtherAccountLabel, topAppOtherLabel)
	return &OpsMetrics{
		registry:                             reg,
		metricPrefix:                         prefix,
		ops:                                  ops,
		dur:                                  dur,
		watchdogKills:                        watchdogKills,
		warmSnapshotErrors:                   warmSnapshotErrors,
		warmupErrors:                         warmupErrors,
		writeRedirectTotal:                   writeRedirectTotal,
		writeRedirectLatency:                 writeRedirectLatency,
		livenessRestarts:                     livenessRestarts,
		workloadOOMKills:                     workloadOOMKills,
		daemonRestartCount:                   daemonRestartCount,
		daemonBuildInfo:                      daemonBuildInfo,
		daemonUptimeSeconds:                  daemonUptimeSeconds,
		daemonReady:                          daemonReady,
		faasDeployVersion:                    faasDeployVersion,
		bridgeFramingTotal:                   bridgeFramingTotal,
		guestInitDuration:                    guestInitDuration,
		wakeRPCDuration:                      wakeRPCDuration,
		gatewayDrainWaitSeconds:              gatewayDrainWaitSeconds,
		gatewayInflightRequests:              gatewayInflightRequests,
		wakeSnapshotTier:                     wakeSnapshotTier,
		wakeFailure:                          wakeFailure,
		wakeLatency:                          wakeLatency,
		boxLabels:                            newBoxLabelSet(maxBoxLabelValues),
		appLabels:                            newAppLabelSet(maxAppLabelValues),
		guestTailSeconds:                     guestTailSeconds,
		guestTailFailedTotal:                 guestTailFailedTotal,
		planGateRescuedByExclude:             planGateRescuedByExclude,
		tailCapReached:                       tailCapReached,
		evictedPriority:                      evictedPriority,
		eventsWriteFail:                      eventsWriteFail,
		auditWriteFail:                       auditWriteFail,
		auditWriteDur:                        auditWriteDur,
		accountOrgMismatch:                   accountOrgMismatch,
		requestFailures:                      requestFailures,
		requestTotal:                         requestTotal,
		cveCheckTotal:                        cveCheckTotal,
		cvesOpenTotal:                        cvesOpenTotal,
		appErrorsRecorded:                    appErrorsRecorded,
		appErrorsFingerprintCacheHits:        appErrorsFingerprintCacheHits,
		appErrorsDedupeMerges:                appErrorsDedupeMerges,
		appErrorsFlushDuration:               appErrorsFlushDuration,
		requestTelemetryRecorded:             requestTelemetryRecorded,
		gatewaydPublicOtelSpansIngested:      gatewaydPublicOtelSpansIngested,
		gatewaydPublicOtelSpansTruncated:     gatewaydPublicOtelSpansTruncated,
		gatewaydPublicOtelAuthFailures:       gatewaydPublicOtelAuthFailures,
		spansWriteOutcomes:                   spansWriteOutcomes,
		appErrorsPurges:                      appErrorsPurges,
		debugRegressionOldestPassSeconds:     debugRegressionOldestPassSeconds,
		debugRegressionSkippedFlagDisabled:   debugRegressionSkippedFlagDisabled,
		debugRegressionDetected:              debugRegressionDetected,
		previewJanitorOutcomes:               previewJanitorOutcomes,
		dataUpstreamRTT:                      dataUpstreamRTT,
		dataUpstreamProbes:                   dataUpstreamProbes,
		dataUpstreamProbeDuration:            dataUpstreamProbeDuration,
		accountLabels:                        newAccountLabelSet(maxAccountLabelValues),
		failedLoginTotal:                     failedLoginTotal,
		failedLoginDropped:                   failedLoginDropped,
		failedLoginAuditWriteFailures:        failedLoginAuditWriteFailures,
		auditEventsDeletedTotal:              auditEventsDeletedTotal,
		auditEventsRetentionLagSeconds:       auditEventsRetentionLagSeconds,
		domainDoctorOldestObservationSeconds: domainDoctorOldestObservationSeconds,
		domainDoctorSkippedFlagDisabled:      domainDoctorSkippedFlagDisabled,
		auditEventsVolumeTotal:               auditEventsVolumeTotal,
		deploymentAuditGCRowsDeletedTotal:    deploymentAuditGCRowsDeletedTotal,
		alertEvalSkippedDegradedTotal:        alertEvalSkippedDegradedTotal,
		alertEvalFiredTotal:                  alertEvalFiredTotal,
		canaryProgressionAdvancedTotal:       canaryProgressionAdvancedTotal,
		canaryProgressionErrorsTotal:         canaryProgressionErrorsTotal,
		canaryProgressionZeroTimestampTotal:  canaryProgressionZeroTimestampTotal,
		alertDeliveryAttemptsTotal:           alertDeliveryAttemptsTotal,
		alertActionExecutedTotal:             alertActionExecutedTotal,
		paddleWebhookVerifyFailedTotal:       paddleWebhookVerifyFailedTotal,
		paddleWebhookReplaySuppressedTotal:   paddleWebhookReplaySuppressedTotal,
		alertEvaluatorEnabled:                alertEvaluatorEnabled,
		meterdAccountSpendEur:                meterdAccountSpendEur,
		meterdAPIReachable:                   meterdAPIReachable,
		apidDeploymentFailedTotal:            apidDeploymentFailedTotal,
		apidTenantSurfaceCertExpirySeconds:   apidTenantSurfaceCertExpirySeconds,
		apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal: apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal,
		pgBackupLastPushed:                   pgBackupLastPushed,
		ipLabels:                             newIPLabelSet(maxIPLabelValues),
		topTenantRPS:                         topTenantRPS,
		topAccounts:                          newTopAccountSet(topAccountSetCap),
		throttleSecondsTotal:                 throttleSecondsTotal,
		throttleRatio:                        throttleRatio,
		topApps:                              newTopAppSet(topAppSetCap),
		throttleSecondsLastSeen:              newCPUThrottleLastSeen(),
		cpuSecondsLast:                       newCPUSecondsLastSeen(),
		cronFireNowDispatchDur:               cronFireNowDispatchDur,
		stripePushDur:                        stripePushDur,
		paddlePushDur:                        paddlePushDur,
		polarPushDur:                         polarPushDur,
		buildDur:                             buildDur,
		buildQueueWait:                       buildQueueWait,
		residentGBPerCustomer:                residentGBPerCustomer,
		billingCapExceededTotal:              billingCapExceededTotal,
		meterdFloorAppliedTotal:              meterdFloorAppliedTotal,
		meteredMBSecondsTotal:                meteredMBSecondsTotal,
		auditOrgEvent:                        auditOrgEvent,
		authzDenied:                          authzDenied,
		authzAllowed:                         authzAllowed,
		wakeIDV4Fallback:                     wakeIDV4Fallback,
		snapshotDiskDrift:                    snapshotDiskDrift,
		capacitySignatureRejected:            capacitySignatureRejected,
		imagedOCIPull:                        imagedOCIPull,
		instanceCPUPct:                       instanceCPUPct,
		instanceRSSMB:                        instanceRSSMB,
		instanceInflightReqs:                 instanceInflightReqs,
		instanceCPUSecondsTotal:              instanceCPUSecondsTotal,
		instanceStatsCollectDur:              instanceStatsCollectDur,
		instanceStatsPartialErrors:           instanceStatsPartialErrors,
		sidecarRestartTotal:                  sidecarRestartTotal,
		cpuStatsCollectDur:                   cpuStatsCollectDurLocal,
		scaleUpDecisions:                     scaleUpDecisions,
		scaleDownDecisions:                   scaleDownDecisions,
		floorReconcileDecisions:              floorReconcileDecisions,
		floorReconcileErrors:                 floorReconcileErrors,
		floorInstancesAdmitted:               floorInstancesAdmitted,
		scaleUpAdmitRPS:                      scaleUpAdmitRPS,
		sseClients:                           sseClients,
		egressDeny:                           egressDeny,
		ociEgressDeny:                        ociEgressDeny,
		ownershipClamp:                       ownershipClamp,
		layerEntrySkipped:                    layerEntrySkipped,
		provenanceWrites:                     provenanceWrites,
		imageScanVulns:                       imageScanVulns,
		deployScanDuration:                   deployScanDuration,
		deployStageDuration:                  deployStageDuration,
		deployScanTotal:                      deployScanTotal,
		deployScanVulns:                      deployScanVulns,
		liveMigrationDecisions:               liveMigrationDecisions,
		rebalanceDecisions:                   rebalanceDecisions,
		migratingReconcileDecisions:          migratingReconcileDecisions,
		appAtCapacityTotal:                   appAtCapacityTotal,
		pressureReassignmentsTotal:           pressureReassignmentsTotal,
		overflowTargetSpillHitsTotal:         overflowTargetSpillHitsTotal,
		activePassiveFailoversTotal:          activePassiveFailoversTotal,
		standbyState:                         standbyState,
		standbyStateValue:                    StandbyStateWarming, // mirrors the gauge.Set(StandbyStateWarming) above
		deadNodeReconcileDecisions:           deadNodeReconcileDecisions,
		registryCredentialMarkUsedFailures:   registryCredentialMarkUsedFailures,
		storageCacheStaleFallback:            storageCacheStaleFallback,
		apidLogsEmittedTotal:                 apidLogsEmittedTotal,
		apidLogsDroppedTotal:                 apidLogsDroppedTotal,
		egressSourceErrors:                   egressSourceErrors,
		oauthDisabledTotal:                   oauthDisabledTotal,
		advisoryBatchesEmittedTotal:          advisoryBatchesEmittedTotal,
		apidStatelessAdvisoryEventsTotal:     apidStatelessAdvisoryEventsTotal,
		apidGithubdBridgeEnqueuedTotal:       apidGithubdBridgeEnqueuedTotal,
		githubdPathFilterTotal:               githubdPathFilterTotal,
		wakePhaseEmitted:                     wakePhaseEmitted,
		wakePhaseDur:                         wakePhaseDur,
		esmPollsTotal:                        esmPollsTotal,
		esmRecordsConsumedTotal:              esmRecordsConsumedTotal,
		esmLagSeconds:                        esmLagSeconds,
		auditLogWriteTotal:                   auditLogWriteTotal,
		auditLogWriteFailuresTotal:           auditLogWriteFailuresTotal,
		operatorActionTraceCompletenessRatio: operatorActionTraceCompletenessRatio,
		operatorActionTraceCompletenessFirstTickCompleted:   operatorActionTraceCompletenessFirstTickCompleted,
		operatorActionTraceCompletenessLastSuccessTimestamp: operatorActionTraceCompletenessLastSuccessTimestamp,
	}
}

// WatchdogKills returns the per-(from_state, to_state) counter the
// §6.1 watchdog increments when it transitions a stuck instance.
// The returned Counter can be safely cached by callers; the underlying
// CounterVec is shared with other label tuples.
func (m *OpsMetrics) WatchdogKills(fromState, toState string) prometheus.Counter {
	return m.watchdogKills.WithLabelValues(fromState, toState)
}

// ObserveDrainWait (issue #587 / PR-A) records the wall-clock
// duration of a drain.Tracker.Drain call. outcome is one of
// drain.Outcome{Clean,DeadlineExceeded,Cancelled} — the caller
// passes the string it got back from Drain. daemon ∈ {gatewayd-public,
// gatewayd-internal} — the prefix is the per-daemon scope, not
// the metric name (the metric is `gatewayd_drain_wait_seconds`
// in production with the gatewayd prefix; the prefix is already
// applied by NewOpsMetrics). Nil-safe: nil receiver is a no-op
// so unit tests don't have to wire metrics.
func (m *OpsMetrics) ObserveDrainWait(daemon, outcome string, seconds float64) {
	if m == nil || m.gatewayDrainWaitSeconds == nil {
		return
	}
	m.gatewayDrainWaitSeconds.WithLabelValues(daemon, outcome).Observe(seconds)
}

// SetInflightRequests (issue #587 / PR-A) sets the per-daemon
// per-op in-flight gauge. op ∈ {http, upgrade, control}. Called
// by the daemon at boot (zero) and after every drain.Tracker
// state change (the tracker exposes Inflight() and
// MaxInflight()). daemon follows the same convention as
// ObserveDrainWait. Nil-safe.
func (m *OpsMetrics) SetInflightRequests(daemon, op string, count float64) {
	if m == nil || m.gatewayInflightRequests == nil {
		return
	}
	m.gatewayInflightRequests.WithLabelValues(daemon, op).Set(count)
}

// LivenessRestarts returns the per-(app, deployment) counter the
// Engine.DestroyForLivenessFailure path increments on every
// liveness-driven destroy (issue #554 / ADR-078). The dashboard
// panel "liveness: restarts by deployment (5m)" queries this; the
// liveness_exhausted park alert (instances.parked_liveness_exhausted
// audit kind) is the operator-facing signal. The returned Counter
// is safe to retain — Prometheus's WithLabelValues is internally
// cached. nil-receiver guard mirrors the WatchdogKills /
// WarmSnapshotErrors pattern so unit tests without metrics keep
// working.
func (m *OpsMetrics) LivenessRestarts(app, deployment string) prometheus.Counter {
	if m == nil {
		return nil
	}
	if app == "" {
		app = labelUnknown
	}
	if deployment == "" {
		deployment = labelUnknown
	}
	return m.livenessRestarts.WithLabelValues(app, deployment)
}

// WorkloadOOMKills (Cluster C / ADR-121) returns the per-(app,
// deployment) counter the Engine.DestroyForWorkloadOOMFailure path
// increments on every workload OOM-kill. The dashboard panel pairs
// this with LivenessRestarts to distinguish "the app is unhealthy"
// (liveness) from "the app's RAM cap is too low" (workload OOM).
// nil-receiver guard mirrors LivenessRestarts / WatchdogKills so
// unit tests without metrics keep working.
func (m *OpsMetrics) WorkloadOOMKills(app, deployment string) prometheus.Counter {
	if m == nil {
		return nil
	}
	if app == "" {
		app = labelUnknown
	}
	if deployment == "" {
		deployment = labelUnknown
	}
	return m.workloadOOMKills.WithLabelValues(app, deployment)
}

// RecordDaemonRestart (issue #573 / ADR-128) records the systemd
// restart count for the calling daemon. The wire.Daemon() boot
// path reads $SYSTEMD_RESTARTS_ON_FAILURE (set by the systemd
// unit's Restart=on-failure + RestartCountExport logic — see
// deploy/ansible/roles/<daemon>/files/<daemon>.service) and calls
// this accessor once at startup. Add(1) is called n-1 times where
// n is the systemd restart count, so the counter ends at n minus
// the increment at the boot immediately after — operators see
// "this process is the Nth incarnation" rather than "N+1
// increments happened across N processes". A "this process" read
// is also what the FaasRestartLoop alert wants (it's wired off
// the per-process delta).
//
// The daemon label is normalised through the closed set
// (apid, gatewayd-public, gatewayd-internal, schedd, vmmd, imaged,
// meterd, builderd, githubd, gregale) — anything else collapses to "other"
// so the label cardinality stays bounded across the daemon's
// lifetime. nil-receiver guard mirrors LivenessRestarts /
// WorkloadOOMKills so unit tests without metrics keep working.
func (m *OpsMetrics) RecordDaemonRestart(daemon, version string, n int) {
	if m == nil || n <= 0 {
		return
	}
	switch daemon {
	case "apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged", "meterd", "builderd", "githubd", "gregale":
		// closed set, admit unchanged
	default:
		daemon = "other"
	}
	if version == "" {
		version = Version
	}
	m.daemonRestartCount.WithLabelValues(daemon, version).Add(float64(n - 1))
}

// SetDaemonBuildInfo (issue #586 / ADR-129) re-stamps the build
// info gauge for a daemon. The constructor already pre-instantiates
// every (daemon, thisVersion, thisGitSHA, thisBuildTime) row with
// value=1, so this accessor is only useful when the daemon's
// identity changes mid-process (e.g. a hot-reload that re-reads
// ldflags-injected values, or a /v1/internal/reload-build-info
// handler that's out of scope here). nil-receiver guard mirrors
// RecordDaemonRestart so unit tests without metrics keep working.
func (m *OpsMetrics) SetDaemonBuildInfo(daemon, version, gitSHA, buildTime string) {
	if m == nil {
		return
	}
	switch daemon {
	case "apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged", "meterd", "builderd", "githubd", "gregale":
		// closed set, admit unchanged
	default:
		daemon = "other"
	}
	if version == "" {
		version = Version
	}
	if gitSHA == "" {
		gitSHA = GitSHA
	}
	m.daemonBuildInfo.WithLabelValues(daemon, version, gitSHA, buildTime).Set(1)
}

// SetDaemonUptime (issue #586 / ADR-129) updates the per-daemon
// uptime gauge. Called once per second by the goroutine spawned
// from wire.Daemon() (see recordUptime). nil-receiver guard
// mirrors RecordDaemonRestart.
func (m *OpsMetrics) SetDaemonUptime(daemon string, seconds float64) {
	if m == nil {
		return
	}
	switch daemon {
	case "apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged", "meterd", "builderd", "githubd", "gregale":
		// closed set, admit unchanged
	default:
		daemon = "other"
	}
	m.daemonUptimeSeconds.WithLabelValues(daemon).Set(seconds)
}

// MarkReady (issue #586 / ADR-129 / issue #571 PR-A2) flips the
// per-daemon readiness gauge. Called by the daemon-side
// readiness probes (pkg/wire/readiness.go, commit 2) when their
// signals flip — single source of truth between the /readyz body
// and the daemon_ready gauge.
//
// reason is captured for human triage (operator can pair the
// reason string with the gauge value in journalctl) but is NOT
// surfaced as a Prometheus label — adding a reason label would
// inflate cardinality (one row per unique reason string per
// daemon). Pass "" when there is no human-readable reason to
// surface; pass "draining" / "pg ping failed: ..." / etc. when
// the operator can act on it.
//
// nil-receiver guard mirrors RecordDaemonRestart.
func (m *OpsMetrics) MarkReady(daemon string, ready bool, reason string) {
	if m == nil {
		return
	}
	switch daemon {
	case "apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged", "meterd", "builderd", "githubd", "gregale":
		// closed set, admit unchanged
	default:
		daemon = "other"
	}
	v := 0
	if ready {
		v = 1
	}
	m.daemonReady.WithLabelValues(daemon).Set(float64(v))
}

// SetDeployVersion (issue #586 / ADR-129) stamps the platform-wide
// release identifier gauge. Called once by wire.Daemon() at boot from
// wire.Version; the constructor pre-instantiates the row at the same
// value, so this Set is idempotent and covers the edge case where
// ldflags inject a different value mid-process (rolling deploy, hot
// reload of a sidecar). The label is `version` — free-form text
// matching wire.Version verbatim (typically "v1.2.3-<sha>"). nil-receiver
// guard mirrors MarkReady.
func (m *OpsMetrics) SetDeployVersion(version string) {
	if m == nil {
		return
	}
	if version == "" {
		version = Version
	}
	m.faasDeployVersion.WithLabelValues(version).Set(1)
}

// RecordCVECheck (issue #601 / ADR-131 / cluster D commit 13 of
// the platform-observability mega-PR) increments the per-run
// CVE-check counter. Called by meterd when it scrapes the
// .github/workflows/cve-check.yml artifact. The closed-set
// labels (result, severity) are pre-instantiated in the
// constructor; RecordCVECheck accepts any string but Prometheus's
// WithLabelValues caches on first call so an out-of-vocabulary
// call is just a one-time cardinality bump. nil-receiver guard
// mirrors SetDeployVersion.
func (m *OpsMetrics) RecordCVECheck(result, severity string) {
	if m == nil {
		return
	}
	m.cveCheckTotal.WithLabelValues(result, severity).Inc()
}

// IncCVEOpen (issue #601 / ADR-131) sets the per-(severity, dep)
// open-CVE gauge. Called by meterd from grype's filtered
// output. The dep label is unbounded by design (every package
// has a row) but bounded in practice by the dependency tree
// size — single-digit thousands at worst. nil-receiver guard
// mirrors RecordCVECheck.
func (m *OpsMetrics) IncCVEOpen(severity, dep string, count float64) {
	if m == nil {
		return
	}
	m.cvesOpenTotal.WithLabelValues(severity, dep).Set(count)
}

// BridgeFramingTotal (ADR-127 §D3, Layer 7) returns the per-
// (app_protocol, bridge_protocol, framing) counter that the
// vmmd-stream-bridge's newHandler closure increments on every
// inbound request. The "mismatch" label is the operator-facing
// signal for the surgical-rollback path: a sustained mismatch
// rate > 0.1 rps triggers FaasBridgeFramingMismatch (see
// pkg/api/alerts.go).
//
// All three label values are validated against closed sets in
// the producer (cmd/vmmd-stream-bridge::newHandler); invalid
// labels surface as Prometheus label-validation errors at boot
// rather than silently extending the time-series set. nil-
// receiver guard matches the OpsMetrics accessors above so unit
// tests without metrics keep working.
func (m *OpsMetrics) BridgeFramingTotal(appProtocol, bridgeProtocol, framing string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.bridgeFramingTotal.WithLabelValues(appProtocol, bridgeProtocol, framing)
}

// WarmSnapshotErrors returns the per-reason counter the warm-tier
// capture (issue #470 / PR A / ADR-055) increments on every soft
// failure. The two label values are the only ones schedd emits:
//   - "vmm_call" — vmm.WarmSnapshot returned a non-nil error and
//     the engine transitioned the instance to STOPPED.
//   - "store_write" — CreateSnapshot rejected the staging row.
//     The corresponding vmm call still succeeded; the blob is on
//     disk but the audit row is missing. PR C's ngcpath hook
//     reconciles the delete.
//
// The returned Counter is safe to retain — Prometheus's
// WithLabelValues is internally cached. nil-receiver guard mirrors
// the EgressDeny / OCIEgressDeny pattern so unit tests without
// metrics keep working.
func (m *OpsMetrics) WarmSnapshotErrors(reason string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.warmSnapshotErrors.WithLabelValues(reason)
}

// WarmupErrors returns the per-(app slug) probe-failure counter
// the standby warm-up scraper increments on every probe failure
// (Tier A8 / ADR-083). The dashboard panel "standby warmup: errors
// by app (5m)" queries this; the §12 standby-warmup alert fires
// on a sustained non-zero rate. Bounded cardinality by
// FAAS_STANDBY_WARMUP_SLUGS_PATH. nil-safe — returns nil if m is
// nil (a unit test without metrics keeps building).
func (m *OpsMetrics) WarmupErrors(slug string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.warmupErrors.WithLabelValues(slug)
}

// WriteRedirectTotal returns the (outcome, auth_kind)-labeled
// counter the cmd/gatewayd-internal writeGate bumps per
// classification (Tier A9 / ADR-084). The closed label sets
// `outcome ∈ {relayed, redirect_307, same_box, cookie_blocked,
// leader_unreachable, loop_prevented, mTLS_failure, error}` and
// `auth_kind ∈ {bearer, cookie, anonymous}` are pre-instantiated
// at every (outcome, kind) pair in the constructor block above;
// callers should not introduce new label values (writeGate
// classifies at request entry, never per-request-derived). The
// Prometheus query
// `rate(gatewayd_internal_write_redirect_total{outcome="relayed"}[5m])`
// is the cross-box hop health signal; `{outcome="loop_prevented"}`
// is the redirect-storm DoS alarm. nil-safe — returns nil if m
// is nil.
func (m *OpsMetrics) WriteRedirectTotal(outcome, authKind string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.writeRedirectTotal.WithLabelValues(outcome, authKind)
}

// WriteRedirectLatency returns the histogram observer the
// cmd/gatewayd-internal writeGate uses to record cross-box mTLS
// hop durations (Tier A9 / ADR-084). Only cross-box hops emit a
// sample; same-box fall-through and 307-fallback paths do not.
// Buckets are sized for the overlay round-trip + leader apid
// handler (5 ms to 5 s — the top bucket matches
// pkg/api/limits.go::StandbyWriteRedirectTimeoutMS). nil-safe —
// returns nil if m is nil; callers should use Observe with
// nil-safe wrappers if they need to.
func (m *OpsMetrics) WriteRedirectLatency() prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.writeRedirectLatency
}

// ObserveAppErrorsRecorded increments the customer-facing
// automatic error grouping ingest counter (ADR-096). outcome
// MUST be one of {ok, redaction_failed, rate_limited, db_error};
// the closed set is enforced by the canonical ADR-096 spec
// (the apid gRPC handler + gatewayd-internal recorder share
// this contract). nil-safe — no-op if m is nil.
func (m *OpsMetrics) ObserveAppErrorsRecorded(outcome string) {
	if m == nil || m.appErrorsRecorded == nil {
		return
	}
	m.appErrorsRecorded.WithLabelValues(outcome).Inc()
}

// IncrementRequestTelemetryRecorded increments the
// production-debugger ingest counter by outcome. outcome ∈
// {inserted, rate_limited, db_error} — the closed label set keeps
// cardinality bounded. nil-safe — no-op if m is nil.
func (m *OpsMetrics) IncrementRequestTelemetryRecorded(outcome string) {
	if m == nil || m.requestTelemetryRecorded == nil {
		return
	}
	m.requestTelemetryRecorded.WithLabelValues(outcome).Inc()
}

// IncrementGatewaydPublicOtelSpansIngested increments the OTel
// spans writer ingest counter by outcome. outcome ∈ {inserted,
// rate_limited, db_error, shape_invalid}. nil-safe — no-op if m
// is nil.
func (m *OpsMetrics) IncrementGatewaydPublicOtelSpansIngested(outcome string) {
	if m == nil || m.gatewaydPublicOtelSpansIngested == nil {
		return
	}
	m.gatewaydPublicOtelSpansIngested.WithLabelValues(outcome).Inc()
}

// IncrementGatewaydPublicOtelSpansTruncated increments the OTel
// spans writer truncation tripwire (once per OTLP POST whose
// inbound body exceeded DebugTelemetrySpansPerTrace). Unlabelled.
// nil-safe — no-op if m is nil.
func (m *OpsMetrics) IncrementGatewaydPublicOtelSpansTruncated() {
	if m == nil || m.gatewaydPublicOtelSpansTruncated == nil {
		return
	}
	m.gatewaydPublicOtelSpansTruncated.Inc()
}

// IncrementGatewaydPublicOtelAuthFailures increments the OTel
// spans writer auth-failure counter by reason. reason ∈
// {unauthenticated, plan_disabled, internal}. nil-safe — no-op
// if m is nil.
func (m *OpsMetrics) IncrementGatewaydPublicOtelAuthFailures(reason string) {
	if m == nil || m.gatewaydPublicOtelAuthFailures == nil {
		return
	}
	m.gatewaydPublicOtelAuthFailures.WithLabelValues(reason).Inc()
}

// IncrementSpansWriteOutcome increments the apid-side spans
// writer counter by outcome. outcome ∈ {inserted, rate_limited,
// db_error}. nil-safe — no-op if m is nil. Distinct from
// gatewaydPublicOtelSpansIngested (gateway-side) because the
// apid counter fires once per WriteSpansSummary RPC, not per
// OTLP POST — they're at different layers of the protocol
// stack.
func (m *OpsMetrics) IncrementSpansWriteOutcome(outcome string) {
	if m == nil || m.spansWriteOutcomes == nil {
		return
	}
	m.spansWriteOutcomes.WithLabelValues(outcome).Inc()
}

// ObserveAppErrorsFingerprintCacheHit increments the in-process
// LRU fingerprint cache hit counter (ADR-096). nil-safe.
func (m *OpsMetrics) ObserveAppErrorsFingerprintCacheHit() {
	if m == nil || m.appErrorsFingerprintCacheHits == nil {
		return
	}
	m.appErrorsFingerprintCacheHits.Inc()
}

// ObserveAppErrorsDedupeMerge increments the server-side
// dedupe-merge counter (ADR-096). Fires once per ON CONFLICT
// DO UPDATE that bumps an existing row's count + last_seen_at.
// nil-safe.
func (m *OpsMetrics) ObserveAppErrorsDedupeMerge() {
	if m == nil || m.appErrorsDedupeMerges == nil {
		return
	}
	m.appErrorsDedupeMerges.Inc()
}

// ObserveAppErrorsFlushDuration records one sample of the
// gatewayd-internal publisher's per-flush wall-clock duration
// (ADR-096). Callers should pass seconds. nil-safe — no-op if
// m is nil.
func (m *OpsMetrics) ObserveAppErrorsFlushDuration(seconds float64) {
	if m == nil || m.appErrorsFlushDuration == nil {
		return
	}
	m.appErrorsFlushDuration.Observe(seconds)
}

// ObserveAppErrorsPurge records one observation of the apid
// retention cron (ADR-096). outcome ∈ {ok, no_accounts,
// failed}. nil-safe — no-op if m is nil or the metric was
// pre-instantiated without labels. The closed outcome set keeps
// cardinality bounded.
func (m *OpsMetrics) ObserveAppErrorsPurge(outcome string) {
	if m == nil || m.appErrorsPurges == nil {
		return
	}
	m.appErrorsPurges.WithLabelValues(outcome).Inc()
}

// ObservePreviewJanitor records one observation of the apid
// preview teardown cron (ADR-095 PR-C / issue #272). outcome ∈
// {ok, failed, torn_down}. nil-safe — no-op if m is nil or the
// metric was pre-instantiated without labels. The closed outcome
// set keeps cardinality bounded per the ObserveAppErrorsPurge
// convention (ADR-096 retention cron shares the same pattern).
func (m *OpsMetrics) ObservePreviewJanitor(outcome string) {
	if m == nil || m.previewJanitorOutcomes == nil {
		return
	}
	m.previewJanitorOutcomes.WithLabelValues(outcome).Inc()
}

// GuestInitDuration returns the {(app, runner)}-labeled histogram
// observer for guest-init boot duration (issue #470 / PR C / ADR-074).
// The empty-tuple sentinel ("", "") is pre-instantiated at boot so
// dashboards render from a fresh process. nil-safe — returns nil if
// m is nil (callers must use Observe with nil-safe wrappers if they
// need to).
func (m *OpsMetrics) GuestInitDuration(app, runner string) prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.guestInitDuration.WithLabelValues(app, runner)
}

// WakeRPCDuration (ADR-097, P1B) returns the {(app, phase)}-labeled
// histogram observer for schedd-side wake-phase duration. phase is
// the closed set {admit_to_rpc, rpc_call, rpc_to_running}; callers
// attach wake_id as a prometheus.Exemplar via ObserveWithExemplar
// on the returned observer. The accessor is nil-safe — returns nil
// on a nil receiver so Engine unit tests that construct the engine
// without metrics keep working (mirrors GuestInitDuration above).
//
// Distinct from the legacy WakePhaseDuration(phase, result string)
// at metrics.go:3783 (events platform, ADR-064, {phase, result}
// labels, full wake envelope). The two metrics cover different
// windows: wake_rpc_duration_seconds is the schedd-side RPC path
// only; wake_phase_duration_seconds covers every wake phase from
// queue→admit through proxy first byte. They are correlated by
// wake_id exemplar on the schedd-side observations.
//
// Naming the new accessor WakeRPCDuration avoids the collision
// with the legacy WakePhaseDuration accessor — both surface
// prometheus.Observer, but the new one carries a wake_id exemplar
// obligation that the legacy one does not.
func (m *OpsMetrics) WakeRPCDuration(app, phase string) prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.wakeRPCDuration.WithLabelValues(app, phase)
}

// WakeSnapshotTier returns the per-tier counter the engine increments
// on every wake (issue #470 / PR C / ADR-074). The closed set
// {warm, init, cold_boot_fallback} is pre-instantiated at boot so the
// wake-tier-mix Grafana panel has zero rows from idle fleet.
func (m *OpsMetrics) WakeSnapshotTier(tier string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.wakeSnapshotTier.WithLabelValues(tier)
}

// WakeFailure returns the per-(box, app, reason) counter the
// wake-failure hook sites increment on every wake failure
// (issue #1059 / ADR-127, §3.5 per-app split). reason MUST be
// one of {snapshot_stale, disk_full, jailer_fail, netns_fail,
// cgroup_fail, vsock_fail, snapshot_restore_err,
// mem_backend_err} — the closed vocabulary is enforced by
// pkg/fcvm/wake_classify.go and the wake-failure call sites
// hardcode the literal reason string. box is resolved through
// the boxLabelSet admission (maxBoxLabelValues = 64);
// overflow collapses to "__other__". An empty box argument
// resolves to BoxHostname() so call sites can pass "" to
// inherit the hostname-derived label (issue #1059 / ADR-127
// §3.4 follow-up — cluster A commit 5 of the platform-
// observability mega-PR). app is resolved through the
// appLabelSet admission (maxAppLabelValues = 256); empty
// input collapses to labelAppUnknown ("") to distinguish a
// missing-app-slug path from a real app slug that hit the
// admission cap (which collapses to otherAppLabel). The
// (box, app, reason) cartesian is pre-instantiated in the
// constructor for every pair in the closed set (the reserved
// {labelLocal, otherBoxLabel} × {labelAppUnknown, otherAppLabel}
// × 10 reasons matrix — 40 series from idle fleet) so the §12
// "Wake failures by reason (24h)" dashboard panel surfaces
// zero rows from an idle fleet. nil-safe — returns nil if m
// is nil so unit tests without metrics keep building (same
// nil-safe posture as WarmSnapshotErrors).
func (m *OpsMetrics) WakeFailure(box, app, reason string) prometheus.Counter {
	if m == nil {
		return nil
	}
	if box == "" {
		box = BoxHostname()
	}
	return m.wakeFailure.WithLabelValues(m.boxLabel(box), m.appLabel(app), reason)
}

// WakeLatency returns the per-(box, phase) histogram observer the
// per-box wake-path hook sites call to record wake-phase latency
// (issue #1059 / ADR-127). phase MUST be one of {restore_ms,
// netns_tap_ms, guest_ready_ms} — the closed vocabulary mirrors the
// existing fleet-level vmmd_wake_phase_duration_seconds{phase}
// histogram so the §12 dashboard panel can swap fleet → per-box
// without a legend change. box is resolved through the boxLabelSet
// admission (same contract as WakeFailure above); an empty box
// argument resolves to BoxHostname() (issue #1059 / ADR-127 §3.4
// follow-up — cluster A commit 5 of the platform-observability
// mega-PR). The (box, phase) cartesian is pre-instantiated in the
// constructor for every pair so the "Per-box p99 wake latency
// (24h)" dashboard panel surfaces zero rows from idle fleet.
// wake_id is attached as a prometheus.Exemplar at the call site
// (matching the wakeRPCDuration precedent at metrics.go:1421).
// nil-safe — returns nil if m is nil.
func (m *OpsMetrics) WakeLatency(box, phase string) prometheus.Observer {
	if m == nil {
		return nil
	}
	if box == "" {
		box = BoxHostname()
	}
	return m.wakeLatency.WithLabelValues(m.boxLabel(box), phase)
}

// EvictedPriority returns the per-(priority, reason) counter the
// reaper increments after every successful park (issue #475).
// priority ∈ {best_effort, reserved} (matches pkg/api/dto.go's
// EvictionPriority enum); reason ∈ {idle, eviction_aggressive,
// eviction_ram}. The closed (priority × reason) label set is
// pre-instantiated at boot so the §12 eviction-by-tier panel has
// zero rows from idle fleet and non-zero rows as soon as the
// reaper parks any instance. The helper is nil-safe so a unit test
// without metrics keeps building.
func (m *OpsMetrics) EvictedPriority(priority, reason string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.evictedPriority.WithLabelValues(priority, reason)
}

// GuestTailSeconds returns the per-(plan, runtime, outcome)
// histogram the runner's tail host observes once per terminal
// tail event (issue #667 / ADR-078). plan ∈ api.Plans (4),
// runtime ∈ {node22, node24, python312, python313, go124} (5;
// hard-coded per ADR-052), outcome ∈ {completed, failed,
// timeout} (3). All three axes are closed sets; the Cartesian
// is pre-instantiated at boot so the registry never sees an
// unknown label and the §12 tail-latency panel has zero rows
// from idle fleet and non-zero rows as soon as a tail drains.
// The returned Observer is safe to retain; nil-safe on receiver
// so a unit test without metrics keeps building.
func (m *OpsMetrics) GuestTailSeconds(plan, runtime, outcome string) prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.guestTailSeconds.WithLabelValues(plan, runtime, outcome)
}

// GuestTailFailedTotal returns the per-(plan, reason) counter
// the runner's tail host (and the schedd watchdog at park)
// increments once per failed tail event (issue #667 /
// ADR-078). plan ∈ api.Plans (4), reason ∈ {timeout,
// handler_error, forced_at_park, unknown} (4; hard-coded).
// The closed (plan × reason) set is pre-instantiated at boot
// so the §12 tail-failure panel has zero rows from idle fleet
// and non-zero rows as soon as any tail fails. The returned
// Counter is safe to retain; nil-safe on receiver so a unit
// test without metrics keeps building.
func (m *OpsMetrics) GuestTailFailedTotal(plan, reason string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.guestTailFailedTotal.WithLabelValues(plan, reason)
}

// PlanGateRescuedByExclude returns the per-(plan, reason) counter
// the apid scan service increments when --exclude flipped a
// blocked pre-exclude gate to allowed (ADR-124 follow-up #2).
// plan ∈ api.Plans (4; pre-instantiated). reason ∈
// {apps_over_limit, crons_over_limit, crons_not_allowed} — see
// cmd/apid/scan_service.go::gateRescueReason for the canonical
// bucketing. The §12 gate-rescue panel keys off this series;
// a sustained non-zero rate per (plan, reason) means customers
// routinely bump plan caps and the --exclude workaround path
// is being exercised. The returned Counter is safe to retain;
// nil-safe on receiver so a unit test without ops keeps building.
func (m *OpsMetrics) PlanGateRescuedByExclude(plan, reason string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.planGateRescuedByExclude.WithLabelValues(plan, reason)
}

// TailCapReached returns the per-(plan) counter the runner
// increments when a customer tries to register the
// (ConcurrentTailsPerInstance + 1)-th tail (issue #667 /
// ADR-078). plan ∈ api.Plans (4; pre-instantiated). The §12
// cap-pressure panel keys off this series — non-zero values
// mean the customer is bumping into the per-instance cap and
// should consider raising the plan. The returned Counter is
// safe to retain; nil-safe on receiver so a unit test without
// metrics keeps building.
func (m *OpsMetrics) TailCapReached(plan string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.tailCapReached.WithLabelValues(plan)
}

// RebalanceDecisions returns the per-(outcome) counter the Tier
// A4 cross-node rebalancer (ADR-064) increments once per app
// per drain-event. outcome ∈ {migrated, conflict, no_headroom,
// cooldown, no_eligibility}. Same caching rules as
// WatchdogKills — the returned Counter is safe to retain.
func (m *OpsMetrics) RebalanceDecisions(outcome string) prometheus.Counter {
	return m.rebalanceDecisions.WithLabelValues(outcome)
}

// AppAtCapacityTotal returns the per-(app, kind) counter the
// engine increments at every WakeResult{AtCapacity: true} return
// site (Tier A9 / ADR-087). app is the customer app id; kind ∈
// {wake, admit, scaleup, floor} is the engine branch that
// produced the no-op. The per-app rate is the §12 dashboard
// panel for the pressure-rebalancer trigger. Same caching rules
// as RebalanceDecisions — the returned Counter is safe to retain.
// Nil-safe on receiver so a unit test without metrics keeps
// building.
func (m *OpsMetrics) AppAtCapacityTotal(app, kind string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.appAtCapacityTotal.WithLabelValues(app, kind)
}

// PressureReassignments returns the per-(outcome) counter the
// Tier A9 / ADR-087 pressure-rebalancer increments once per app
// per sweep. outcome ∈ {migrated, conflict, no_headroom,
// no_eligibility, no_peer, overflow_target_unavailable}.
// `migrated` is the §12 dashboard panel;
// `overflow_target_unavailable` is the Tier A10 / ADR-088
// tripwire when the customer's preferred spill target is full
// or inactive. The `peer_live_migrated` label was removed in
// the Tier A10 follow-up PR because the
// maybeMigrateLiveInstancesFor helper it gated always no-op'd
// (passed e.ownerNodeID as deadNodeID, which
// MigrateLiveInstances self-skips at engine.go:2944); Tier
// A10.1 will re-introduce it once a true peer-to-peer migrator
// is wired. `no_headroom` is the tripwire for sustained
// full-cluster pressure (call the operator). Same caching
// rules as RebalanceDecisions — the returned Counter is safe
// to retain.
func (m *OpsMetrics) PressureReassignments(outcome string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.pressureReassignmentsTotal.WithLabelValues(outcome)
}

// OverflowTargetSpillHits returns the per-(outcome) counter the
// Tier A10 / ADR-088 pressure-rebalancer increments once per app
// per sweep when an overflow_node preference was consulted.
// outcome ∈ {used, unavailable, fallback_used}. `used` is the
// happy path (the preferred node had headroom and the engine
// reassigned to it); `unavailable` is the spill-out tripwire
// (the preferred node was inactive, full, or gone — the engine
// fell through to the A9 first-peer-with-headroom path);
// `fallback_used` answers "did the fallback save the sweep?"
// after an `unavailable` observation. A high `unavailable` rate
// with a low `fallback_used` rate is the sustained-full-cluster-
// pressure tripwire. Same caching rules as PressureReassignments
// — the returned Counter is safe to retain.
func (m *OpsMetrics) OverflowTargetSpillHits(outcome string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.overflowTargetSpillHitsTotal.WithLabelValues(outcome)
}

// LiveMigrationDecisions returns the labelled counter for the
// four-phase cross-node LIVE-instance handoff (Tier A5 / ADR-066).
// Called from Engine.MigrateLiveInstances once per candidate
// instance outcome (mirrors the rebalanceDecisions dispatch shape).
// outcome ∈ {migrated, conflict, no_headroom, no_eligibility,
// lease_expired, peer_failure}. The returned Counter is safe to
// retain — Prometheus's WithLabelValues is internally cached, so a
// per-batch loop that fires 100 calls/sec doesn't allocate.
func (m *OpsMetrics) LiveMigrationDecisions(outcome string) prometheus.Counter {
	return m.liveMigrationDecisions.WithLabelValues(outcome)
}

// MigratingReconcileDecisions returns the labelled counter for the
// Tier A6 / ADR-067 migrating-instance watchdog. Called from
// Engine.ReconcileExpiredMigrations once per stuck state='migrating'
// row (mirrors the rebalanceDecisions / liveMigrationDecisions
// dispatch shape). outcome ∈ {reinvited, hard_deleted, conflict,
// error}. The returned Counter is safe to retain — same caching
// rules as LiveMigrationDecisions.
func (m *OpsMetrics) MigratingReconcileDecisions(outcome string) prometheus.Counter {
	return m.migratingReconcileDecisions.WithLabelValues(outcome)
}

// ActivePassiveFailovers returns the labelled counter for the
// Tier A8 / ADR-083 active-passive HA fail-over. Called from the
// dns_handoff orchestrator (cmd/gatewayd-public/dns_handoff.go) once
// per drain outcome. outcome ∈ {dns_flipped, dns_stale,
// peer_unreachable, manual_drain}. The returned Counter is safe to
// retain — same caching rules as the other WithLabelValues
// accessors. The counter is on the shared OpsMetrics struct
// (single-registry pattern) so /metrics doesn't show a 404 for
// cmd/<other> scrapes that incidentally probe the prefix.
func (m *OpsMetrics) ActivePassiveFailovers(outcome string) prometheus.Counter {
	return m.activePassiveFailoversTotal.WithLabelValues(outcome)
}

// StandbyState enum (ADR-083 / Tier A8 / §14 M8). Closed vocabulary
// the §12 Prometheus rules and docs/runbooks/active-passive-ha.md
// escalate against. The terminal values (Drained / Failed) are the
// audit's fix #1: without them the gauge sticks at Draining and the
// runbook-implied `FaasStandbyStateDrainingTooLong` alert cannot
// fire. Use the named constants instead of raw ints everywhere in
// the active-passive HA path.
const (
	// StandbyStateWarming is the boot value; set at
	// metrics.NewOpsMetrics time and held until the WarmupLoop's
	// first tick completes. FaasStandbyStateWarmingTooLong fires
	// if the gauge holds at 1 for > 60s on a node where
	// compute_nodes.active=true.
	StandbyStateWarming = 1
	// StandbyStateWarm is the steady state on both the active and
	// the standby-warm box. The standby holds at 2 forever; the
	// active transitions 2→3 on drain.
	StandbyStateWarm = 2
	// StandbyStateDraining is the in-flight drain. Held until the
	// orchestrator either succeeds (→Drained) or exhausts retries
	// (→Failed). FaasStandbyStateDrainingTooLong fires if the
	// gauge holds at 3 for > HADNSRecordStaleSeconds + backoff
	// slack.
	StandbyStateDraining = 3
	// StandbyStateDrained is the terminal success state — DNS
	// flipped, in-flight reached zero inside the budget, the box
	// is safe to shut down. This is the value the manual drain
	// path waits for before SIGKILLing the listener.
	StandbyStateDrained = 4
	// StandbyStateFailed is the terminal failure state — DNS
	// provider exhausted retries OR pg_notify peer_unreachable
	// stuck inside the budget. The box stays in the fleet as a
	// standby-only contributor (the orchestrator does NOT retry
	// on its own — operator intervention is required per the
	// runbook's escalation section).
	StandbyStateFailed = 5
)

// SetStandbyState stamps the enum gauge to the current StandbyState.
// Called from cmd/gatewayd-public on every active-passive HA state
// transition (boot→warming→warm→draining→drained|failed). Unlabelled;
// the gauge is the per-node state signal — fan-out is by Prometheus
// scrape target. The `FaasStandbyStateWarmingTooLong` alert queries
// this gauge; the runbook's escalation section covers the
// investigation path (DNS provider, store connectivity, peer
// network).
func (m *OpsMetrics) SetStandbyState(s int) {
	m.standbyStateMu.Lock()
	m.standbyStateValue = s
	m.standbyStateMu.Unlock()
	m.standbyState.Set(float64(s))
}

// StandbyState returns the current enum-gauge value. Mirrors the
// shadow-bool pattern AlertEvaluatorEnabled uses for the alert
// evaluator (no prometheus.Gauge.Value() accessor exists); the
// shadow is stamped on every SetStandbyState call so the read
// path doesn't scrape /metrics. Used by cmd/gatewayd-public
// startup assertions (the gauge MUST be 1 on boot — see
// pkg/wire/metrics_test.go).
func (m *OpsMetrics) StandbyState() int {
	m.standbyStateMu.Lock()
	defer m.standbyStateMu.Unlock()
	return m.standbyStateValue
}

// DeadNodeReconcileDecisions returns the labelled counter for the
// dead-node billing reconciler. Called from
// Engine.ReconcileDeadNodeInstances once per RUNNING row whose owning
// compute_node stopped heartbeating past the staleness window.
// outcome ∈ {failed, conflict, error}. Same caching rules as
// MigratingReconcileDecisions.
func (m *OpsMetrics) DeadNodeReconcileDecisions(outcome string) prometheus.Counter {
	return m.deadNodeReconcileDecisions.WithLabelValues(outcome)
}

// EventsWriteFailures returns the unlabelled counter for audit-log
// writes that failed. The transition itself succeeded; this counter
// only signals observability debt. See also commit 4.
func (m *OpsMetrics) EventsWriteFailures() prometheus.Counter {
	return m.eventsWriteFail
}

// AuditWriteFailures returns the per-account counter for IAM-4
// (ADR-035) auth audit emits whose events row could not be written
// (issue #278). The handler has already returned 200 to the
// customer; this counter only signals observability debt. Same
// posture as EventsWriteFailures.
//
// accountID flows through the bounded admission set (accountLabel):
// empty/nil resolves to "anonymous" (unauthenticated, never billed);
// new ids above the capacity return the counter labelled "__other__"
// so the Prometheus TSDB series set stays bounded. Repeated calls
// for the same accountID return the same underlying Counter — safe
// to call from the hot path.
func (m *OpsMetrics) AuditWriteFailures(accountID string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.auditWriteFail.WithLabelValues(m.accountLabel(accountID))
}

// AccountOrgMismatch returns the per-kind account/org-mirror mismatch
// counter (issue #190, ADR-061). kind ∈ {plan, status,
// provider_customer_id, stripe_subscription_item} — the closed set of
// mirrored fields. PR 3 registers the counter; PR 4 (handlers) and
// PR 6 (billing webhook) call this from the dual-write paths. The
// closed label set is pre-instantiated at boot so /metrics surfaces
// zero from the first scrape. PR 7's cutover gate
// ("rate() == 0 for ≥1 metering cycle") reads this counter.
func (m *OpsMetrics) AccountOrgMismatch(kind string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.accountOrgMismatch.WithLabelValues(kind)
}

// AuditWriteFailureDuration returns the per-result observer for the
// audit-write latency histogram (issue #278). result ∈ {ok, failed};
// "ok" is the AppendEvent-success branch, "failed" is the AppendEvent
// failure branch. The histogram covers the single-row INSERT
// round-trip so an operator can distinguish a Postgres outage (slow
// AppendEvent, many failures) from a transient insert race (fast
// failures). Safe to cache; the underlying HistogramVec is shared.
func (m *OpsMetrics) AuditWriteFailureDuration(result string) prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.auditWriteDur.WithLabelValues(result)
}

// AuditLogWriteTotal returns the per-(endpoint, kind) counter for
// successful events-table appends (PR-#TBD / C5). endpoint ∈
// {apid, schedd, meterd, gatewayd-internal}; kind is the closed
// operator-action vocabulary + "other" overflow (see
// NewOpsMetrics pre-instantiation block). nil-safe.
func (m *OpsMetrics) AuditLogWriteTotal(endpoint, kind string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.auditLogWriteTotal.WithLabelValues(endpoint, kind)
}

// AuditLogWriteFailuresTotal returns the per-(endpoint, kind,
// error_class) counter for failed events-table appends
// (PR-#TBD / C5). error_class ∈ {sqlstate_23514, sqlstate_23505,
// timeout, other}. nil-safe.
func (m *OpsMetrics) AuditLogWriteFailuresTotal(endpoint, kind, errorClass string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.auditLogWriteFailuresTotal.WithLabelValues(endpoint, kind, errorClass)
}

// SetOperatorActionTraceCompleteness sets the per-kind gauge to
// the 5-minute trailing ratio (0.0..1.0) of operator.action.<verb>*
// audit rows whose events.trace_id column is non-NULL (PR-#TBD /
// C5). Set by schedd's 60s completeness tick via a single SQL
// aggregation. nil-safe.
func (m *OpsMetrics) SetOperatorActionTraceCompleteness(kind string, ratio float64) {
	if m == nil {
		return
	}
	m.operatorActionTraceCompletenessRatio.WithLabelValues(kind).Set(ratio)
}

// MarkOperatorActionTraceCompletenessFirstTickCompleted records the first
// successful completeness observation exactly once. nil-safe.
func (m *OpsMetrics) MarkOperatorActionTraceCompletenessFirstTickCompleted() {
	if m == nil {
		return
	}
	m.operatorActionTraceCompletenessFirstTickOnce.Do(func() {
		m.operatorActionTraceCompletenessFirstTickCompleted.Inc()
	})
}

// SetOperatorActionTraceCompletenessLastSuccess records the wall-clock time
// of a successful completeness observation. nil-safe.
func (m *OpsMetrics) SetOperatorActionTraceCompletenessLastSuccess(t time.Time) {
	if m == nil {
		return
	}
	m.operatorActionTraceCompletenessLastSuccessTimestamp.Set(float64(t.UnixNano()) / float64(time.Second))
}

// CronFireNowDispatchDuration returns the per-result observer for
// the cron fire-now dispatch-latency histogram (issue #791 PR-D /
// ADR-090 §Sub-decision 7). result ∈ {succeeded, failed}; the schedd
// dispatch path emits one observation per terminal request row.
// Safe to call from the cron-fire dispatch loop — the underlying
// HistogramVec is shared and pre-instantiated at boot, so /metrics
// surfaces the histogram's HELP/TYPE from the first scrape even
// before any cron has fired.
func (m *OpsMetrics) CronFireNowDispatchDuration(result string) prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.cronFireNowDispatchDur.WithLabelValues(result)
}

// FailedLoginTotal returns the per-IP counter for failed login
// attempts on the dashboard auth surface (issue #286, SOC 2 CC7.2).
// The IP is resolved through the bounded admission set (ipLabel):
// empty or "unknown" collapses to "anonymous" (unparseable IP);
// new IPs past the capacity return the counter labelled "__other__"
// so the Prometheus TSDB series set stays bounded under a
// credential-stuffing burst. Backs the FaasFailedLoginSpike alert
// at 20/min/IP/5m. Repeated calls for the same IP return the same
// underlying Counter — safe to call from the hot path.
func (m *OpsMetrics) FailedLoginTotal(ip string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.failedLoginTotal.WithLabelValues(m.ipLabel(ip))
}

// FailedLoginDropped returns the unlabelled counter for failed-login
// audit rows that the async-batched channel could not enqueue
// (issue #286). A non-zero rate is the canary for "audit flusher is
// the bottleneck under a credential-stuffing burst". The auth
// response is unaffected — the channel write is non-blocking and the
// 401 returns regardless.
func (m *OpsMetrics) FailedLoginDropped() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.failedLoginDropped
}

// FailedLoginAuditWriteFailures returns the unlabelled counter for
// failed-login audit rows whose AppendEvent could not be written
// (issue #286, cmd/apid/audit.go::flushOne). Distinct from the
// success-path audit counter (which is labelled by account_id) —
// routing this counter through AuditWriteFailures would conflate
// "the failed-login row's subject is always nil" with "the success-
// path caller's subject is empty" in the operator's anonymous
// bucket. Unlabelled — the per-IP breakage is not observable on
// this path. Backs the SOC 2 CC7.2 audit-write-failure signal
// alongside apid_audit_write_failures_total{account_id}.
func (m *OpsMetrics) FailedLoginAuditWriteFailures() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.failedLoginAuditWriteFailures
}

// AuditEventsDeleted returns the unlabelled counter for audit-
// event rows pruned by the daily retention pass (pkg/eventretention,
// ADR-075). Distinct from the per-kind volume counter so an operator
// can see "is the prune loop running AND is it making progress"
// without conflating emit rate with delete rate. Backs the
// docs/runbooks/FaasAuditRetentionExhaustion.md runbook alongside
// AuditEventsRetentionLag. ADR-091 D20.3 / PR-B residual.
//
// Also satisfies eventretention.Ops — the cleanup loop calls
// .Add(float64(n)) on the returned counter (only when n > 0 so
// idle passes don't tick up).
func (m *OpsMetrics) AuditEventsDeleted() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.auditEventsDeletedTotal
}

// DeploymentAuditGCRowsDeleted returns the counter of rows
// pruned by the deployment_audit 90-day retention cron
// (pkg/meter.RetentionOnceDeploymentAudit, SAFE-RELEASES
// production-leveling Stream D). The counter is unlabelled —
// the loop has one outcome (rows deleted); per-kind breakdown
// stays on the deployment_audit_at_gc_idx index. cmd/meterd
// calls .Add(float64(n)) on the returned counter after each
// successful pass (only when n > 0 so idle passes don't tick
// up — same precedent as AuditEventsDeleted). Nil-safe —
// single-registry pattern, daemons that don't wire the
// retention loop (apid) still expose a nil-returning accessor.
func (m *OpsMetrics) DeploymentAuditGCRowsDeleted() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.deploymentAuditGCRowsDeletedTotal
}

// AuditEventsRetentionLag returns the gauge of seconds since
// the most recent retention cutoff was computed. Set (not Inc) —
// the value reflects the LATEST pass. A pinned-zero value is the
// canary for "the loop is running but never deleting anything"
// (events table growing). ADR-091 D20.3 / PR-B residual.
//
// Also satisfies eventretention.Ops — the cleanup loop calls
// .Set(seconds) on the returned gauge on every successful pass
// (so pinned-zero on an idle pass is a red flag).
func (m *OpsMetrics) AuditEventsRetentionLag() prometheus.Gauge {
	if m == nil {
		return nil
	}
	return m.auditEventsRetentionLagSeconds
}

// DomainDoctorOldestObservationSeconds (ADR-120 Tier A1) returns
// the apid_domain_doctor_oldest_observation_seconds gauge. The
// dns_poller (cmd/apid/dns_poller.go::emitDoctorOldestObservationGauge)
// calls Set(age) after every runDoctorOnce pass so the
// FaasDomainDoctorStalled / FaasDomainDoctorStretched alerts can
// page on a stalled loop. Safe to call when m is nil — the dns_poller
// also nil-checks s.ops before calling here.
func (m *OpsMetrics) DomainDoctorOldestObservationSeconds() prometheus.Gauge {
	if m == nil {
		return nil
	}
	return m.domainDoctorOldestObservationSeconds
}

// DomainDoctorSkippedFlagDisabled (ADR-120 Tier A1) returns the
// apid_domain_doctor_skipped_flag_disabled_total counter so the
// dns_poller can bump it once per tick when
// FAAS_DOMAIN_DOCTOR_ENABLED is off. Nil-safe (returns a no-op
// counter when m is nil — the dns_poller also nil-checks s.ops).
func (m *OpsMetrics) DomainDoctorSkippedFlagDisabled() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.domainDoctorSkippedFlagDisabled
}

// DebugRegressionOldestPassSeconds (ADR-127 PR-B) returns the
// apid_debug_regression_oldest_pass_seconds gauge. The regression
// cron (cmd/apid/debug_regression_cron.go::emitRegressionOldestPassGauge)
// calls Set(age) after every runRegressionOnce pass so the
// FaasDebugRegressionStalled alert can page on a stalled loop.
// Nil-safe (returns nil when m is nil — the cron also nil-checks
// s.ops before calling here).
func (m *OpsMetrics) DebugRegressionOldestPassSeconds() prometheus.Gauge {
	if m == nil {
		return nil
	}
	return m.debugRegressionOldestPassSeconds
}

// DebugRegressionSkippedFlagDisabled (ADR-127 PR-B) returns the
// apid_debug_regression_skipped_flag_disabled_total counter so the
// regression cron can bump it once per tick when every account
// has DebugTelemetryEnabled=false OR the operator flipped
// FAAS_DEBUG_TELEMETRY_ENABLED=false. Nil-safe (returns a no-op
// counter when m is nil).
func (m *OpsMetrics) DebugRegressionSkippedFlagDisabled() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.debugRegressionSkippedFlagDisabled
}

// DebugRegressionDetected (ADR-127 Debugger UX v1) returns the
// apid_debug_regression_detected_total counter so the regression
// cron (cmd/apid/debug_regression_cron.go) can bump it after
// every successful UpsertRegressionObservation. A new
// regression fires once; an existing regression that persists
// across cron passes re-fires every 5m. Backs the
// FaasDebugRegressionDetected page-tier alert. Nil-safe
// (returns nil when m is nil — the cron also nil-checks s.ops).
func (m *OpsMetrics) DebugRegressionDetected() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.debugRegressionDetected
}

// AuditEventsVolumeTotal returns the per-kind-prefix counter for
// audit-event emit calls. kind_prefix is the bounded-admission
// label — overflow collapses to "__other__" via the wire admission
// helper so Prometheus series stay bounded. Safe to call from the
// emit hot path; the underlying CounterVec is shared and pre-
// instantiated at boot. ADR-091 D20.3 / PR-B residual.
func (m *OpsMetrics) AuditEventsVolumeTotal(kindPrefix string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.auditEventsVolumeTotal.WithLabelValues(kindPrefix)
}

// RequestFailure is the primitive counter accessor for
// apid_request_failures_total{account_id, route} (issue #278). It
// is exposed for unit tests that drive the metric directly — the
// canonical HTTP-path call site is RequestFailureFor, which owns
// the route-template extraction so callers cannot accidentally pass
// a raw URL path (that would explode the cardinality unbounded).
//
// route MUST be a Go mux pattern (e.g. "GET /v1/apps/{slug}") or
// the reserved sentinel "unmatched" for paths the mux did not
// dispatch. accountID flows through the bounded admission set
// (accountLabel) — empty resolves to "anonymous"; ids past the
// capacity collapse to "__other__".
func (m *OpsMetrics) RequestFailure(accountID, route string) prometheus.Counter {
	if m == nil {
		return nil
	}
	// Failures are by definition code="err"; the label is closed-set
	// and never varies. Callers that need code="ok" must use
	// RequestTotal, not RequestFailure.
	return m.requestFailures.WithLabelValues(m.accountLabel(accountID), route, "err")
}

// RequestFailureFor is the canonical accessor for the per-customer
// request-failure counter (issue #278; PR #336 added the `code` label
// to mirror requestTotal — see ADR-039). It extracts the route label
// from r.Pattern (the Go mux pattern, e.g. "GET /v1/apps/{slug}")
// with the reserved "unmatched" fallback for paths the mux did not
// dispatch — so the route label's cardinality is bounded by the
// route table and never by a URL path the scanner fed in.
//
// accountID is resolved through the bounded admission set: empty
// resolves to "anonymous" ; ids past the capacity collapse to
// "__other__". Safe on a nil receiver so callers can call it
// without a nil-check at the top of the helper, mirroring the
// Observe* family pattern. The failure counter's `code` label is
// closed-set at "err" — failures by definition have code="err";
// the canonical counter for any other status is RequestTotalFor,
// not RequestFailureFor.
func (m *OpsMetrics) RequestFailureFor(r *http.Request, accountID string) prometheus.Counter {
	if m == nil {
		return nil
	}
	route := r.Pattern
	if route == "" {
		route = "unmatched"
	}
	return m.RequestFailure(accountID, route)
}

// RequestTotal is the primitive counter accessor for
// apid_request_total{account_id, route, code} (issue #303, ADR-039).
// It is exposed for unit tests that drive the metric directly —
// the canonical HTTP-path call site is RequestTotalFor, which owns
// the route-template extraction so callers cannot accidentally pass
// a raw URL path (that would explode the cardinality unbounded).
//
// route MUST be a Go mux pattern (e.g. "GET /v1/apps/{slug}") or
// the reserved sentinel "unmatched" for paths the mux did not
// dispatch. code MUST be "ok" or "err"; see CodeFromStatus.
// accountID flows through the bounded admission set (accountLabel)
// — empty resolves to "anonymous"; ids past the capacity collapse to
// "__other__". Shares the same admission set as RequestFailure and
// AuditWriteFailures so a customer is represented by their real id
// in all three, or by "__other__" in all three.
func (m *OpsMetrics) RequestTotal(accountID, route, code string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.requestTotal.WithLabelValues(m.accountLabel(accountID), route, code)
}

// RequestTotalFor is the canonical accessor for the per-customer
// request-total counter (issue #303, ADR-039). It extracts the route
// label from r.Pattern (the Go mux pattern, e.g. "GET /v1/apps/{slug}")
// with the reserved "unmatched" fallback for paths the mux did not
// dispatch — so the route label's cardinality is bounded by the
// route table and never by a URL path the scanner fed in. The code
// label is derived from the response status via CodeFromStatus —
// the caller passes the status recorded on the response writer.
//
// accountID is resolved through the bounded admission set: empty
// resolves to "anonymous"; ids past the capacity collapse to
// "__other__". Safe on a nil receiver so callers can call it
// without a nil-check at the top of the helper, mirroring the rest
// of the Observe* family.
func (m *OpsMetrics) RequestTotalFor(r *http.Request, status int, accountID string) prometheus.Counter {
	if m == nil {
		return nil
	}
	route := r.Pattern
	if route == "" {
		route = "unmatched"
	}
	return m.RequestTotal(accountID, route, CodeFromStatus(status))
}

// ObserveTopTenantRPS records a 5s RPS sample for the given accountID
// into the bounded top-N admission primitive (issue #300). Cheap path:
// takes the topAccountSet lock, increments the count, releases. Does
// NOT touch the gauge — that happens once per 5s tick via
// EmitTopTenantRPS, called from the sampler goroutine.
//
// Why the split: a per-sample gauge Set would race under concurrent
// goroutines because the top-N membership bounces for any given id
// as more ids arrive. Pushing gauge emission to a single-goroutine
// once-per-tick snapshot bounds the gauge series set to at most
// cap + 1 — see pkg/wire/topn.go for the design note.
//
// accountID is the raw (pre-admission) account id. The accessor
// routes it through accountLabelSet so the 10k overflow collapses
// to "__other__" upstream of the top-N primitive.
//
// Safe on a nil receiver.
func (m *OpsMetrics) ObserveTopTenantRPS(accountID string) {
	if m == nil || m.topAccounts == nil {
		return
	}
	safe := m.accountLabel(accountID)
	m.topAccounts.sample(safe)
}

// EmitTopTenantRPS drives the gauge emission from the sampler
// goroutine's once-per-tick snapshot. Reads topNSnapshot, sets one
// gauge row per tuple, and emits the "other" overflow row for any
// account not in the top-N (this collapses the per-tick overflow
// into the pre-instantiated "other" series).
//
// perAccountRPS is a closure that returns the current 5s rps for
// the given account id (e.g. from the underlying requestTotal
// delta). It is called once per tuple in the snapshot.
//
// Returns the number of series emitted (always ≤ cap + 1).
//
// Safe on a nil receiver — returns 0.
func (m *OpsMetrics) EmitTopTenantRPS(perAccountRPS func(accountID string) float64) int {
	if m == nil || m.topAccounts == nil || m.topTenantRPS == nil {
		return 0
	}
	snap := m.topAccounts.topNSnapshot()
	for _, c := range snap {
		m.topTenantRPS.WithLabelValues(c.id).Set(perAccountRPS(c.id))
	}
	// Always emit the "other" row so the panel selector
	// {account_id!="other"} never sees "no data" — same precedent
	// as every other CounterVec pre-instantiation in NewOpsMetrics.
	m.topTenantRPS.WithLabelValues(topAccountOtherLabel).Set(perAccountRPS(topAccountOtherLabel))
	return len(snap) + 1
}

// TopAccountSet exposes the bounded admission primitive so the sampler
// goroutine can drive its 24h rolling-window reset without going
// through the gauge accessor. Returns nil on a nil receiver so unit
// tests can nil-check.
func (m *OpsMetrics) TopAccountSet() *topAccountSet {
	if m == nil {
		return nil
	}
	return m.topAccounts
}

// TopAppSet exposes the bounded admission primitive for the per-app
// throttle counter (issue #301, ADR-044). Same nil-safe contract as
// TopAccountSet. The return type is the sibling *topAppSet; the
// sampler calls into ShouldReset/ResetWindow/SnapshotAppCounts
// instead of the topAccountSet methods.
func (m *OpsMetrics) TopAppSet() *topAppSet {
	if m == nil {
		return nil
	}
	return m.topApps
}

// AppKeyForTest builds the composite (account_id, app_id) key
// matching what the rollup uses internally. Test-only seam
// (issue #301 / ADR-043) so pkg/sched/instancestats/poller_test.go
// can index SnapshotAppCounts() without exporting the unexported
// appKey struct to the public surface. Mirrors the
// TestAdvanceAppClock / ThrottleSecondsLastSeenForTest pattern:
// same package-private access via thin helpers rather than
// exporting the type.
func (m *OpsMetrics) AppKeyForTest(accountID, appID string) appKey {
	return appKey{accountID: accountID, appID: appID}
}

// ShouldReset returns true if the rolling 24h window has elapsed
// since the last resetWindow. Cheap read; called from the 5s
// sampler tick. Forwarded from *topAppSet so the sampler stays
// decoupled from the primitive's unexported state.
func (s *topAppSet) ShouldReset() bool {
	if s == nil {
		return false
	}
	return s.shouldReset()
}

// ResetWindow wipes the rolling-window counts and updates
// lastReset. Called by the sampler goroutine every 24h. Nil-safe.
func (s *topAppSet) ResetWindow() {
	if s == nil {
		return
	}
	s.resetWindow()
}

// SnapshotAppCounts returns a copy of the current per-(account_id,
// app_id) rolling counts. Used by the vmmd-side sampler
// (cmd/vmmd/throttle_sampler.go, fed via ReplaceInstanceStats) to
// compute the per-tick delta between successive ticks. The
// returned map is a fresh allocation; callers may mutate it freely.
func (s *topAppSet) SnapshotAppCounts() map[appKey]uint64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[appKey]uint64, len(s.counts))
	for k, count := range s.counts {
		out[k] = count
	}
	return out
}

// ObserveTopAppThrottle records a throttle observation for the
// given (accountID, appID) into the bounded top-N admission
// primitive (issue #301, ADR-044). Cheap path: takes the topAppSet
// lock, increments the count, releases. Does NOT touch the counter
// — that happens once per 5s tick via EmitTopAppThrottle, called
// from the vmmd-side sampler.
//
// accountID flows through accountLabelSet so the 10k overflow
// collapses to "__other__" upstream of the top-N primitive. Empty
// or nil accountID resolves to "anonymous" (mirroring the
// requestTotal overflow policy) so a missing owner doesn't explode
// the wire surface.
//
// Safe on a nil receiver.
func (m *OpsMetrics) ObserveTopAppThrottle(accountID, appID string) {
	if m == nil || m.topApps == nil {
		return
	}
	safe := m.accountLabel(accountID)
	m.topApps.sample(safe, appID)
}

// EmitTopAppThrottle drives the counter emission from the sampler
// goroutine's once-per-tick snapshot. Reads topNSnapshot, adds one
// counter delta per tuple, and emits the ("other", "other")
// overflow row for any (account_id, app_id) not in the top-N.
// perAppThrottleSeconds is a closure that returns the current tick's
// throttle-seconds for the given pair (e.g. from the underlying
// throttleSecondsLastSeen baseline).
//
// Returns the number of series emitted (always ≤ cap + 1).
//
// Safe on a nil receiver — returns 0.
func (m *OpsMetrics) EmitTopAppThrottle(perAppThrottleSeconds func(accountID, appID string) float64) int {
	if m == nil || m.topApps == nil || m.throttleSecondsTotal == nil {
		return 0
	}
	snap := m.topApps.topNSnapshot()
	for _, c := range snap {
		delta := perAppThrottleSeconds(c.accountID, c.appID)
		if delta > 0 {
			m.throttleSecondsTotal.WithLabelValues(c.accountID, c.appID).Add(delta)
		}
	}
	// Always emit the ("other", "other") overflow row so the
	// panel selector {app_id!="other"} never sees "no data".
	overflow := perAppThrottleSeconds(topAppOtherAccountLabel, topAppOtherLabel)
	if overflow > 0 {
		m.throttleSecondsTotal.WithLabelValues(topAppOtherAccountLabel, topAppOtherLabel).Add(overflow)
	}
	return len(snap) + 1
}

// ThrottleSecondsLastSeenForTest exposes the per-(account_id,
// app_id) baseline microseconds the rollup last observed via
// ReplaceInstanceStats. Test-only seam (issue #301 / ADR-043)
// used by pkg/sched/instancestats/poller_test.go to assert the
// schedd-side poller correctly fed the baseline after decoding
// the wire field CpuThrottledSeconds. The empty-account case
// resolves through the "anonymous" admission label so the test
// sees the same key the rollup uses internally.
//
// Returns 0 if the pair has never been observed (no prior
// baseline — the wire's first sample is the canonical
// "no-baseline-yet" state).
//
// Nil-safe.
func (m *OpsMetrics) ThrottleSecondsLastSeenForTest(accountID, appID string) float64 {
	if m == nil || m.throttleSecondsLastSeen == nil {
		return 0
	}
	if accountID == "" {
		accountID = "anonymous"
	}
	m.throttleSecondsLastSeen.mu.Lock()
	defer m.throttleSecondsLastSeen.mu.Unlock()
	return m.throttleSecondsLastSeen.m[accountID+"\x00"+appID]
}

// SetThrottleRatio sets the per-slice throttle ratio gauge
// (issue #301, ADR-044). Called by the vmmd-side sampler after
// computing the per-tick (throttle_delta / (throttle_delta +
// usage_delta)) ratio. slice must be one of the api.Plan.SliceName
// values ("tenant-free", "tenant-hobby", "tenant-pro",
// "tenant-scale"); the closed set is pre-instantiated at boot
// (see the loop at top of NewOpsMetrics) so /metrics always
// surfaces a row. Safe on a nil receiver.
func (m *OpsMetrics) SetThrottleRatio(slice string, ratio float64) {
	if m == nil || m.throttleRatio == nil {
		return
	}
	m.throttleRatio.WithLabelValues(slice).Set(ratio)
}

// ShouldReset returns true if the rolling 24h window has elapsed
// since the last resetWindow. Cheap read; called from the 5s
// sampler tick. Forwarded from *topAccountSet so the sampler stays
// decoupled from the primitive's unexported state.
func (s *topAccountSet) ShouldReset() bool {
	if s == nil {
		return false
	}
	return s.shouldReset()
}

// ResetWindow wipes the rolling-window counts and updates
// lastReset. Called by the sampler goroutine every 24h. Nil-safe so
// a sampler that races a torn-down primitive no-ops.
func (s *topAccountSet) ResetWindow() {
	if s == nil {
		return
	}
	s.resetWindow()
}

// SnapshotCounts returns a copy of the current per-account rolling
// counts keyed by account_id (post-accountLabelSet admission). Used
// by the sampler (cmd/apid/topn.go) to compute the 5s rps diff
// between successive ticks. The returned map is a fresh allocation;
// callers may mutate it freely.
//
// Implementation note: exposed for the sampler; pkg/wire unit tests
// in topn_test.go use the lower-level topNSnapshot which returns
// sorted (id, count) tuples. The sampler wants the raw count map
// because it tracks per-id prev values keyed by the same id;
// sorting would force a reverse-lookup.
func (s *topAccountSet) SnapshotCounts() map[string]uint64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]uint64, len(s.counts))
	for id, count := range s.counts {
		out[id] = count
	}
	return out
}

// CodeFromStatus returns the wire-level code label for a recorded
// HTTP response status. "ok" covers 2xx/3xx (the request landed
// server-side and produced a response); "err" covers 4xx/5xx (the
// request failed before, during, or after the handler). This is the
// same split observeErrFromStatus uses in cmd/apid/server.go for
// apid_ops_total{code} — kept in lockstep so the §12 traffic-anomaly
// recording rules (faas_apid_request_rate_5m, _error_rate_5m) read
// from a consistent client/server view.
func CodeFromStatus(status int) string {
	if status >= 200 && status < 400 {
		return "ok"
	}
	return "err"
}

// WakeIDV4Fallback returns the unlabelled counter the wake_id mint
// path increments when uuid.NewV7 fails and the engine falls back to
// uuid.New (v4). Review finding #6 (gaps analysis 2026-07-23): any
// non-zero rate indicates a broken crypto/rand subsystem and silently
// breaks the time-ordering invariant the partial index is built on.
func (m *OpsMetrics) WakeIDV4Fallback() prometheus.Counter {
	return m.wakeIDV4Fallback
}

// SnapshotDiskDrift returns the counter accessor for the read-only
// /srv/fc/snap drift sweep (PR scale-out readiness #3). Nil-safe —
// returns nil on a nil receiver so DiskDrift.Tick can call this
// without a nil-check at every call site. The counter is unlabelled,
// so callers increment directly without label selection.
func (m *OpsMetrics) SnapshotDiskDrift() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.snapshotDiskDrift
}

// CapacitySignatureRejected returns the counter accessor for the
// ADR-053 §3 signature-failure path. The handler
// (pkg/scheddgrpc.Server.ReportCapacity) increments this once per
// rejected stream — the handler closes the stream on the first
// bad frame, so per-frame increment would over-count under any
// publish-after-verify scenario. Nil-safe — returns nil on a nil
// receiver so the handler can call this without a nil-check at
// every call site. The counter is unlabelled; the operator's
// "which node?" question is answered by the audit log's
// stream-rejection event, not by the metric label.
func (m *OpsMetrics) CapacitySignatureRejected() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.capacitySignatureRejected
}

// CPUStatsCollectDuration returns the histogram accessor for the
// CPU-rate-and-accumulator read-path duration histogram
// (issue #279 / PR-B / ADR-039). The histogram is per-RPC (not
// per-tick) and unlabelled; the underlying Histogram is a
// singleton, not a vec. nil on daemons that don't expose the
// path (apid, imaged, builderd, gatewayd-internal, meterd, githubd,
// faas CLI) — the vmmdgrpc.Stats and scheddgrpc.ListInstanceStats
// handlers guard the call with a nil check. Safe to cache.
func (m *OpsMetrics) CPUStatsCollectDuration() prometheus.Histogram {
	return m.cpuStatsCollectDur
}

// SSEClients returns the gauge apid's /v1/events handler increments
// at the top of the connection (defer Dec) so the §12 panel sees the
// number of currently-open dashboard EventSource + CLI faas tail
// connections. Move 3 / M7.5 prep. The returned gauge is shared
// across every caller (the Gauge is a singleton, not a vec) and
// the handler's Add(1)/Add(-1) is the only producer.
func (m *OpsMetrics) SSEClients() prometheus.Gauge {
	return m.sseClients
}

// EgressDeny returns the per-(cidr, family) counter for the egress
// denylist (PR-E). cidr is the DenyEntry.CounterName (the canonical
// name looked up by the vmmd nft-poll adapter via `nft list counters`)
// and family is the nft family keyword ("ip" / "ip6"). The returned
// Counter is safe to cache; the underlying CounterVec is shared with
// other (cidr, family) tuples. Every catalog (cidr, family) tuple is
// pre-instantiated at boot so callers can call EgressDeny on any
// catalog entry without a nil-Counter panic.
//
// PR-E caller pattern:
//
//	counter, err := netns.PopCounters(ctx)
//	if err != nil { /* log + continue */ }
//	for _, e := range netns.NewDefaultDenySet().Entries {
//	    curr := counter[e.CounterName]
//	    delta := curr - lastSeen[e.CounterName]
//	    lastSeen[e.CounterName] = curr
//	    ops.EgressDeny(e.CounterName, e.Family.String()).Add(float64(delta))
//	}
//
// Safe on a nil receiver so tests without metrics keep working (matches
// the Observe* family pattern).
func (m *OpsMetrics) EgressDeny(cidr, family string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.egressDeny.WithLabelValues(cidr, family)
}

// EgressDenySeries returns the underlying CounterVec for callers that
// need to iterate the closed (cidr, family) label set (e.g. an admin
// /debug endpoint that wants to dump the full catalog of zero-valued
// series). The CounterVec is shared with EgressDeny — use either, but
// EgressDeny is the canonical call site for increment.
func (m *OpsMetrics) EgressDenySeries() *prometheus.CounterVec {
	return m.egressDeny
}

// OCIEgressDeny returns the per-(cidr, family) counter for the
// user-space OCI dialer refusals (PR-E). Mirrors EgressDeny's
// signature but returns nil on non-imaged OpsMetrics (the
// ociEgressDeny collector is only registered when prefix ==
// "imaged"). cidr is the DenyEntry.CounterName (or, for OCI-only
// extras, netns.DropCounterName(family, prefix)) and family is the
// nft family keyword.
//
// The returned Counter is safe to cache; the underlying CounterVec
// is shared with other (cidr, family) tuples. Every catalog
// (cidr, family) tuple is pre-instantiated at boot; the OCI-only
// extras are pre-instantiated from cmd/imaged/main.go because
// pkg/wire doesn't import pkg/oci (and shouldn't — pkg/oci imports
// pkg/netns but not pkg/wire, and that direction is correct).
//
// Safe on a nil receiver so tests without metrics keep working.
func (m *OpsMetrics) OCIEgressDeny(cidr, family string) prometheus.Counter {
	if m == nil || m.ociEgressDeny == nil {
		return nil
	}
	return m.ociEgressDeny.WithLabelValues(cidr, family)
}

// OCIEgressDenySeries returns the underlying CounterVec for callers
// that need to iterate the closed (cidr, family) label set on the
// imaged registry. nil on non-imaged OpsMetrics. Use OCIEgressDeny
// for the canonical call site; this is for admin/debug iteration.
func (m *OpsMetrics) OCIEgressDenySeries() *prometheus.CounterVec {
	return m.ociEgressDeny
}

// OwnershipClamp returns the per-reason counter that records layer
// entries whose declared uid/gid fell outside the preserved range
// (ADR-136 §Decision 2). reason ∈ {out_of_range, unparseable_uid,
// unparseable_gid}. Nil-safe on a nil receiver and on non-imaged
// OpsMetrics (the collector is registered only when prefix ==
// "imaged"), so pkg/rootfs.ApplyLayer can call without a nil-check.
// The returned Counter is safe to cache; the underlying CounterVec
// is shared with other reason tuples.
func (m *OpsMetrics) OwnershipClamp(reason string) prometheus.Counter {
	if m == nil || m.ownershipClamp == nil {
		return nil
	}
	return m.ownershipClamp.WithLabelValues(reason)
}

// LayerEntrySkipped returns the bare Counter for char/block/fifo
// layer entries dropped by pkg/rootfs.applyEntry. Nil-safe on a nil
// receiver and on non-imaged OpsMetrics.
func (m *OpsMetrics) LayerEntrySkipped() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.layerEntrySkipped
}

// EgressSourceErrors returns the bare Counter that records per-
// instance sysfs read failures from the vmmd network poll adapter
// (ADR-046, step 7). Safe on a nil receiver so call sites can be
// written without a nil-check; the caller's expected use is:
//
//	ops.EgressSourceErrors().Inc()
//
// on each per-instance read failure inside the 250 ms tick. The
// counter is registered on every daemon via the single-registry
// pattern; only vmmd's production path increments. The byte data
// itself lives in usage_minutes.net_tx_bytes; this counter is the
// failure-channel for the alert pipeline.
func (m *OpsMetrics) EgressSourceErrors() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.egressSourceErrors
}

// RegistryCredentialMarkUsedFailures returns the bare Counter that
// records imaged's
// store.MarkAppRegistryCredentialUsed failures after a successful
// authenticated pull (ADR-062 / issue #461). Safe on a nil
// receiver so call sites can be written without a nil-check; the
// caller's expected use is:
//
//	ops.RegistryCredentialMarkUsedFailures().Inc()
//
// Registered on every daemon via the single-registry pattern; only
// imaged's markRegistryCredentialUsed increments in production.
// The deployment itself succeeds — mark-used is intentionally
// non-fatal per ADR-062 §Decision 8 — but a persistent non-zero
// rate means `last_used_at` is lagging reality.
func (m *OpsMetrics) RegistryCredentialMarkUsedFailures() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.registryCredentialMarkUsedFailures
}

// StorageCacheStaleFallback returns the unlabelled counter the
// LocalCacheBackend's CacheObserver adapter increments once per
// Get that served a stale cached blob because the parent backend
// failed AND FAAS_STORAGE_CACHE_SERVE_STALE=true. The counter is
// unlabelled (deployment-level policy; closed-set cardinality).
//
// A non-zero rate signals "registry is down" — alertable via the
// §12 storage panel. cmd/{imaged,vmmd,schedd}/main.go wire the
// cache observer with a small adapter that calls this method
// (pkg/storage has no prometheus dependency; the adapter lives
// in the daemon, not in pkg/storage).
//
// nil-safe: returns nil when called on a nil receiver so the
// adapter's call site doesn't need to guard.
func (m *OpsMetrics) StorageCacheStaleFallback() prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.storageCacheStaleFallback
}

// Registry returns the underlying registry — pass to promhttp.HandlerFor
// if you want to share it with metrics from elsewhere.
func (m *OpsMetrics) Registry() *prometheus.Registry { return m.registry }

// MetricPrefix returns the exact prefix used by this registry's metric names.
func (m *OpsMetrics) MetricPrefix() string {
	if m == nil {
		return ""
	}
	return m.metricPrefix
}

// Observe records one operation outcome. err == nil codes OK; any error
// is treated as a failure and exposes the gRPC code's string form as the
// "code" label.
func (m *OpsMetrics) Observe(op string, dur time.Duration, err error) {
	code := "ok"
	if err != nil {
		code = "err"
	}
	m.ops.WithLabelValues(op, code).Inc()
	m.dur.WithLabelValues(op).Observe(dur.Seconds())
}

// ObserveCode is like Observe but the caller supplies the terminal code
// label directly. Use it when the failure mode has sub-categories worth
// alerting on (e.g. "stripe-card-decline" vs "stripe-rate-limit" rather
// than a single "stripe-err" bucket). code="ok" is the success label;
// any other short, stable label is the failure mode — see
// pkg/billing/stripe.ClassifyPushError for the canonical Stripe set.
//
// The counter and histogram are incremented under the same op label as
// Observe; only the code-label cardinality differs. Pairs with
// StripePushDuration(result) for ops that want a dedicated histogram
// (the dur histogram's sub-millisecond control-plane buckets are wrong
// for the multi-second Stripe API).
func (m *OpsMetrics) ObserveCode(op, code string, dur time.Duration) {
	m.ops.WithLabelValues(op, code).Inc()
	m.dur.WithLabelValues(op).Observe(dur.Seconds())
}

// StripePushDuration returns the per-(result) observer for the dedicated
// <daemon>_stripe_push_duration_seconds histogram. result is the same
// label set as ObserveCode's code arg — "ok" on success, or a
// stripe.ClassifyPushError label on failure. Returned Observer is safe
// to cache; the underlying HistogramVec is shared across labels.
func (m *OpsMetrics) StripePushDuration(result string) prometheus.Observer {
	return m.stripePushDur.WithLabelValues(result)
}

// PaddlePushDuration returns the per-(result) observer for the dedicated
// <daemon>_paddle_push_duration_seconds histogram. result is the closed
// label set from paddle.PushResultLabels() — "ok" on success, or a
// paddle.ClassifyPushError label on failure (note the substitution
// "negative-quantity" → "negative-mb-sec" and the addition of
// "overage-price-missing" vs the Stripe set; the dashboard panel
// definitions are paired per-provider). Returned Observer is safe to
// cache; the underlying HistogramVec is shared across labels. The
// caller (pkg/meter.Pusher) dispatches to this or StripePushDuration
// based on the runtime provider type — see pusherDispatch.
func (m *OpsMetrics) PaddlePushDuration(result string) prometheus.Observer {
	return m.paddlePushDur.WithLabelValues(result)
}

// PolarPushDuration returns the per-(result) observer for the dedicated Polar
// usage-ingestion histogram.
func (m *OpsMetrics) PolarPushDuration(result string) prometheus.Observer {
	return m.polarPushDur.WithLabelValues(result)
}

// WakePhaseEmitted returns the per-(phase, result) counter for
// pkg/events.Platform.Emit (issue #517 / PR-C, ADR-064). phase is
// the substring after `wake.` (e.g. "boot_started", "readiness_200",
// "proxy_first_byte"); result is "ok" on AppendEvent success or
// "failed" on AppendEvent error. The returned Counter is safe to
// cache; the underlying CounterVec is shared across labels. The
// callsite (pkg/events) lives on the per-daemon Platform, which
// upstream callers reach via the engine's `Events` field — the
// collector is single-registry so any daemon can call it without
// a per-daemon switch (matching the auditWriteFail precedent).
// nil-safe on a nil receiver.
func (m *OpsMetrics) WakePhaseEmitted(phase, result string) prometheus.Counter {
	if m == nil {
		return nil
	}
	return m.wakePhaseEmitted.WithLabelValues(phase, result)
}

// WakePhaseDuration returns the per-(phase, result) observer for
// the dedicated <daemon>_wake_phase_duration_seconds histogram.
// phase / result semantics match WakePhaseEmitted. The returned
// Observer is safe to cache; the underlying HistogramVec is shared
// across labels. Observers are sized for the wake envelope
// (queue→admit <100ms; boot <30s; readiness <60s; proxy <5s) so
// the §12 wake-latency panel can page on a per-phase p99.
// nil-safe on a nil receiver.
func (m *OpsMetrics) WakePhaseDuration(phase, result string) prometheus.Observer {
	if m == nil {
		return nil
	}
	return m.wakePhaseDur.WithLabelValues(phase, result)
}

// ObserveBuildCount increments <daemon>_ops_total{op="build",code} by one
// (ADR-030). code is "ok" on success, "cache_hit" for the cache
// short-circuit, or a state.FailureClass string (oom/timeout/user_error/
// infra) on failure — the §12 "build success (non-user_error)" ratio is
// computed off this label. Deliberately separate from the timing
// histograms: the counter is emitted at the point where the outcome is
// known (the mark-succeeded/failed funnels), while duration is emitted
// once per build. Safe on a nil receiver so builderd unit tests without
// metrics keep working.
func (m *OpsMetrics) ObserveBuildCount(code string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues("build", code).Inc()
}

// ObserveDeploymentCancelled (ADR-124) increments the
// <daemon>_ops_total{op="deployment_cancel",outcome} counter by one.
// outcome is the closed label set:
//   - "ok" — deployment row flipped to "cancelled" successfully
//   - "live_forbidden" — attempt to cancel a DeployLive row (409)
//   - "not_cancellable" — row already in a terminal state
//   - "error" — internal failure (logged at the call site)
//
// The §12 "deployment cancel success rate" dashboard tile computes
// ok / (ok + live_forbidden + not_cancellable + error) over a scrape
// window. Safe on a nil receiver so apid unit tests without
// metrics keep working.
func (m *OpsMetrics) ObserveDeploymentCancelled(outcome string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues("deployment_cancel", outcome).Inc()
}

// ObserveBuildCancelled (ADR-124) increments the
// <daemon>_ops_total{op="build_cancel",outcome} counter by one.
// outcome is the closed label set:
//   - "ok" — VM teardown via VM.Cancel succeeded
//   - "vmmd_error" — vmmd.Destroy RPC returned an error
//   - "not_wired" — VM driver is nil (unit-test / stub path)
//   - "skip" — no live VM for the build_id (already exited)
//
// Safe on a nil receiver so builderd unit tests without metrics
// keep working.
func (m *OpsMetrics) ObserveBuildCancelled(outcome string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues("build_cancel", outcome).Inc()
}

// ObserveDeploymentReorder (ADR-124) increments the
// <daemon>_ops_total{op="deployment_reorder",outcome} counter by one.
// outcome is the closed label set:
//   - "ok" — priority updated on a DeployPending row
//   - "not_pending" — target row already past the builderd claim path
//   - "plan_disabled" — caller is on Free plan
//   - "out_of_range" — priority outside [0, 1000]
//   - "error" — internal failure
//
// Safe on a nil receiver.
func (m *OpsMetrics) ObserveDeploymentReorder(outcome string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues("deployment_reorder", outcome).Inc()
}

// ObserveDeploymentCleared (ADR-124) increments the
// <daemon>_ops_total{op="deployment_clear",outcome} counter by one.
// outcome is the closed label set:
//   - "ok" — single deployment soft-deleted (status untouched)
//   - "live_forbidden" — attempt to clear a DeployLive row (409)
//   - "error" — internal failure (logged at the call site)
//
// Mirrors ObserveDeploymentCancelled's label set for §12 dashboard
// symmetry — the cancel/clear success-rate tile joins the two on
// op ∈ {deployment_cancel, deployment_clear} so a clear regression
// is visible alongside a cancel regression. Safe on a nil receiver.
func (m *OpsMetrics) ObserveDeploymentCleared(outcome string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues("deployment_clear", outcome).Inc()
}

// ObserveDeploymentClearObsolete (ADR-124) increments the
// <daemon>_ops_total{op="deployment_clear_obsolete",outcome} counter
// by one. outcome is the closed label set:
//   - "ok" — bulk soft-delete completed (clearedCount = 0 still emits
//     ok; the caller wants the metric regardless of hit count — the
//     hit count rides in the audit row's data->>'cleared_count')
//   - "plan_disabled" — caller is on Free plan
//   - "error" — internal failure
//
// Reorder-side gates a Free caller at the same handler-entry check
// (`Plan.QueueControlsAllowed()`), so the "plan_disabled" outcome
// shares its label value with ObserveDeploymentReorder for the §12
// plan-funnel tile. Safe on a nil receiver.
func (m *OpsMetrics) ObserveDeploymentClearObsolete(outcome string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues("deployment_clear_obsolete", outcome).Inc()
}

// ObserveProvenanceWrite records one ADR-038 build_provenance
// populator outcome. code is "ok" on a successful CREATE /
// ON CONFLICT (build_id) DO UPDATE write, "error" on any
// failure (logged at WARN inside
// pkg/builderd.recordProvenance; the build itself still
// succeeded). The §12 dashboard's "Provenance populator
// success" ratio is the `ok` / (`ok` + `error`) fraction
// per builderd scrape window; a sustained non-1.0 rate points
// at a database issue (connection-pool starvation, FK drift,
// ON CONFLICT DO UPDATE going stale). Safe on a nil receiver
// so the populator's best-effort path doesn't crash unit tests.
func (m *OpsMetrics) ObserveProvenanceWrite(code string) {
	if m == nil {
		return
	}
	m.provenanceWrites.WithLabelValues(code).Inc()
}

// ObserveImageScanVuln records one (image, severity, count) tuple from
// a Grype scan at imaged base-stage time (issue #299). The counter
// is <daemon>_trivy_image_vulns_total{image, severity}; severity is
// the closed {CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN} Grype set. The
// CRITICAL count is the vmmd admission gate's read side — see
// Manager.bringUpScanCheck at pkg/fcvm/manager.go. Safe on a nil
// receiver so the imaged and vmmd callers don't need a nil-check at
// the top of the hot path.
func (m *OpsMetrics) ObserveImageScanVuln(image, severity string, count int) {
	if m == nil {
		return
	}
	m.imageScanVulns.WithLabelValues(image, severity).Add(float64(count))
}

// ObserveDeployScanDuration records one per-deploy grype scan's
// wall-clock duration in <daemon>_deploy_scan_duration_seconds{app}
// (issue #464 / ADR-055). Result is the closed set
// {complete, failed} — skipped scans never run the histogram (the
// scan didn't happen, so duration is meaningless). Safe on a nil
// receiver so the imaged deploy-complete hook doesn't need a
// nil-check at the top of the hot path.
func (m *OpsMetrics) ObserveDeployScanDuration(app, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.deployScanDuration.WithLabelValues(app).Observe(dur.Seconds())
}

// ObserveDeployScanTotal records one per-deploy scan outcome in
// <daemon>_deploy_scan_total{app, result} (issue #464 / ADR-055).
// Result is the closed set {complete, failed, skipped} — the same
// vocabulary the deployments.scan_status column uses, so a
// `count by (result) (rate(deployScanTotal[5m]))` query mirrors
// the SQL `SELECT scan_status, count(*) FROM deployments WHERE
// scanned_at > ... GROUP BY scan_status` view. Safe on a nil
// receiver.
func (m *OpsMetrics) ObserveDeployScanTotal(app, result string) {
	if m == nil {
		return
	}
	m.deployScanTotal.WithLabelValues(app, result).Inc()
}

// ObserveDeployScanVulns records one per-deploy scan's per-severity
// CVE count in <daemon>_deploy_scan_vulns_total{app, severity}
// (issue #464 / ADR-055). Severity is the Grype closed set
// {CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN}. The CRITICAL row is the
// per-deploy equivalent of the vmmd admission gate's read side —
// surfaced-not-enforced (ADR-055 §2). Safe on a nil receiver.
func (m *OpsMetrics) ObserveDeployScanVulns(app, severity string, count int) {
	if m == nil {
		return
	}
	m.deployScanVulns.WithLabelValues(app, severity).Add(float64(count))
}

// ObserveDeployStageDuration records one stage's wall-clock duration
// in <daemon>_deploy_stage_duration_seconds{stage, status}
// (ADR-117 §Production-ready follow-on). Stage is the closed-6
// vocabulary from pkg/state.AllStageNames; status is the closed set
// {completed, failed}. The caller passes the row's own ended_at −
// started_at delta (already on the jsonb row) so the metric agrees
// with the customer-facing stage timeline to the millisecond. Safe
// on a nil receiver so imaged's transitionWithStage can call it
// unconditionally.
func (m *OpsMetrics) ObserveDeployStageDuration(stage, status string, dur time.Duration) {
	if m == nil {
		return
	}
	m.deployStageDuration.WithLabelValues(stage, status).Observe(dur.Seconds())
}

// ObserveBuildDuration records one build's wall-clock duration in the
// build-sized <daemon>_build_duration_seconds histogram (ADR-030),
// labelled by outcome ∈ {cache_hit,ok,failed}. Deliberately NOT ObserveCode:
// that also feeds the control-plane dur histogram whose 5 s ceiling is
// wrong for a 10-min build. Safe on a nil receiver.
func (m *OpsMetrics) ObserveBuildDuration(outcome string, dur time.Duration) {
	if m == nil {
		return
	}
	m.buildDur.WithLabelValues(outcome).Observe(dur.Seconds())
}

// ObserveBuildQueueWait records how long a build sat between enqueue
// (apid CreateBuild) and dequeue (builderd start), feeding the
// <daemon>_build_queue_wait_seconds histogram (spec §12, ADR-030). Safe
// on a nil receiver.
func (m *OpsMetrics) ObserveBuildQueueWait(dur time.Duration) {
	if m == nil {
		return
	}
	m.buildQueueWait.Observe(dur.Seconds())
}

// ObserveImagedOCIPull records one OCI registry pull into the per-domain
// <daemon>_oci_pull_duration_seconds histogram. op ∈ {manifest, config,
// blob, above_base}, result ∈ {ok, err}. Sized to api.OCIPullTimeoutSeconds
// (60 s) — distinct from the 5 s control-plane dur histogram because
// blob downloads can run multi-second. Safe on a nil receiver.
func (m *OpsMetrics) ObserveImagedOCIPull(op, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.imagedOCIPull.WithLabelValues(op, result).Observe(dur.Seconds())
}

// SetResidentGBPerCustomer writes one sample to the
// <daemon>_resident_gb_per_customer gauge (ADR-031, PR #141).
// Spec §12 target is 0.305 GB-RAM-hours per paying customer
// (= 312 MB / Hobby plan's 256 MB ≈ 312 MB-monthly inclusive); > 0.45
// warns. Safe on a nil receiver so meterd unit tests without metrics
// keep working.
func (m *OpsMetrics) SetResidentGBPerCustomer(plan string, gb float64) {
	if m == nil {
		return
	}
	m.residentGBPerCustomer.WithLabelValues(plan).Set(gb)
}

// BillingCapExceededTotal is the per-plan counter the meterd quota
// tick increments every time accounts.overage_cap_cents is met and
// the overage-row insert is skipped (issue #279). Labelled by plan
// ∈ {free, hobby, pro, scale}. A non-zero rate is informational: a
// cap-hit account is operating as designed (the customer hit the
// operator-set monthly ceiling), not a failure mode. Safe on a nil
// receiver so meterd unit tests without metrics keep working.
func (m *OpsMetrics) BillingCapExceededTotal(plan string) {
	if m == nil {
		return
	}
	m.billingCapExceededTotal.WithLabelValues(plan).Inc()
}

// MeterdFloorAppliedTotal is the per-plan counter the meterd sample
// tick increments every time the per-app min_instances GB-h floor
// fires and synthetic usage_minutes rows were appended
// (ADR-060, issue #515). Labelled by plan ∈ {free, hobby, pro,
// scale}. Free never fires (PR-A's PATCH gate rejects
// min_instances > 0). One increment per (app, tick) — the
// SyntheticFloor bool on RolledRow is the in-memory marker used by
// the Loop closure to dedupe. Safe on a nil receiver so meterd
// unit tests without metrics keep working.
func (m *OpsMetrics) MeterdFloorAppliedTotal(plan string) {
	if m == nil {
		return
	}
	m.meterdFloorAppliedTotal.WithLabelValues(plan).Inc()
}

// MeteredMBSecondsTotal accumulates MB-seconds onto the
// {mode,plan}-labelled counter wired into §12 dashboards. The
// sample loop calls this with mode ∈ {normal, worker, service,
// job} — mirror is filtered upstream by IsMeteredSkippableMode
// and never reaches this method. Add(mbSeconds) carries the
// per-row billable MB-seconds so the cumulative total reconciles
// with the storage-side usage_minutes table (1:1). Dashboards
// query `rate(metered_mb_seconds_total[5m])` to split
// worker idle-RAM from request-driven RAM without a formula
// change. Safe on a nil receiver so meterd unit tests without
// metrics keep working.
func (m *OpsMetrics) MeteredMBSecondsTotal(mode, plan string, mbSeconds int64) {
	if m == nil {
		return
	}
	if mode == "" {
		mode = "normal"
	}
	m.meteredMBSecondsTotal.WithLabelValues(mode, plan).Add(float64(mbSeconds))
}

// AlertEvalSkippedDegradedTotal increments the alert-eval skip counter
// when pkg/appmetrics returns a degraded source. Safe on a nil receiver
// so meterd unit tests without metrics keep working. The returned closure
// is what pkg/alerts.Evaluator invokes at the skip site — the closure
// shape lets pkg/alerts define a narrow Ops interface without importing
// the prometheus.Counter type (matches the BillingCapExceededTotal API
// convention, but closure-based so the dispatcher's caller can read
// "alerts: eval skipped degraded source" without a one-line
// per-callsite import).
func (m *OpsMetrics) AlertEvalSkippedDegradedTotal() func() {
	if m == nil {
		return func() {}
	}
	m.alertEvalSkippedDegradedTotal.Inc()
	return func() {}
}

// AlertEvalFiredTotal increments the alert-eval fire counter when a
// rule crossed its threshold AND ClaimAlertFire won the cool-down race.
// Returns a no-op closure on a nil receiver (see AlertEvalSkippedDegradedTotal).
func (m *OpsMetrics) AlertEvalFiredTotal() func() {
	if m == nil {
		return func() {}
	}
	m.alertEvalFiredTotal.Inc()
	return func() {}
}

// CanaryProgressionAdvancedTotal (issue #976 / ADR-122 /
// SAFE-RELEASES-A) increments the canary-progression advanced
// counter. Fires once per row whose canary_step has just been
// bumped (wall-clock boundary crossed + APID patch accepted).
// Unlabelled. Returns a no-op closure on a nil receiver — mirrors
// AlertEvalFiredTotal.
func (m *OpsMetrics) CanaryProgressionAdvancedTotal() func() {
	if m == nil {
		return func() {}
	}
	m.canaryProgressionAdvancedTotal.Inc()
	return func() {}
}

// CanaryProgressionZeroTimestampTotal (SAFE-RELEASES code-review
// hardening, migration 00517) increments the canary-progression
// tripwire counter every time the meterd tick walks a row whose
// canary_step_started_at is the zero time. Post-00517 the column is
// NOT NULL DEFAULT NOW(), so a non-zero rate means a write path
// bypassed the schema default — exactly the silent-soak-bypass hole
// code-review finding #1 was worried about. Behaviour on zero is
// unchanged (the wall-clock check still runs; elapsed = 56 years >
// Duration → advance) — the counter exists purely for operator
// visibility (and as the §12 dashboard tripwire for "a write path
// skipped the apid CreateDeployment stamp"). Unlabelled — fleet
// rollup; per-deployment detail lives in the existing
// deploy.traffic_changed audit row. Returns a no-op closure on a
// nil receiver — mirrors CanaryProgressionAdvancedTotal.
func (m *OpsMetrics) CanaryProgressionZeroTimestampTotal() func() {
	if m == nil {
		return func() {}
	}
	m.canaryProgressionZeroTimestampTotal.Inc()
	return func() {}
}

// CanaryProgressionErrorsTotal (issue #976 / ADR-122 /
// SAFE-RELEASES-A) increments the canary-progression error counter
// labelled by reason ∈ {advance, list_in_flight}. Closed vocabulary; unknown reasons drop to the
// no-op closure (matches AlertDeliveryAttemptsTotal).
func (m *OpsMetrics) CanaryProgressionErrorsTotal(reason string) func() {
	if m == nil {
		return func() {}
	}
	switch reason {
	case "advance", "list_in_flight":
		// admitted
	default:
		return func() {}
	}
	m.canaryProgressionErrorsTotal.WithLabelValues(reason).Inc()
	return func() {}
}

// AlertDeliveryAttemptsTotal increments the alert-delivery attempts
// counter, labelled by outcome ∈ {delivered, failed}. An unknown
// outcome is dropped (the closed-vocabulary contract — see
// pkg/alerts.evaluator's recordResult). Returns a no-op closure on a
// nil receiver.
func (m *OpsMetrics) AlertDeliveryAttemptsTotal(outcome string) func() {
	if m == nil {
		return func() {}
	}
	switch outcome {
	case "delivered", "failed":
		// admitted
	default:
		return func() {}
	}
	m.alertDeliveryAttemptsTotal.WithLabelValues(outcome).Inc()
	return func() {}
}

// AlertActionExecutedTotal (issue #976 / ADR-122 / SAFE-RELEASES-B)
// increments the alert-action executed counter, labelled by action
// ∈ {rollback, demote, promote} (closed vocabulary — the
// 'webhook' default is NOT a candidate here because it bypasses
// this surface entirely; webhook fan-out is the legacy path and
// is counted under AlertDeliveryAttemptsTotal). Unknown actions
// are dropped. Returns a no-op closure on a nil receiver.
func (m *OpsMetrics) AlertActionExecutedTotal(action string) func() {
	if m == nil {
		return func() {}
	}
	switch action {
	case "rollback", "demote", "promote":
		// admitted
	default:
		return func() {}
	}
	m.alertActionExecutedTotal.WithLabelValues(action).Inc()
	return func() {}
}

// MeterdAccountSpendEur returns the per-{account_id} MTD EUR-spend
// gauge fed by the meterd spend aggregator. Issue #1233 / ADR-123
// backs the alert preset spend_eur_20. Returns nil on a nil receiver.
func (m *OpsMetrics) MeterdAccountSpendEur() *prometheus.GaugeVec {
	if m == nil {
		return nil
	}
	return m.meterdAccountSpendEur
}

// ApidTenantSurfaceCertExpirySeconds returns the per-{account_id,
// app_id, hostname} remaining-seconds gauge fed by the
// meterd_tenant_surface_cert_expiry refresher
// (cmd/meterd/alert_presets_ticks.go). Issue #1233 / ADR-123 backs
// the alert preset cert_expiring_14d. The accessor + metric name
// keep the legacy `apid_` prefix for backward-compat with already-
// deployed alert rules; the underlying table is meterd-owned per
// the CLAUDE.md ownership rule. Returns nil on a nil receiver.
func (m *OpsMetrics) ApidTenantSurfaceCertExpirySeconds() *prometheus.GaugeVec {
	if m == nil {
		return nil
	}
	return m.apidTenantSurfaceCertExpirySeconds
}

// MeterdAPIReachable returns the per-{account_id, app_id}
// reachability gauge fed by cmd/meterd/api_reachability_sweep.go
// (issue #1233 / ADR-123 PR-B). The caller stamps 1.0 (reachable)
// or 0.0 (no successful invocation in the last 5 min). Backs the
// alert preset api_down. Returns nil on a nil receiver.
func (m *OpsMetrics) MeterdAPIReachable() *prometheus.GaugeVec {
	if m == nil {
		return nil
	}
	return m.meterdAPIReachable
}

// ApidDeploymentFailedTotal returns the per-{account_id, app_id}
// delta counter of deployments whose status transitioned to
// 'failed' since the previous sweep, fed by
// cmd/meterd/deployment_failure_sweep.go (issue #1233 / ADR-123
// PR-B). Backs the alert preset deploy_failed. The counter is
// additive across sweep boundaries; the writer is responsible
// for tracking last-swept state. Returns nil on a nil receiver.
func (m *OpsMetrics) ApidDeploymentFailedTotal() *prometheus.CounterVec {
	if m == nil {
		return nil
	}
	return m.apidDeploymentFailedTotal
}

// ApidTenantSurfaceCertExpiryRefresherWalkCompleteTotal increments
// the walker status counter with the closed-vocabulary {ok, error}
// outcome. Used by the meterd_tenant_surface_cert_expiry
// refresher at cmd/meterd/alert_presets_ticks.go
// (issue #1233 / ADR-123) — surfaces a healthy vs failing walker
// for the §12 self-healing alert. Returns a no-op closure on a nil
// receiver or an unknown result.
func (m *OpsMetrics) ApidTenantSurfaceCertExpiryRefresherWalkCompleteTotal(result string) func() {
	if m == nil {
		return func() {}
	}
	switch result {
	case "ok", "error":
		// admitted
	default:
		return func() {}
	}
	m.apidTenantSurfaceCertExpiryRefresherWalkCompleteTotal.WithLabelValues(result).Inc()
	return func() {}
}

// PgBackupLastPushed returns the unlabelled gauge stamped by the
// apid's pgBackupPushedSampler goroutine (issue #250). Operators
// scrape it via the cluster-wide /metrics endpoint; the alert rule
// `PgBackupStale` (deploy/ansible/roles/prometheus/files/pg_backup.rules.yml)
// fires when `time() - pg_backup_last_pushed_seconds > 86400`.
// Safe on a nil receiver (returns nil — same shape as the other
// accessor shortcuts).
func (m *OpsMetrics) PgBackupLastPushed() prometheus.Gauge {
	if m == nil {
		return nil
	}
	return m.pgBackupLastPushed
}

// SetAlertEvaluatorEnabled stamps the alert-evaluator-enabled gauge.
// cmd/meterd calls this once at boot (1 when the Evaluator tick is
// wired and 0 when the boot decision disabled it) and the gauge is
// scraped by /healthz plus the §12 self-healing alert
// alertEvalDisabled. Safe on a nil receiver.
func (m *OpsMetrics) SetAlertEvaluatorEnabled(enabled bool) {
	if m == nil {
		return
	}
	if enabled {
		m.alertEvaluatorEnabled.Set(1)
	} else {
		m.alertEvaluatorEnabled.Set(0)
	}
	m.alertEvaluatorMu.Lock()
	m.alertEvaluatorEnabledValue = enabled
	m.alertEvaluatorMu.Unlock()
}

// AlertEvaluatorEnabled returns the current gauge value. Used by
// the meterd /healthz handler so a curl from the operator can tell
// "evaluator wired?" without scraping /metrics. Returns false on a
// nil receiver.
//
// Note: prometheus.Gauge has no Value() reader (its surface is
// Set/Inc/Dec/Add/Sub/Write); this method tracks the same state in
// a shadow bool updated under a tiny mutex so the /healthz handler
// can answer "is the evaluator wired?" without scraping /metrics.
func (m *OpsMetrics) AlertEvaluatorEnabled() bool {
	if m == nil {
		return false
	}
	m.alertEvaluatorMu.Lock()
	defer m.alertEvaluatorMu.Unlock()
	return m.alertEvaluatorEnabledValue
}

// ReplaceInstanceStats rewrites the per-{app,node} instance-stats
// gauges from the latest poller snapshot (issue #170 / PR-A).
//
// Rollup semantics across live siblings of one (app, node):
//
//   - CPUPct: max — peaks are what scaling cares about. NaN values
//     are excluded (the poller marks a row Unknown when the first
//     sample is missing or the cgroup is unreadable).
//   - RSSMB: sum — capacity rollup. NaN values are excluded.
//   - InflightRequests: sum — load rollup. Always 0 or positive;
//     zero is a real value.
//   - CPUSeconds: sum — cumulative work, added to the
//     CounterVec per tick via the per-(app, node) baseline
//     in cpuSecondsLastSeen. On regression (curr < last) the
//     baseline is reset to curr and the delta is 0, so the
//     counter stays monotonic. NaN is treated as 0.
//   - ThrottledUsec (issue #301 / ADR-044): sum — cumulative
//     cgroup throttled_usec, fed into the per-(account_id,
//     app_id) baseline in cpuThrottleLastSeen. The CounterVec
//     emission happens via EmitTopAppThrottle from the
//     vmmd-side sampler (each vmmd polls its own per-instance
//     baseline; the Sampler picks the top-N every 5s). Here
//     we just record the per-tick sample into topAppSet so the
//     100-app invariant has the data to rank. NaN is treated
//     as 0.
//
// After each call the three gauge label sets are exactly the
// (app, node) pairs present in rows. The GaugeVec.Reset() call
// drops any prior label tuples that no longer have a live
// instance, so a destroyed app stops surfacing in the next
// scrape (no zombie samples). The trade-off is that we lose
// any "app X is now idle" history — the gauge was designed to
// be the live view, the audit log is the durable view.
//
// dur is recorded in the per-Tick histogram. The caller passes
// the wall-clock duration of the Tick so the poller doesn't
// have to know about wire plumbing.
//
// Safe on a nil receiver so schedd unit tests without metrics
// keep working.
func (m *OpsMetrics) ReplaceInstanceStats(rows []InstanceStatRow, dur time.Duration) {
	if m == nil {
		return
	}
	m.instanceStatsCollectDur.Observe(dur.Seconds())
	if len(rows) == 0 {
		m.instanceCPUPct.Reset()
		m.instanceRSSMB.Reset()
		m.instanceInflightReqs.Reset()
		return
	}
	// Roll into per-(app,node) buckets. The map key is the
	// (app, node) tuple — same string form used as the Prom label.
	type acc struct {
		maxCPU  float64
		hasCPU  bool
		sumRSS  float64
		sumInfl int64
	}
	rolled := make(map[string]*acc, len(rows))
	for _, r := range rows {
		key := r.AppID + "\x00" + r.NodeID
		a, ok := rolled[key]
		if !ok {
			a = &acc{}
			rolled[key] = a
		}
		// CPUPct: max over rows that have a real reading.
		if !math.IsNaN(r.CPUPct) {
			if !a.hasCPU || r.CPUPct > a.maxCPU {
				a.maxCPU = r.CPUPct
				a.hasCPU = true
			}
		}
		// RSSMB: sum over rows that have a real reading.
		if !math.IsNaN(r.RSSMB) {
			a.sumRSS += r.RSSMB
		}
		// InflightRequests: sum always (zero is a real value).
		a.sumInfl += r.InflightRequests
	}
	// Reset all three GaugeVecs so disappeared (app, node) pairs
	// don't linger. The (app, node) label pair is bounded by the
	// app+node cardinality, which is fine for a one-box or small
	// cluster; the customer count is the load-bearing bound.
	m.instanceCPUPct.Reset()
	m.instanceRSSMB.Reset()
	m.instanceInflightReqs.Reset()
	for key, a := range rolled {
		app, node := splitKey(key)
		if a.hasCPU {
			m.instanceCPUPct.WithLabelValues(app, node).Set(a.maxCPU)
		}
		m.instanceRSSMB.WithLabelValues(app, node).Set(a.sumRSS)
		m.instanceInflightReqs.WithLabelValues(app, node).Set(float64(a.sumInfl))
	}
	// Second pass for the cumulative counter: sum CPUSeconds
	// per (app, node) and Add the per-row delta through
	// cpuSecondsLastSeen. Done after the gauge pass so the
	// reader (Prometheus scrape) sees a consistent set of
	// rows in one Tick. NaN values are skipped (the
	// "absent this tick" sentinel never contributes to a
	// monotonic counter).
	cpuTotals := make(map[string]float64, len(rows))
	for _, r := range rows {
		if math.IsNaN(r.CPUSeconds) {
			continue
		}
		key := r.AppID + "\x00" + r.NodeID
		cpuTotals[key] += r.CPUSeconds
	}
	for key, curr := range cpuTotals {
		app, node := splitKey(key)
		delta := m.cpuSecondsLast.add(key, curr)
		if delta > 0 {
			m.instanceCPUSecondsTotal.WithLabelValues(app, node).Add(delta)
		}
	}
	// Third pass (issue #301 / ADR-044): sum ThrottledUsec per
	// (account_id, app_id), record the per-tick sample in
	// topAppSet (the bounded top-100 admission primitive), and
	// update the per-pair baseline in cpuThrottleLastSeen. The
	// actual counter emission is via EmitTopAppThrottle from the
	// vmmd-side sampler (the Sampler closes the loop with the
	// per-tick delta); here we just (a) keep the baseline fresh
	// and (b) bump the topAppSet count so the top-N ranking
	// stays accurate. NaN values are skipped (the "absent this
	// tick" sentinel never contributes to a monotonic counter).
	throttleTotals := make(map[string]float64, len(rows))
	throttleKeys := make(map[string]appKey, len(rows))
	for _, r := range rows {
		if math.IsNaN(r.ThrottledUsec) {
			continue
		}
		// accountID is the apps row's owner; empty falls
		// through to the "anonymous" admission label so the
		// topAppSet is bounded consistently with the other
		// account-labelled metrics.
		accountID := r.AccountID
		if accountID == "" {
			accountID = "anonymous"
		}
		key := accountID + "\x00" + r.AppID
		throttleTotals[key] += r.ThrottledUsec
		throttleKeys[key] = appKey{accountID: accountID, appID: r.AppID}
	}
	for key, curr := range throttleTotals {
		k := throttleKeys[key]
		// Bump the topAppSet count so the top-N ranking
		// reflects the current tick. The Sampler closes the
		// loop with EmitTopAppThrottle — but the rank
		// primitive must see the observation first.
		if m.topApps != nil {
			m.topApps.sample(k.accountID, k.appID)
		}
		// Update the per-pair baseline so the Sampler's
		// perAppThrottleSeconds closure can compute the
		// delta between rolls. The first observation
		// returns 0 (no prior baseline); the Sampler must
		// skip the first tick the same way it does for the
		// (app, node) CPUSeconds pass.
		m.throttleSecondsLastSeen.add(key, curr)
	}
}

// InstanceStatsPartialError increments the per-node
// instance_stats_partial_errors counter. Called by the poller
// when a single node's dial or decode fails but the rest of the
// sweep still completes. Distinct from the per-op ops_total
// because the poller intentionally logs + continues on partial
// failures rather than aborting the whole Tick.
func (m *OpsMetrics) InstanceStatsPartialError(node string) {
	if m == nil {
		return
	}
	m.instanceStatsPartialErrors.WithLabelValues(node).Inc()
}

// InstanceStatsCollectSeconds is the per-Tick duration observer the
// poller uses to record its own wall-clock time. The returned
// Observer is safe to cache; the underlying Histogram is shared
// across all callers.
func (m *OpsMetrics) InstanceStatsCollectSeconds() prometheus.Observer {
	return m.instanceStatsCollectDur
}

// splitKey reverses the (app, node) key-join used by
// ReplaceInstanceStats. The separator is the NUL byte (never
// valid in an app_id or node_id — both are UUIDs / [a-z0-9-]+)
// so a malicious payload can't smuggle an extra delimiter.
func splitKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// ObserveScaleUp records one scale-up trigger decision (issue #169 /
// #172). Outcome ∈ {admit, reject_at_cap, no_signal}.
// app is the apps.id (UUID). Safe on a nil receiver so schedd unit
// tests without metrics keep working.
func (m *OpsMetrics) ObserveScaleUp(app, outcome string) {
	if m == nil {
		return
	}
	m.scaleUpDecisions.WithLabelValues(app, outcome).Inc()
}

// ObserveScaleDown records one reaper scale-down decision per app
// per 10 s reaper tick that ran the new code path. outcome ∈
// {park, keep, min_floor_already, cooldown_held}; emitted by both
// ReapIdle (P1D) and ReapAggressive (P1C), one observation per
// app per reaper branch per tick. Safe on a nil receiver so schedd
// unit tests without metrics keep working.
func (m *OpsMetrics) ObserveScaleDown(app, outcome string) {
	if m == nil {
		return
	}
	m.scaleDownDecisions.WithLabelValues(app, outcome).Inc()
}

// ObserveFloor records one proactive min-instances floor reconciler
// decision (issue #557 / ADR-071 §Decision 1). One observation per
// app per tick. outcome ∈ {admit, floor_met, disabled, at_capacity,
// ram_ceiling, cooldown_held, error, backoff_held}. Safe on a nil
// receiver so schedd unit tests without metrics keep working.
func (m *OpsMetrics) ObserveFloor(app, outcome string) {
	if m == nil {
		return
	}
	m.floorReconcileDecisions.WithLabelValues(app, outcome).Inc()
}

// IncFloorReconcileError records one proactive floor reconcile error
// (issue #557 / ADR-071 §Decision 4). kind ∈ {admit_denied,
// admit_error}. Safe on a nil receiver.
func (m *OpsMetrics) IncFloorReconcileError(app, kind string) {
	if m == nil {
		return
	}
	m.floorReconcileErrors.WithLabelValues(app, kind).Inc()
}

// IncFloorInstanceAdmitted increments the global "floor wake
// succeeded" counter (issue #557 / ADR-071 §Decision 1). Safe on a
// nil receiver.
func (m *OpsMetrics) IncFloorInstanceAdmitted() {
	if m == nil {
		return
	}
	m.floorInstancesAdmitted.Inc()
}

// ObserveSidecarRestart records one sidecar restart cycle
// (issue #463 / ADR-069 / ADR-071 / PR-C §4). vmmd's
// dispatchSidecarRestart calls this on every guest-init
// Supervisor.OnCrash event for an essential sidecar; the
// counter lands in <daemon>_sidecar_restart_total. Bounded
// cardinality (apps × SidecarCapMax ≤ 200 worst-case, see
// ADR-071). Safe on a nil receiver so a vmmd run without
// metrics keeps working (default-local path).
func (m *OpsMetrics) ObserveSidecarRestart(app, sidecar string) {
	if m == nil {
		return
	}
	m.sidecarRestartTotal.WithLabelValues(app, sidecar).Inc()
}

// ObserveScaleUpAdmitRPS records the per-instance RPS at the moment
// the trigger admitted a new instance (issue #169 / #172). Sized to
// the per-instance RPS target range; observation lands in
// <daemon>_scale_up_admit_rps. Safe on a nil receiver.
func (m *OpsMetrics) ObserveScaleUpAdmitRPS(rps float64) {
	if m == nil {
		return
	}
	m.scaleUpAdmitRPS.Observe(rps)
}

// ObserveLogEmitted records one SSE log frame emitted to a client
// (issue #254, Move 4). Called from cmd/apid/handlers_ext.go::
// writeAppLogEvent for each `event: log` frame so the §12
// per-app log throughput panel has a per-app series. The app
// label is the apps.id (UUID) — same identity used by
// ObserveScaleUp / ObserveScaleDown. Safe on a nil receiver so
// unit tests without a metrics registry keep working.
//
// Per memory wire-opsmetrics-single-registry: only apid calls
// this in production; the CounterVec is registered on every
// daemon's OpsMetrics so the struct remains single-registry.
func (m *OpsMetrics) ObserveLogEmitted(app string) {
	if m == nil {
		return
	}
	m.apidLogsEmittedTotal.WithLabelValues(app).Inc()
}

// IncLogDropped increments apid_logs_dropped_total{reason} for
// the three closed-set drop sites (issue #309 / tier-2 DX):
//
//   - "slow_subscriber" — pkg/fcvm/logbuf/ring.go::Write's
//     default branch fires when the ring is full (the LogSink
//     consumer is behind). Incrementing here is the operator's
//     "logs are being silently dropped at the source" signal.
//   - "filter_grep" — schedd's StreamAppLogs sink callback
//     dropped the line because it didn't match the
//     customer-supplied --grep regex. The counter is the
//     customer's first signal that their filter is too narrow.
//   - "filter_level" — same site dropped the line because the
//     heuristic --level matcher classified it below the floor.
//     A persistently high rate means the customer is filtering
//     out their own info logs and may want to relax the floor.
//
// Unknown reason values are silently dropped (the CounterVec
// has no matching label) — callers MUST map to the closed set
// above. Safe on a nil receiver for parity with ObserveLogEmitted.
func (m *OpsMetrics) IncLogDropped(reason string) {
	if m == nil {
		return
	}
	switch reason {
	case "slow_subscriber", "filter_grep", "filter_level":
		m.apidLogsDroppedTotal.WithLabelValues(reason).Inc()
	}
}

// LogsDropped (issue #309 / tier-2 DX): typed counter accessor
// for the per-reason <prefix>_logs_dropped_total series. The
// schedd-side filter-counter whitebox tests
// (TestStreamAppLogs_FilterLevelDropsAndCounts / FilterGrepDropsAndCounts /
// the combined level+grep tiebreaker test) read this directly via
// prometheus/testutil.ToFloat64 instead of walking the registry's
// Gather() output. Direct Counter reads go through the counter's
// internal atomic read; Gather() traverses every registered family
// under a registry mutex and on cold CI runners occasionally
// returns a stale snapshot when the increment and the read are
// in flight on different goroutines — that's the flake this
// accessor closes.
//
// Nil-safe (returns nil on a nil receiver). The closed `reason`
// set is the same as IncLogDropped; unknown values return nil so
// testutil.ToFloat64 surfaces a clean 0 rather than a panic on a
// nil deref.
func (m *OpsMetrics) LogsDropped(reason string) prometheus.Counter {
	if m == nil || m.apidLogsDroppedTotal == nil {
		return nil
	}
	switch reason {
	case "slow_subscriber", "filter_grep", "filter_level":
		return m.apidLogsDroppedTotal.WithLabelValues(reason)
	}
	return nil
}

// ObserveOAuthDisabled increments apid_oauth_disabled_total{provider}
// (issue #419 / ADR-046). Called from the sign-in OAuth consent
// handlers when the provider's SignInConfig entry is Disabled —
// the handler has just returned 503 oauth_provider_unavailable.
// provider is "google" or "github"; unknown values produce no
// increment (the CounterVec has no matching label), so callers
// MUST guard on auth.SignInConfig.Enabled(provider) first. Nil
// receiver is allowed for parity with ObserveLogEmitted above —
// tests that don't wire metrics keep working.
func (m *OpsMetrics) ObserveOAuthDisabled(provider string) {
	if m == nil || m.oauthDisabledTotal == nil {
		return
	}
	switch provider {
	case "google", "github":
		m.oauthDisabledTotal.WithLabelValues(provider).Inc()
	}
}

// advisoryBatchResult labels for ObserveAdvisoryBatchResult
// (Mega-PR B). Exported so the vmmd-side producer
// (pkg/vmmdgrpc/advisory_client.go) can call Observe with the
// exact closed-set label values rather than re-literal-string the
// vocabulary. Adding a new outcome means extending both the const
// block and the pre-instantiation loop in NewOpsMetrics; the
// switch in ObserveAdvisoryBatchResult is the load-bearing
// closed-set guard that prevents accidentally creating a new
// series.
const (
	AdvisoryResultOK                    = "ok"
	AdvisoryResultDialFailed            = "dial_failed"
	AdvisoryResultRejected              = "rejected"
	AdvisoryResultUnavailableAfterRetry = "unavailable_after_retry"
)

// advisorySeverity labels for ObserveStatelessAdvisory (Mega-PR
// B). Exported so cmd/apid/advisory_receiver.go's receiver can
// call Observe with the exact closed-set label values. Mirrors
// the cmd/apid/advisory_receiver.go vocabulary
// (severityHigh / severityWarn / severityInfo) but defined here
// as the canonical wire-side labels so the test fixtures share
// the same values across both packages.
const (
	AdvisorySeverityHigh = "high"
	AdvisorySeverityWarn = "warn"
	AdvisorySeverityInfo = "info"
)

// GithubdBridgeKind* are the closed-set values for the `kind`
// label on githubd_bridge_enqueued_total (issue #432 phase 5;
// extended for issue #272 / ADR-094 PR-preview environments).
// The bridge stamps DeploymentKindGitHub for push events and
// DeploymentKindPreview for pull_request events — the metric
// label mirrors the resolved deployment kind so dashboards
// can split preview traffic from production-push traffic.
//
// The closed-set switch in IncGithubdBridgeEnqueued drops
// unknown values silently (CardinalityGuard). The pre-
// instantiation loop in NewOpsMetrics pre-fills both values
// so the rows surface in /metrics from boot.
const (
	GithubdBridgeKindGitHub  = "github"
	GithubdBridgeKindPreview = "preview"
)

// PathFilterMode* are the closed-set values for the `mode`
// label on githubd_path_filter_total (issue #432 phase 5 /
// ADR-050 §109). The closed-set switch in ObserveGithubdPathFilter
// drops unknown values silently (CardinalityGuard) — adding a
// new mode here requires the matching case in the switch AND
// the matching entry in the pre-instantiation loop above so
// the row surfaces in /metrics from boot.
//
//   - paths:         the optimistic path — the compare API
//     returned successfully and the RootDir
//     intersection picked the touched apps.
//   - full_fallback: any of {client nil, before empty, owner/
//     repo split failed, breaker open} — the
//     dispatcher rebuilds every touched app.
//     Different from truncated: full_fallback is
//     the "we didn't even try" bucket.
//   - truncated:     the compare API returned successfully
//     but the diff was capped (300 files / 250
//     commits). ADR-050 §109 mandates rebuild-
//     all on this signal.
//   - error:         the compare API returned a 4xx/5xx (after
//     retries) that wasn't 429-truncation. The
//     dispatcher rebuilds every touched app and
//     logs at warn level.
//   - breaker_open:  the circuit breaker in pkg/githubd/changedfiles.go
//     was tripped on a prior push and the
//     cooldown hasn't elapsed. The dispatcher
//     doesn't even call GitHub — this label is
//     incremented at the breaker-check site
//     (BEFORE the lookup). A non-zero rate for
//     10m is the canary for the upstream outage.
const (
	PathFilterModePaths        = "paths"
	PathFilterModeFullFallback = "full_fallback"
	PathFilterModeTruncated    = "truncated"
	PathFilterModeError        = "error"
	PathFilterModeBreakerOpen  = "breaker_open"
)

// ObserveAdvisoryBatchResult increments
// stateless_advisory_batches_emitted_total{result} (Mega-PR B).
// Called from pkg/vmmdgrpc/advisory_client.go on each
// ForwardStatelessAdvisory RPC outcome:
//
//   - AdvisoryResultOK                    — apid returned success
//   - AdvisoryResultDialFailed            — couldn't reach apid's unix socket
//   - AdvisoryResultRejected              — apid returned codes.InvalidArgument
//     (validation failure; not retried)
//   - AdvisoryResultUnavailableAfterRetry — apid returned codes.Unavailable
//     after the retry budget was spent
//
// Unknown result values produce no increment (the CounterVec has
// no matching label), so the closed-set switch here is the
// load-bearing cardinality guard. Nil receiver is allowed for
// parity with the other Observe* accessors — vmmd unit tests
// that don't wire metrics keep working.
func (m *OpsMetrics) ObserveAdvisoryBatchResult(result string) {
	if m == nil || m.advisoryBatchesEmittedTotal == nil {
		return
	}
	switch result {
	case AdvisoryResultOK, AdvisoryResultDialFailed, AdvisoryResultRejected, AdvisoryResultUnavailableAfterRetry:
		m.advisoryBatchesEmittedTotal.WithLabelValues(result).Inc()
	}
}

// ObserveStatelessAdvisory increments
// stateless_advisory_events_total{severity} (Mega-PR B). Called
// from cmd/apid/advisory_receiver.go's ForwardStatelessAdvisory
// handler on each landed advisory. severity is
// AdvisorySeverityHigh / Warn / Info — the same vocabulary
// advisoryBatchSeverity already produces. Unknown values produce
// no increment (closed-set guard). Nil receiver is allowed for
// parity.
func (m *OpsMetrics) ObserveStatelessAdvisory(severity string) {
	if m == nil || m.apidStatelessAdvisoryEventsTotal == nil {
		return
	}
	switch severity {
	case AdvisorySeverityHigh, AdvisorySeverityWarn, AdvisorySeverityInfo:
		m.apidStatelessAdvisoryEventsTotal.WithLabelValues(severity).Inc()
	}
}

// IncGithubdBridgeEnqueued increments
// githubd_bridge_enqueued_total{kind} (issue #432 phase 5;
// extended for issue #272 / ADR-094 PR-preview environments).
// Called from cmd/apid/githubd_bridge.go's EnqueueBuild handler
// on each landed build row. kind is one of GithubdBridgeKindGitHub
// (production push) or GithubdBridgeKindPreview (pull_request
// preview). Unknown values produce no increment (closed-set
// guard). Nil receiver is allowed for parity with the other
// Observe* accessors — apid unit tests that don't wire metrics
// keep working.
//
// The function name uses the Inc* prefix (not Observe*) because
// it accepts a state.DeploymentKind-shaped enum value, not a
// duration+error pair — same shape as ObserveAdvisoryBatchResult
// but without a time component. The receiver-side semantics
// (one increment per landed build) mirror the producer-side
// ObserveAdvisoryBatchResult pair.
func (m *OpsMetrics) IncGithubdBridgeEnqueued(kind string) {
	if m == nil || m.apidGithubdBridgeEnqueuedTotal == nil {
		return
	}
	switch kind {
	case GithubdBridgeKindGitHub, GithubdBridgeKindPreview:
		m.apidGithubdBridgeEnqueuedTotal.WithLabelValues(kind).Inc()
	}
}

// ObserveGithubdPathFilter increments
// githubd_path_filter_total{mode} (issue #432 phase 5 /
// ADR-050 §109). Called from pkg/githubd/service.go's
// lookupChangedFiles (and the new circuit breaker in
// pkg/githubd/changedfiles.go) once per inbound webhook after
// the filterMode decision is made.
//
// The closed-set switch below is the load-bearing cardinality
// guard: unknown modes produce no increment so the Prometheus
// TSDB series set stays bounded. Nil receiver is allowed for
// parity with the other Observe* accessors — githubd unit
// tests that don't wire metrics keep working.
//
// The prefix on the metric name follows the
// githubd_<counter> convention (single-registry); see the
// wire-opsmetrics-single-registry note for why this struct is
// identical across every daemon.
func (m *OpsMetrics) ObserveGithubdPathFilter(mode string) {
	if m == nil || m.githubdPathFilterTotal == nil {
		return
	}
	switch mode {
	case PathFilterModePaths, PathFilterModeFullFallback, PathFilterModeTruncated, PathFilterModeError, PathFilterModeBreakerOpen:
		m.githubdPathFilterTotal.WithLabelValues(mode).Inc()
	}
}

// IncPaddleWebhookVerifyFailed increments the paddleWebhookVerifyFailedTotal
// counter (PR-P4). Called from cmd/apid/handlers_ext.go::paddleWebhook
// on signature verify failure. Nil-receiver safe so tests that don't
// wire metrics keep working — the same nil-guard pattern as
// ObserveGithubdPathFilter above. Single-registry: the field is
// registered on every daemon's OpsMetrics; only apid increments.
func (m *OpsMetrics) IncPaddleWebhookVerifyFailed() {
	if m == nil || m.paddleWebhookVerifyFailedTotal == nil {
		return
	}
	m.paddleWebhookVerifyFailedTotal.Inc()
}

// IncPaddleWebhookReplaySuppressed increments the
// paddleWebhookReplaySuppressedTotal counter (PR-P4). Called from
// cmd/apid/handlers_ext.go::paddleWebhook when pkg/webhookdedupe
// reports a replay. Nil-receiver safe; single-registry; only apid
// increments.
func (m *OpsMetrics) IncPaddleWebhookReplaySuppressed() {
	if m == nil || m.paddleWebhookReplaySuppressedTotal == nil {
		return
	}
	m.paddleWebhookReplaySuppressedTotal.Inc()
}

// GithubdPathFilterTotal returns the labelled-counter accessor for
// the path-filter mode counter, used by service-level tests in
// pkg/githubd to assert that the dispatcher emitted the expected
// mode after a push. The mode argument must be one of the closed
// set {paths, full_fallback, truncated, error, breaker_open} —
// any other value returns nil so a typo can't silently succeed.
// Nil-safe — returns nil on a nil receiver so the dispatcher can
// call this without a nil-check at every site.
func (m *OpsMetrics) GithubdPathFilterTotal(mode string) prometheus.Counter {
	if m == nil || m.githubdPathFilterTotal == nil {
		return nil
	}
	switch mode {
	case PathFilterModePaths, PathFilterModeFullFallback, PathFilterModeTruncated, PathFilterModeError, PathFilterModeBreakerOpen:
		return m.githubdPathFilterTotal.WithLabelValues(mode)
	}
	return nil
}

// Handler returns an http.Handler that serves the registry's metrics.
// Plug into any mux — daemons mount it at /metrics.
func (m *OpsMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		Registry: m.registry,
	})
}

// maxAccountLabelValues caps the per-OpsMetrics account-label
// admission set (issue #278). Sized to comfortably exceed the
// Scale-plan 100-deploy upper bound while staying inside Prometheus'
// "tens of thousands of series per metric" guideline. Above the
// cap, new ids collapse to otherAccountLabel ("__other__") so the
// TSDB series set stays bounded over the daemon's lifetime. The cap
// is shared across every account_id-labelled counter —
// AuditWriteFailures and RequestFailure see the same admission set
// so a customer is either represented by their real id in both, or
// by "__other__" in both.
// maxIPLabelValues caps the per-OpsMetrics IP-label admission set
// (issue #286). Sized at the same 10_000 ceiling as
// maxAccountLabelValues — comfortably above the realistic 95th
// percentile of unique source IPs seen in a 5-minute window on a
// single-host edge, while staying inside Prometheus' "tens of
// thousands of series per metric" guideline. Above the cap, new IPs
// collapse to otherIPLabel ("__other__") so the TSDB series set stays
// bounded over the daemon's lifetime — the same fixed-capacity,
// non-evicting contract as accountLabelSet, sized differently because
// IP cardinality grows under attack (a credential-stuffing burst
// from a botnet) where account_id cardinality grows under organic
// signup.
//
// Distinct from maxAccountLabelValues so the two sets don't share
// capacity — a sudden signup surge could affect account labels while
// IP cardinality stays low, and vice versa.
const maxIPLabelValues = 10_000

// anonymousIPLabel is the reserved IP for traffic whose source IP
// could not be resolved. Two cases collapse here:
//   - the RemoteAddr is missing entirely (the Go http server's
//     RemoteAddr is empty string in pathological cases), and
//   - pkg/middleware.defaultClientIP returned the "unknown" sentinel
//     for an unparseable host (the canonical fallback for untrusted
//     callers — `defaultClientIP` parses the host, returns "unknown"
//     on parse failure, and ipLabelSet.admit collapses that sentinel
//     to anonymousIPLabel).
//
// Always admitted without consuming capacity, so an operator can
// distinguish a missing/garbled RemoteAddr from a credential-stuffing
// burst that crossed the admission cap (which collapses to
// otherIPLabel).
const anonymousIPLabel = "anonymous"

// labelUnknown is the closed-set sentinel for "label collapsed because
// the source string was empty / unparseable" on the Prometheus
// collectors in this file. The liveness_restarts counter uses it for
// empty app / deployment labels, and the IP-admission switch path
// uses it for the `case "", "unknown"` branch. Pinning the value
// here keeps goconst at 0 occurrences (golangci-lint v2.4.0 fires on
// repeated string literals ≥ 3×).
const labelUnknown = "unknown"

// otherIPLabel is the reserved IP for traffic whose IP exceeded the
// admission cap. Same contract as otherAccountLabel — operators must
// check the daemon slog for the original IP when an IP lands here;
// the metric label is intentionally lossy.
const otherIPLabel = "__other__"

const maxAccountLabelValues = 10_000

// anonymousAccountLabel is the reserved account_id for traffic that
// arrives without a resolvable principal (e.g. a 401 before
// authentication finishes). Always admitted without consuming real
// capacity, and always re-admitted on collision-free lookups so
// accountLabelSet is free to evict the underlying set across a
// restart.
const anonymousAccountLabel = "anonymous"

// otherAccountLabel is the reserved account_id for traffic whose
// account_id exceeded the admission cap (issue #278). Always
// admitted without consuming real capacity. Operators must check
// the daemon slog for the original id when an account lands here —
// the metric label is intentionally lossy.
const otherAccountLabel = "__other__"

// maxBoxLabelValues caps the per-OpsMetrics box-label admission
// set (issue #1059 / ADR-127). Sized at 64 — covers the
// single-control-plane reality today (N=1) and gives headroom
// for the Tier A multi-host rollout (ADR-062 / ADR-066 chain).
// The cap is far below the "tens of thousands of series per
// metric" Prometheus guideline so a hostile config can never
// exhaust TSDB cardinality on this label — the operator can
// always rely on the closed (box, reason) and (box, phase)
// cartesian being bounded. Above the cap, new box identifiers
// collapse to otherBoxLabel ("__other__") so the Prometheus TSDB
// series set stays bounded over the daemon's lifetime. The cap
// is shared across every box-labelled metric (wakeFailure and
// wakeLatency today) so a box is either represented by its real
// identifier in both, or by "__other__" in both.
//
// Distinct from maxAccountLabelValues and maxIPLabelValues so the
// three sets do not share capacity — a sudden signup surge could
// affect account labels while box cardinality stays low, and
// vice versa.
const maxBoxLabelValues = 64

// labelLocal is the reserved box identifier for the
// single-control-plane reality (issue #1059 / ADR-127 §3.4).
// Today vmmd / schedd always resolve box to this literal —
// there's no compute_nodes.id lookup yet. Always admitted without
// consuming capacity, and always re-admitted on collision-free
// lookups so boxLabelSet is free to reset across a restart. When
// the Tier A multi-host rollout lands (ADR-062 / ADR-066 chain),
// a follow-up commit replaces this placeholder with the
// compute_nodes.id lookup at boot.
const labelLocal = "local"

// otherBoxLabel is the reserved box identifier for traffic whose
// box identifier exceeded the admission cap. Same contract as
// otherAccountLabel — operators must check the daemon slog for
// the original box identifier when a box lands here; the metric
// label is intentionally lossy.
const otherBoxLabel = "__other__"

// BoxHostname returns the canonical box label for the current
// control-plane host — the os.Hostname() value, falling back to
// labelLocal ("local") if the hostname lookup fails or returns
// empty. Wire callers should pass "" rather than the literal
// "local" so the accessors resolve to BoxHostname(); the
// existing literal "local" call sites remain valid for now and
// will be migrated in a follow-up commit when the Tier A
// multi-host rollout (ADR-062 / ADR-066 chain) lands.
//
// The function is pure and side-effect-free — it does not touch
// the boxLabelSet admission set, which is the accessor's job.
// Callers must still funnel the result through m.boxLabel(box)
// (OpsMetrics.WakeFailure and OpsMetrics.WakeLatency do this
// internally when the caller passes ""). The hostname is
// read on every call, not cached — a rare codepath and the
// underlying syscall is cheap.
//
// TODO(multi-host/ADR-066): replace os.Hostname() lookup with
// compute_nodes.id once the compute_nodes table lands (Tier A
// multi-host rollout). At that point BoxHostname() becomes a
// pkg/state/pkgstore.ComputeNodeIDForSelf() call and the call
// sites in pkg/fcvm/manager.go and pkg/vmmdgrpc/server.go stop
// passing the literal "local". Until then the literal stays in
// place because compute_nodes doesn't exist yet, and the host
// hostname is a reasonable stand-in for "this box's box" —
// operators get per-host Prometheus rows from an idle fleet.
func BoxHostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return labelLocal
	}
	return host
}

// maxAppLabelValues caps the per-OpsMetrics app-label admission
// set (issue #1059 / ADR-127 §3.5 deferred work, shipped in the
// platform-observability mega-PR alongside the per-app wake-
// failure split). Sized at 256 — covers the single-region Scale
// plan budget (100 deployed apps per pkg/api/limits.go) plus
// headroom for Tier A multi-region fan-out and per-account
// per-region app replication. The cap is far below the "tens of
// thousands of series per metric" Prometheus guideline so a
// hostile config can never exhaust TSDB cardinality on this
// label. Above the cap, new app slugs collapse to otherAppLabel
// ("__other__") so the Prometheus TSDB series set stays bounded
// over the daemon's lifetime.
//
// Distinct from maxBoxLabelValues, maxAccountLabelValues, and
// maxIPLabelValues so the four sets do not share capacity —
// an account-surge affects account labels while app cardinality
// stays low, and vice versa.
//
// Per-plan budget reference (pkg/api/limits.go):
//
//	Free   →  1 deployed app
//	Hobby  →  5 deployed apps
//	Pro    → 25 deployed apps
//	Scale  → 100 deployed apps
//
// 256 covers Scale headroom (100 apps × 2.5× = ~250) plus a
// small fleet-of-fleets fudge factor for a future multi-region
// rollout (ADR-066 chain).
const maxAppLabelValues = 256

// labelAppUnknown is the reserved app identifier for callsites
// that do not have an app slug in scope (issue #1059 / ADR-127
// §3.5). Always admitted without consuming capacity, and always
// re-admitted on collision-free lookups so appLabelSet is free
// to reset across a restart. The literal "" distinguishes a
// "no app in scope" call from a real app slug that hit the
// admission cap (which collapses to otherAppLabel) — the two
// failure modes mean different things on a triage panel.
const labelAppUnknown = ""

// otherAppLabel is the reserved app identifier for traffic whose
// app slug exceeded the admission cap. Same contract as
// otherAccountLabel / otherBoxLabel — operators must check the
// daemon slog for the original app slug when an app lands here;
// the metric label is intentionally lossy.
const otherAppLabel = "__other__"

// accountLabelSet is the bounded admission set that backs every
// account_id-labelled metric in OpsMetrics (issue #278). The set is
// deliberately a plain map+mutex, not an LRU: an evicting LRU would
// let evicted ids re-admit later and grow the Prometheus TSDB
// series set unbounded over the daemon's lifetime. The map is
// initialized once per OpsMetrics in NewOpsMetrics; the mutex is
// the only synchronisation primitive and is held only across the
// lookup/insert path. Prometheus Counter/Histogram increments
// happen outside the critical section.
//
// Reserved values (anonymousAccountLabel, otherAccountLabel) are
// admitted at boot without consuming capacity and are always
// re-admitted on lookup. Real account ids consume capacity once and
// are never evicted in process — the daemon restart is the only
// path that resets the set.
type accountLabelSet struct {
	mu       sync.Mutex
	admitted map[string]struct{}
	cap      int
}

// newAccountLabelSet constructs an admission set with the given
// capacity. capacity must be > 0; the call panics otherwise to fail
// loud at boot rather than silently allow unbounded admission.
//
// Returns a pointer because accountLabelSet contains a sync.Mutex;
// returning by value would copy the lock (govet copylocks).
func newAccountLabelSet(capacity int) *accountLabelSet {
	if capacity <= 0 {
		panic("wire: accountLabelSet capacity must be positive")
	}
	s := &accountLabelSet{
		admitted: make(map[string]struct{}, capacity),
		cap:      capacity,
	}
	// Reserved values don't count against the cap, but pre-admitting
	// them at construction means accountLabel() doesn't need a
	// special branch for them — the lookup short-circuits through
	// the same map.
	s.admitted[anonymousAccountLabel] = struct{}{}
	s.admitted[otherAccountLabel] = struct{}{}
	return s
}

// admit resolves an account id to its label value (issue #278).
// Empty input normalizes to anonymousAccountLabel. Reserved values
// (anonymousAccountLabel, otherAccountLabel) are always admitted
// without consuming capacity (see reservedCount below). Real ids
// are admitted up to the capacity; further ids collapse to
// otherAccountLabel without ever consuming capacity, and the
// underlying map is never resized past cap.
//
// Concurrency: holds mu across the lookup+insert. The hot path is
// the "already admitted" lookup, which is O(1) and never inserts.
// The Prometheus increment happens at the call site AFTER admit
// returns, so it is outside the critical section.
//
// Pointer receiver: the type contains a sync.Mutex, so copying the
// value would duplicate the lock. accountLabelSet is constructed
// once per OpsMetrics in NewOpsMetrics and held as a pointer field.
func (s *accountLabelSet) admit(accountID string) string {
	switch accountID {
	case "":
		return anonymousAccountLabel
	case anonymousAccountLabel, otherAccountLabel:
		return accountID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[accountID]; ok {
		return accountID
	}
	// Reserved labels are pre-admitted at construction (see
	// newAccountLabelSet) — their entries count toward len(s.admitted)
	// but not toward the user-facing capacity. Subtract them so the
	// real-id budget is exactly `cap - reservedCount`, not
	// `cap - reservedCount - 2`. Without the subtraction the
	// reserved labels steal two slots from the real-id budget; the
	// IP-side sibling (ipLabelSet.admit) caught this same flaw via
	// TestFailedLoginTotal_OverflowCollapsesToOtherSlow (issue #286).
	const reservedCount = 2
	if len(s.admitted)-reservedCount >= s.cap {
		return otherAccountLabel
	}
	s.admitted[accountID] = struct{}{}
	return accountID
}

// accountLabel exposes the admission set as an OpsMetrics method so
// callers don't need to know the underlying type. Safe on a nil
// receiver — returns the input unchanged for the daemon paths that
// don't wire an OpsMetrics (unit tests, see handlers_audit_test).
func (m *OpsMetrics) accountLabel(accountID string) string {
	if m == nil || m.accountLabels == nil {
		return accountID
	}
	return m.accountLabels.admit(accountID)
}

// ipLabelSet is the bounded admission set that backs every
// ip-labelled metric in OpsMetrics (issue #286, today only
// failedLoginTotal). Same shape and contract as accountLabelSet
// above — plain map + mutex, fixed capacity, non-evicting — but a
// distinct type so the two sets do not share capacity. The cap is
// maxIPLabelValues (10_000). Above the cap, new IPs collapse to
// otherIPLabel ("__other__") so the Prometheus TSDB series set stays
// bounded over the daemon's lifetime. The map is initialised once
// per OpsMetrics in NewOpsMetrics; the mutex is the only
// synchronisation primitive and is held only across the
// lookup/insert path. Prometheus Counter increments happen outside
// the critical section.
//
// Reserved values (anonymousIPLabel, otherIPLabel) are admitted at
// boot without consuming capacity and are always re-admitted on
// lookup. Real IPs consume capacity once and are never evicted in
// process — the daemon restart is the only path that resets the set.
type ipLabelSet struct {
	mu       sync.Mutex
	admitted map[string]struct{}
	cap      int
}

// newIPLabelSet constructs an admission set with the given capacity.
// capacity must be > 0; the call panics otherwise to fail loud at
// boot rather than silently allow unbounded admission.
//
// Returns a pointer because ipLabelSet contains a sync.Mutex;
// returning by value would copy the lock (govet copylocks).
func newIPLabelSet(capacity int) *ipLabelSet {
	if capacity <= 0 {
		panic("wire: ipLabelSet capacity must be positive")
	}
	s := &ipLabelSet{
		admitted: make(map[string]struct{}, capacity),
		cap:      capacity,
	}
	// Reserved values don't count against the cap, but pre-admitting
	// them at construction means ipLabel() doesn't need a special
	// branch for them — the lookup short-circuits through the same map.
	s.admitted[anonymousIPLabel] = struct{}{}
	s.admitted[otherIPLabel] = struct{}{}
	return s
}

// admit resolves an IP to its label value (issue #286). Empty input
// and the literal "unknown" sentinel from
// pkg/middleware.defaultClientIP both collapse to anonymousIPLabel,
// so the counter for "unparseable RemoteAddr" is observable
// distinctly from a real credential-stuffing burst that crossed the
// admission cap (which lands in otherIPLabel). New IPs past the
// capacity collapse to otherIPLabel without ever consuming capacity,
// and the underlying map is never resized past cap.
//
// Concurrency: holds mu across the lookup+insert. The hot path is
// the "already admitted" lookup, which is O(1) and never inserts.
// The Prometheus increment happens at the call site AFTER admit
// returns, so it is outside the critical section.
//
// Pointer receiver: the type contains a sync.Mutex, so copying the
// value would duplicate the lock. ipLabelSet is constructed once per
// OpsMetrics in NewOpsMetrics and held as a pointer field.
func (s *ipLabelSet) admit(ip string) string {
	switch ip {
	case "", labelUnknown:
		// ClientIP returns "unknown" for an unparseable
		// RemoteAddr; collapse to anonymousIPLabel so the operator
		// can distinguish "we couldn't resolve the source IP"
		// from a credential-stuffing burst that crossed the
		// admission cap.
		return anonymousIPLabel
	case anonymousIPLabel, otherIPLabel:
		return ip
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[ip]; ok {
		return ip
	}
	// Reserved labels (anonymous, __other__) are pre-admitted at
	// construction (see newIPLabelSet) and consume map entries but
	// NOT user-facing capacity. The user-facing cap of `s.cap`
	// distinct REAL IPs must hold. The check is therefore
	// "real IPs admitted = (len - reserved) >= s.cap", not
	// "len >= s.cap" (which would steal reservedCount slots from
	// the real budget — the original bug, surfaced by
	// TestFailedLoginTotal_OverflowCollapsesToOtherSlow).
	const reservedCount = 2
	realAdmitted := len(s.admitted) - reservedCount
	if realAdmitted >= s.cap {
		return otherIPLabel
	}
	s.admitted[ip] = struct{}{}
	return ip
}

// ipLabel exposes the IP admission set as an OpsMetrics method so
// callers don't need to know the underlying type. Safe on a nil
// receiver — returns the input unchanged for the daemon paths that
// don't wire an OpsMetrics (unit tests).
func (m *OpsMetrics) ipLabel(ip string) string {
	if m == nil || m.ipLabels == nil {
		return ip
	}
	return m.ipLabels.admit(ip)
}

// boxLabelSet is the bounded admission set that backs the box-labelled
// metrics on OpsMetrics (issue #1059 / ADR-127). Same shape and contract
// as accountLabelSet and ipLabelSet above — plain map + mutex, fixed
// capacity, non-evicting — but a distinct type so the three sets do not
// share capacity (a credential-stuffing burst on ipLabelSet must not
// steal slots from the box admission set, and vice versa). The cap is
// maxBoxLabelValues (64). Above the cap, new box identifiers collapse to
// otherBoxLabel ("__other__") so the Prometheus TSDB series set stays
// bounded over the daemon's lifetime. The map is initialised once per
// OpsMetrics in NewOpsMetrics; the mutex is the only synchronisation
// primitive and is held only across the lookup/insert path. Prometheus
// Counter / Histogram increments happen outside the critical section.
//
// Reserved values (labelLocal, otherBoxLabel) are admitted at boot
// without consuming capacity and are always re-admitted on collision-
// free lookups. labelLocal is the single-control-plane placeholder used
// by vmmd / schedd today — the multi-host rollout replaces this with a
// compute_nodes.id lookup. Real box identifiers consume capacity once
// and are never evicted in process — the daemon restart is the only
// path that resets the set.
type boxLabelSet struct {
	mu       sync.Mutex
	admitted map[string]struct{}
	cap      int
}

// newBoxLabelSet constructs an admission set with the given capacity.
// capacity must be > 0; the call panics otherwise to fail loud at boot
// rather than silently allow unbounded admission.
//
// Returns a pointer because boxLabelSet contains a sync.Mutex;
// returning by value would copy the lock (govet copylocks).
func newBoxLabelSet(capacity int) *boxLabelSet {
	if capacity <= 0 {
		panic("wire: boxLabelSet capacity must be positive")
	}
	s := &boxLabelSet{
		admitted: make(map[string]struct{}, capacity),
		cap:      capacity,
	}
	// Reserved values don't count against the cap, but pre-admitting
	// them at construction means boxLabel() doesn't need a special
	// branch for them — the lookup short-circuits through the same map.
	s.admitted[labelLocal] = struct{}{}
	s.admitted[otherBoxLabel] = struct{}{}
	return s
}

// admit resolves a box identifier to its label value (issue #1059 /
// ADR-127). Empty input normalises to labelLocal so a missing box value
// (pathological — the host always supplies one) is observable distinctly
// from a real multi-host rollout that crossed the admission cap (which
// collapses to otherBoxLabel). Reserved values (labelLocal, otherBoxLabel)
// are always admitted without consuming capacity. Real box identifiers
// are admitted up to the capacity; further identifiers collapse to
// otherBoxLabel without ever consuming capacity, and the underlying
// map is never resized past cap.
//
// Concurrency: holds mu across the lookup+insert. The hot path is
// the "already admitted" lookup, which is O(1) and never inserts.
// The Prometheus increment happens at the call site AFTER admit
// returns, so it is outside the critical section.
//
// Pointer receiver: the type contains a sync.Mutex, so copying the
// value would duplicate the lock. boxLabelSet is constructed once
// per OpsMetrics in NewOpsMetrics and held as a pointer field.
func (s *boxLabelSet) admit(box string) string {
	switch box {
	case "":
		return labelLocal
	case labelLocal, otherBoxLabel:
		return box
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[box]; ok {
		return box
	}
	// Reserved labels (labelLocal, otherBoxLabel) are pre-admitted at
	// construction (see newBoxLabelSet) and consume map entries but
	// NOT user-facing capacity. The user-facing cap of `s.cap`
	// distinct REAL box identifiers must hold. The check is therefore
	// "real boxes admitted = (len - reserved) >= s.cap", not
	// "len >= s.cap" — the same bug-fix shape as
	// ipLabelSet.admit (TestFailedLoginTotal_OverflowCollapsesToOtherSlow).
	const reservedCount = 2
	realAdmitted := len(s.admitted) - reservedCount
	if realAdmitted >= s.cap {
		return otherBoxLabel
	}
	s.admitted[box] = struct{}{}
	return box
}

// boxLabel exposes the admission set as an OpsMetrics method so callers
// don't need to know the underlying type. Safe on a nil receiver —
// returns the input unchanged for the daemon paths that don't wire an
// OpsMetrics (unit tests, see sched / vmmd test fixtures).
func (m *OpsMetrics) boxLabel(box string) string {
	if m == nil || m.boxLabels == nil {
		return box
	}
	return m.boxLabels.admit(box)
}

// appLabelSet is the bounded admission set that backs the
// app-labelled metrics on OpsMetrics (issue #1059 / ADR-127 §3.5
// deferred work, shipped in the platform-observability mega-PR).
// Same shape and contract as boxLabelSet / accountLabelSet /
// ipLabelSet — plain map + mutex, fixed capacity, non-evicting —
// but a distinct type so the four sets do not share capacity
// (an account-surge must not steal slots from the app admission
// set, and vice versa). The cap is maxAppLabelValues (256). Above
// the cap, new app slugs collapse to otherAppLabel ("__other__")
// so the Prometheus TSDB series set stays bounded over the
// daemon's lifetime. The map is initialised once per OpsMetrics
// in NewOpsMetrics; the mutex is the only synchronisation
// primitive and is held only across the lookup/insert path.
// Prometheus Counter / Histogram increments happen outside the
// critical section.
//
// Reserved values (labelAppUnknown, otherAppLabel) are admitted
// at boot without consuming capacity and are always re-admitted
// on collision-free lookups. labelAppUnknown ("") is the
// missing-app-slug placeholder used by hook sites that fire
// before app_id is resolved — it is observable distinctly from a
// real app slug that hit the admission cap (which collapses to
// otherAppLabel). Real app slugs consume capacity once and are
// never evicted in process — the daemon restart is the only path
// that resets the set.
type appLabelSet struct {
	mu       sync.Mutex
	admitted map[string]struct{}
	cap      int
}

// newAppLabelSet constructs an admission set with the given
// capacity. capacity must be > 0; the call panics otherwise to
// fail loud at boot rather than silently allow unbounded
// admission.
//
// Returns a pointer because appLabelSet contains a sync.Mutex;
// returning by value would copy the lock (govet copylocks).
func newAppLabelSet(capacity int) *appLabelSet {
	if capacity <= 0 {
		panic("wire: appLabelSet capacity must be positive")
	}
	s := &appLabelSet{
		admitted: make(map[string]struct{}, capacity),
		cap:      capacity,
	}
	// Reserved values don't count against the cap, but pre-admitting
	// them at construction means appLabel() doesn't need a special
	// branch for them — the lookup short-circuits through the same
	// map. labelAppUnknown is the empty string; pre-admitting it is
	// a no-op for the empty-string case but documents the contract.
	s.admitted[labelAppUnknown] = struct{}{}
	s.admitted[otherAppLabel] = struct{}{}
	return s
}

// admit resolves an app slug to its label value (issue #1059 /
// ADR-127 §3.5). Empty input stays as labelAppUnknown ("") so a
// missing app slug (pathological — the host usually supplies
// one) is observable distinctly from a real app slug that hit
// the admission cap (which collapses to otherAppLabel). Reserved
// values (labelAppUnknown, otherAppLabel) are always admitted
// without consuming capacity. Real app slugs are admitted up to
// the capacity; further slugs collapse to otherAppLabel without
// ever consuming capacity, and the underlying map is never
// resized past cap.
//
// Concurrency: holds mu across the lookup+insert. The hot path
// is the "already admitted" lookup, which is O(1) and never
// inserts. The Prometheus increment happens at the call site
// AFTER admit returns, so it is outside the critical section.
//
// Pointer receiver: the type contains a sync.Mutex, so copying
// the value would duplicate the lock. appLabelSet is constructed
// once per OpsMetrics in NewOpsMetrics and held as a pointer
// field.
func (s *appLabelSet) admit(app string) string {
	switch app {
	case labelAppUnknown, otherAppLabel:
		return app
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[app]; ok {
		return app
	}
	// Reserved labels (labelAppUnknown, otherAppLabel) are
	// pre-admitted at construction (see newAppLabelSet) and consume
	// map entries but NOT user-facing capacity. The user-facing cap
	// of `s.cap` distinct REAL app slugs must hold. The check is
	// therefore "real app slugs admitted = (len - reserved) >= s.cap",
	// not "len >= s.cap" — the same bug-fix shape as
	// boxLabelSet.admit (TestWakeFailure_OverflowCollapsesToOther).
	const reservedCount = 2
	realAdmitted := len(s.admitted) - reservedCount
	if realAdmitted >= s.cap {
		return otherAppLabel
	}
	s.admitted[app] = struct{}{}
	return app
}

// appLabel exposes the admission set as an OpsMetrics method so
// callers don't need to know the underlying type. Safe on a nil
// receiver — returns the input unchanged for the daemon paths
// that don't wire an OpsMetrics (unit tests, see sched / vmmd
// test fixtures).
func (m *OpsMetrics) appLabel(app string) string {
	if m == nil || m.appLabels == nil {
		return app
	}
	return m.appLabels.admit(app)
}

// RenderSeconds is a tiny helper for callers that want to hand-format a
// duration into the Prometheus convention (seconds, fixed-point with
// nanosecond precision). Avoids the float64-from-time.Duration dance
// duplicating across handlers.
func RenderSeconds(d time.Duration) string {
	// strconv.FormatFloat with -1 precision emits the shortest string
	// that round-trips back to the same float64 — Prometheus expects
	// fixed-point but tolerates either.
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

// cpuSecondsLastSeen is the per-(app, node) memory of the last
// CPUSeconds value the rollup saw (issue #279 / PR-B). It is the
// regression guard for the cumulative-counter rollup. Wire shape
// is the same NUL-joined (app, node) key as the existing
// splitKey; the in-memory store is a plain map + mutex. The set
// is bounded structurally by #apps × #nodes (ADR-036) so no
// eviction is needed — when an (app, node) tuple disappears (the
// app is parked, the node is removed) the entry is just left in
// place; on a reappearance with a smaller value the regression
// branch handles the reset.
type cpuSecondsLastSeen struct {
	mu sync.Mutex
	m  map[string]float64
}

func newCPUSecondsLastSeen() *cpuSecondsLastSeen {
	return &cpuSecondsLastSeen{m: make(map[string]float64)}
}

// add computes the per-tick delta: returns the (curr - last)
// delta to Add to the CounterVec. On a regression (curr < last)
// the baseline is reset to curr and the returned delta is 0
// (counter stays monotonic). On the first observation (last
// missing) the full curr is returned — the vmmd cpustats cache
// resets its own baseline on regression so the first post-restart
// reading is a fresh cumulative value, not a delta.
func (s *cpuSecondsLastSeen) add(key string, curr float64) float64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.m[key]
	if !ok || curr < prev {
		s.m[key] = curr
		return 0
	}
	delta := curr - prev
	s.m[key] = curr
	return delta
}

// cpuThrottleLastSeen is the per-(account_id, app_id) memory of
// the last ThrottledUsec value the rollup saw (issue #301,
// ADR-044). Mirrors cpuSecondsLastSeen (issue #279 / PR-B) — the
// regression guard for throttleSecondsTotal. The key is the
// NUL-joined composite so the rollup in ReplaceInstanceStats can
// reuse the same key-join / splitKey approach as the existing
// CPUSeconds pass. The set is bounded structurally by the (account_id,
// app_id) cardinality at the topAppSet admission layer (max 100 +
// 1 overflow), so no eviction is needed — when an (account_id,
// app_id) pair disappears (the app is parked) the entry is just
// left in place; on a reappearance with a smaller value the
// regression branch handles the reset.
type cpuThrottleLastSeen struct {
	mu sync.Mutex
	m  map[string]float64
}

func newCPUThrottleLastSeen() *cpuThrottleLastSeen {
	return &cpuThrottleLastSeen{m: make(map[string]float64)}
}

// add computes the per-tick delta: returns the (curr - last)
// delta in seconds (the wire-side ThrottledUsec is microseconds
// and the counter is in seconds; the conversion is here so the
// call site is one obvious primitive). On a regression (curr <
// last) the baseline is reset to curr and the returned delta is
// 0 (counter stays monotonic). On the first observation (last
// missing) the curr is returned — the vmmd cpustats cache resets
// its own baseline on regression so the first post-restart reading
// is a fresh cumulative value, not a delta.
//
// curr is microseconds (matches the cpu.stat throttled_usec
// field); the return value is seconds (matches the Prometheus
// "_seconds_total" naming convention — `rate()` over a
// "_seconds_total" counter is the operationally useful
// derivative).
func (s *cpuThrottleLastSeen) add(key string, currMicroseconds float64) float64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.m[key]
	if !ok || currMicroseconds < prev {
		s.m[key] = currMicroseconds
		return 0
	}
	deltaMicroseconds := currMicroseconds - prev
	s.m[key] = currMicroseconds
	return deltaMicroseconds / 1_000_000
}

// ---------------------------------------------------------------------------
// Issue #757 / ADR-118 commit 9 — ESM (event-source mapping) metric observers.
// Mirrors the prefix-aware single-registry pattern (see
// wire-opsmetrics-single-registry): the helpers below are no-ops on nil or
// on daemons that didn't construct the ESM collectors (every daemon
// pre-instantiates the closed set, so non-schedd daemons see zero values).
// ---------------------------------------------------------------------------

// ESMPollOutcome is the closed set of outcomes ObserveESMPoll accepts.
// Matches the closed-set pre-instantiation in NewOpsMetrics.
const (
	ESMPollOutcomeSuccess = "success"
	ESMPollOutcomeEmpty   = "empty"
	ESMPollOutcomeError   = "error"
)

// ObserveESMPoll increments schedd_esm_polls_total{source, outcome}.
// Called from pkg/sched/dispatch_triggers.go::dispatchOneTrigger at the
// end of every per-trigger poll cycle. Nil receiver is a no-op (matches
// the rest of the OpsMetrics surface). Unknown outcome / source values
// are silently ignored — the closed-set guard pre-instantiates the
// label combinations at boot so an out-of-vocab label would inflate
// cardinality silently otherwise.
func (m *OpsMetrics) ObserveESMPoll(source, outcome string) {
	if m == nil || m.esmPollsTotal == nil {
		return
	}
	switch outcome {
	case ESMPollOutcomeSuccess, ESMPollOutcomeEmpty, ESMPollOutcomeError:
		// ok — closed vocab
	default:
		return
	}
	m.esmPollsTotal.WithLabelValues(source, outcome).Inc()
}

// ObserveESMRecords increments schedd_esm_records_consumed_total{source}
// by the supplied count. Called after closeBatch so the count reflects
// what survived closeBatch (NOT what passed FilterCriteria — that's a
// different counter; a future "esm_records_filtered_total" can be added
// in a follow-up if the dashboard needs the breakdown). n <= 0 is a
// no-op (negative would underflow a Prometheus counter).
func (m *OpsMetrics) ObserveESMRecords(source string, n int) {
	if m == nil || m.esmRecordsConsumedTotal == nil || n <= 0 {
		return
	}
	m.esmRecordsConsumedTotal.WithLabelValues(source).Add(float64(n))
}

// ObserveESMLag records schedd_esm_lag_seconds{source, shard}.seconds
// for a per-record lag observation. shard is the partition / stream /
// queue-name identifier; the dispatcher must collapse shards past the
// 32-bucket cap to "_agg" BEFORE calling this helper (the closed-set
// pre-instantiation only populates the `_agg` series at boot). lag < 0
// is a no-op (negative lag is a clock-skew artifact, not a real
// observation).
func (m *OpsMetrics) ObserveESMLag(source, shard string, lagSeconds float64) {
	if m == nil || m.esmLagSeconds == nil || lagSeconds < 0 {
		return
	}
	if shard == "" {
		shard = "_agg"
	}
	m.esmLagSeconds.WithLabelValues(source, shard).Observe(lagSeconds)
}

// ESMPollCounterForTest returns the pre-instantiated Prometheus
// counter child for (source, outcome). Test-only accessor (lives in
// metrics.go because the closed-set pre-instantiation is internal
// to wire). Returns an error if the requested label pair was not
// pre-instantiated by NewOpsMetrics (the dispatcher uses the closed
// vocab so out-of-vocab lookups are a code-path regression).
//
// Added for PR #993 / issue #757 review MED-2 regression test:
// pkg/sched/dispatch_triggers_test.go::TestObserveESM_ForwardsToOpsMetrics.
func (m *OpsMetrics) ESMPollCounterForTest(source, outcome string) (prometheus.Counter, error) {
	if m == nil || m.esmPollsTotal == nil {
		return nil, errors.New("OpsMetrics.esmPollsTotal not initialised")
	}
	return m.esmPollsTotal.GetMetricWithLabelValues(source, outcome)
}

// ESMRecordsCounterForTest returns the pre-instantiated Prometheus
// counter child for source. Mirrors ESMPollCounterForTest.
func (m *OpsMetrics) ESMRecordsCounterForTest(source string) (prometheus.Counter, error) {
	if m == nil || m.esmRecordsConsumedTotal == nil {
		return nil, errors.New("OpsMetrics.esmRecordsConsumedTotal not initialised")
	}
	return m.esmRecordsConsumedTotal.GetMetricWithLabelValues(source)
}
