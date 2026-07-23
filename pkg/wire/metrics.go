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
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
	stripePushDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: prefix + "_stripe_push_duration_seconds",
		Help: "Per-push latency to Stripe, labelled by terminal result code (ok on success, or a stripe.ClassifyPushError label on failure).",
		// Sized for Stripe's documented SLA: p99 ≈ 5 s, p99.9 ≈ 30 s,
		// 60 s ceiling = documented API timeout.
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
	reg.MustRegister(ops, dur, watchdogKills, eventsWriteFail, stripePushDur, buildDur, buildQueueWait, residentGBPerCustomer, wakeIDV4Fallback, imagedOCIPull)
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
	// Pre-instantiate the closed plan set for the residentGBPerCustomer
	// gauge so its HELP/TYPE and zero-valued samples surface in /metrics
	// from the moment the daemon boots — same precedent as the histogram
	// pre-instantiation above. An idle box with zero paying customers
	// would otherwise render the dashboard panel as "no data" until at
	// least one plan tick has fired (ADR-031).
	for _, plan := range api.Plans {
		residentGBPerCustomer.WithLabelValues(string(plan))
	}
	return &OpsMetrics{
		registry:              reg,
		ops:                   ops,
		dur:                   dur,
		watchdogKills:         watchdogKills,
		eventsWriteFail:       eventsWriteFail,
		stripePushDur:         stripePushDur,
		buildDur:              buildDur,
		buildQueueWait:        buildQueueWait,
		residentGBPerCustomer: residentGBPerCustomer,
		wakeIDV4Fallback:      wakeIDV4Fallback,
		imagedOCIPull:         imagedOCIPull,
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

// WakeIDV4Fallback returns the unlabelled counter the wake_id mint
// path increments when uuid.NewV7 fails and the engine falls back to
// uuid.New (v4). Review finding #6 (gaps analysis 2026-07-23): any
// non-zero rate indicates a broken crypto/rand subsystem and silently
// breaks the time-ordering invariant the partial index is built on.
func (m *OpsMetrics) WakeIDV4Fallback() prometheus.Counter {
	return m.wakeIDV4Fallback
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
