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
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
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
//
// The NaN-for-absent convention lets the wire side collapse rows
// the poller marked Unknown without a separate Validity field.
type InstanceStatRow struct {
	AppID            string
	NodeID           string
	CPUPct           float64
	RSSMB            float64
	InflightRequests int64
}

// OpsMetrics is the (per-daemon) bundle emitted at /metrics. Construct via
// NewOpsMetrics and pass the result into every handler that wants to record
// a counter + latency histogram in the ADR-015 shape.
type OpsMetrics struct {
	registry *prometheus.Registry
	ops      *prometheus.CounterVec
	dur      *prometheus.HistogramVec
	// watchdogKills: introduced in commit 3 for the §6.1 state
	// watchdog. Labels identify the transition the watchdog forced
	// (from_state → to_state) — alerting on a non-zero rate of
	// "waking→cold_booting" labels is the spec §6.1 health signal.
	watchdogKills *prometheus.CounterVec
	// eventsWriteFail: introduced in commit 4 for the audit-log
	// emission. A non-zero rate indicates that transitions are
	// succeeding but the events row isn't being written — the state
	// row is the source of truth, so this is observation-only.
	eventsWriteFail prometheus.Counter
	// auditWriteFail: introduced in IAM-4 (ADR-035) for the apid-side
	// auth audit emit. Mirrors eventsWriteFail — a failed audit write
	// logs Warn and increments the counter; the auth action has
	// already returned 200, so this is observation-only.
	auditWriteFail prometheus.Counter
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
	// wakeIDV4Fallback: introduced in feat/wake-id review followup
	// (gaps analysis 2026-07-23, finding #6). Increments when schedd
	// mints a wake_id and uuid.NewV7 returns an error — the engine
	// falls back to uuid.New (v4) in that case so a wake is never
	// refused for ID-generation reasons, but a v4 wake_id breaks the
	// time-ordering invariant the partial index is built on. Any
	// non-zero rate indicates a broken crypto/rand subsystem and
	// should alert. Unlabelled: one counter, no cardinality.
	wakeIDV4Fallback prometheus.Counter
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
	// residentGBPerCustomer: per-plan "resident GB-hours per paying
	// customer" gauge emitted by meterd (ADR-031, PR #141). Labelled
	// by plan ∈ {free, hobby, pro, scale} so the §12 dashboard's
	// "Resident GB per paying customer" panel can split by plan while
	// the FaasResidentGbPerCustomerHigh alert rule fans out per-plan.
	// Cardinality bounded at 4 — the closed plan set is enumerated
	// in the pre-instantiation loop below so every plan label surfaces
	// in /metrics from the moment the daemon boots.
	residentGBPerCustomer *prometheus.GaugeVec
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
	// instanceStatsCollectDur: per-Tick wall-clock duration of the
	// instancestats poller. Sized to the 200 ms poller interval.
	instanceStatsCollectDur prometheus.Histogram
	// instanceStatsPartialErrors: per-node dial/decode failures.
	// Distinct from the per-op ops_total because the poller
	// intentionally prefers partial snapshots to aborting on a
	// single bad node.
	instanceStatsPartialErrors *prometheus.CounterVec
	// scaleUpDecisions: per-app scale-up trigger decisions (issue #169 /
	// #172). Counter labelled by app_id and outcome ∈ {admit,
	// reject_at_cap, no_signal}. App cardinality is bounded
	// by the number of apps with autoscale configured — the trigger
	// emits one row per decision. Outcomes are pre-instantiated so the
	// series surface in /metrics from boot (same precedent as
	// stripePushDur / buildDur).
	scaleUpDecisions *prometheus.CounterVec
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
	eventsWriteFail := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_events_write_failures_total",
		Help: "Count of state-transitions whose events audit-log row could not be written. The transition itself succeeded; this is observation-only (the state row is the source of truth).",
	})
	auditWriteFail := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_audit_write_failures_total",
		Help: "Count of apid-side auth audit emits (IAM-4, ADR-035) whose events row could not be written. The handler has already returned 200; this is observation-only.",
	})
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
	wakeIDV4Fallback := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_wake_id_v4_fallback_total",
		Help: "Count of wake_id mints where uuid.NewV7 returned an error and the engine fell back to uuid.New (v4). Any non-zero rate indicates a broken crypto/rand subsystem and breaks the time-ordering invariant the instances_wake_id_app_idx partial index is built on. Should never increment in production.",
	})
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
	instanceStatsPartialErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_instance_stats_partial_errors_total",
		Help: "Per-node dial/decode failures during an instancestats poller Tick (issue #170 / PR-A). The poller logs and continues on partial failures; a non-zero rate points at a sick node.",
	}, []string{"node"})
	// Issue #169 / #172: scale-up trigger observability. Outcome
	// label set is closed ({admit, reject_at_cap, no_signal});
	// pre-instantiated below so the rows surface in /metrics from
	// boot. App label is per-app (bounded by apps with autoscale
	// configured) — the closed outcome set means the total series
	// cardinality is O(autoscale-enabled apps × 3).
	scaleUpDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_scale_up_decisions_total",
		Help: "Per-app scale-up trigger decisions. outcome ∈ {admit, reject_at_cap, no_signal}; app label is the apps.id.",
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
	sseClients := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_sse_clients",
		Help: "Number of currently open /v1/events SSE connections (Move 3, M7.5 prep). The dashboard's per-page EventSource is one connection; the CLI's faas tail is another. Zero is the idle value.",
	})
	egressDeny := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_egress_deny_total",
		Help: "Per-CIDR drop counter for the nftables egress denylist (PR-E, spec §11 + §12). The cidr label is the DenyEntry.CounterName (e.g. \"drop_v4_10_0_0_0_8\") and the family label is the nft family keyword (\"ip\" / \"ip6\"). The vmmd scrape adapter (cmd/vmmd/poller.go) reads `nft list counters` every 15s and emits the per-counter delta so the Prometheus series sees the rate of drops per CIDR. The imaged-side mirror is oci_egress_deny_total on cmd/imaged's registry because the OCI dialer is user-space — nftables counters do not see it.",
	}, []string{"cidr", "family"})
	// PR-E sister collector for the user-space OCI dialer. Only
	// registered when prefix == "imaged" — on every other daemon the
	// field stays nil and the imaged-side hook in cmd/imaged/main.go
	// must nil-check the accessor (EgressDenySeries / OCIEgressDeny)
	// before calling. The metric name is oci_egress_deny_total so an
	// operator can disambiguate "firewall blocked it" (this metric on
	// vmmd) from "dialer refused it" (this metric on imaged) — they
	// have different remediation paths.
	var ociEgressDeny *prometheus.CounterVec
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
	if prefix == "imaged" {
		ociEgressDeny = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_oci_egress_deny_total",
			Help: "Per-CIDR user-space dialer denial counter (PR-E, spec §11 + §12). Same (cidr, family) label set as egress_deny_total, but counts dialer refusals (oci.EgressDialContext returned ErrImageEgressDenied) rather than kernel-layer nftables drops. The two metrics together let an operator see whether a tenant's blocked pull hit the firewall first (egress_deny_total) or the user-space check (oci_egress_deny_total) — different levers.",
		}, []string{"cidr", "family"})
		reg.MustRegister(ops, dur, watchdogKills, eventsWriteFail, auditWriteFail, stripePushDur, paddlePushDur, buildDur, buildQueueWait, residentGBPerCustomer, wakeIDV4Fallback, imagedOCIPull, instanceCPUPct, instanceRSSMB, instanceInflightReqs, instanceStatsCollectDur, instanceStatsPartialErrors, scaleUpDecisions, scaleUpAdmitRPS, sseClients, egressDeny, ociEgressDeny, provenanceWrites)
	} else {
		reg.MustRegister(ops, dur, watchdogKills, eventsWriteFail, auditWriteFail, stripePushDur, paddlePushDur, buildDur, buildQueueWait, residentGBPerCustomer, wakeIDV4Fallback, imagedOCIPull, instanceCPUPct, instanceRSSMB, instanceInflightReqs, instanceStatsCollectDur, instanceStatsPartialErrors, scaleUpDecisions, scaleUpAdmitRPS, sseClients, egressDeny, provenanceWrites)
	}
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
	// Pre-instantiate the closed plan set for the residentGBPerCustomer
	// gauge so its HELP/TYPE and zero-valued samples surface in /metrics
	// from the moment the daemon boots — same precedent as the histogram
	// pre-instantiation above. An idle box with zero paying customers
	// would otherwise render the dashboard panel as "no data" until at
	// least one plan tick has fired (ADR-031).
	for _, plan := range api.Plans {
		residentGBPerCustomer.WithLabelValues(string(plan))
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
	for _, outcome := range []string{"admit", "reject_at_cap", "no_signal"} {
		scaleUpDecisions.WithLabelValues("", outcome)
	}
	return &OpsMetrics{
		registry:                   reg,
		ops:                        ops,
		dur:                        dur,
		watchdogKills:              watchdogKills,
		eventsWriteFail:            eventsWriteFail,
		auditWriteFail:             auditWriteFail,
		stripePushDur:              stripePushDur,
		paddlePushDur:              paddlePushDur,
		buildDur:                   buildDur,
		buildQueueWait:             buildQueueWait,
		residentGBPerCustomer:      residentGBPerCustomer,
		wakeIDV4Fallback:           wakeIDV4Fallback,
		imagedOCIPull:              imagedOCIPull,
		instanceCPUPct:             instanceCPUPct,
		instanceRSSMB:              instanceRSSMB,
		instanceInflightReqs:       instanceInflightReqs,
		instanceStatsCollectDur:    instanceStatsCollectDur,
		instanceStatsPartialErrors: instanceStatsPartialErrors,
		scaleUpDecisions:           scaleUpDecisions,
		scaleUpAdmitRPS:            scaleUpAdmitRPS,
		sseClients:                 sseClients,
		egressDeny:                 egressDeny,
		ociEgressDeny:              ociEgressDeny,
		provenanceWrites:           provenanceWrites,
	}
}

// WatchdogKills returns the per-(from_state, to_state) counter the
// §6.1 watchdog increments when it transitions a stuck instance.
// The returned Counter can be safely cached by callers; the underlying
// CounterVec is shared with other label tuples.
func (m *OpsMetrics) WatchdogKills(fromState, toState string) prometheus.Counter {
	return m.watchdogKills.WithLabelValues(fromState, toState)
}

// EventsWriteFailures returns the unlabelled counter for audit-log
// writes that failed. The transition itself succeeded; this counter
// only signals observability debt. See also commit 4.
func (m *OpsMetrics) EventsWriteFailures() prometheus.Counter {
	return m.eventsWriteFail
}

// AuditWriteFailures returns the unlabelled counter for IAM-4
// (ADR-035) auth audit emits whose events row could not be written.
// The handler has already returned 200 to the customer; this counter
// only signals observability debt. Same posture as
// EventsWriteFailures.
func (m *OpsMetrics) AuditWriteFailures() prometheus.Counter {
	return m.auditWriteFail
}

// WakeIDV4Fallback returns the unlabelled counter the wake_id mint
// path increments when uuid.NewV7 fails and the engine falls back to
// uuid.New (v4). Review finding #6 (gaps analysis 2026-07-23): any
// non-zero rate indicates a broken crypto/rand subsystem and silently
// breaks the time-ordering invariant the partial index is built on.
func (m *OpsMetrics) WakeIDV4Fallback() prometheus.Counter {
	return m.wakeIDV4Fallback
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

// Registry returns the underlying registry — pass to promhttp.HandlerFor
// if you want to share it with metrics from elsewhere.
func (m *OpsMetrics) Registry() *prometheus.Registry { return m.registry }

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

// Handler returns an http.Handler that serves the registry's metrics.
// Plug into any mux — daemons mount it at /metrics.
func (m *OpsMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		Registry: m.registry,
	})
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
