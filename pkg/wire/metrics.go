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
	"sync"
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
	// alertDeliveryAttemptsTotal — counts dispatched alert-rule
	// webhook attempts, labelled by outcome ∈ {delivered, failed}.
	// Label cardinality budget = 2 (closed vocabulary). The counter
	// surfaces the dispatcher's success rate without exposing
	// per-customer detail (account_id is intentionally absent — the
	// audit events table is the per-customer detail; this is the
	// fleet-wide counter for the §12 dashboard).
	alertDeliveryAttemptsTotal *prometheus.CounterVec
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
	// (cmd/apid/topn.go / cmd/gatewayd/listener.go). Bounded at
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
	// expose the path (apid, imaged, builderd, gatewayd, meterd,
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
	// imageScanVulns: issue #299 / supply-chain scan observability.
	// Counter labelled by image (the OCI ref of the staged base
	// ext4, e.g. "ghcr.io/onebox-faas/builder-base:latest" or
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
	auditWriteFail := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_audit_write_failures_total",
		Help: "Count of apid-side auth audit emits (IAM-4, ADR-035) whose events row could not be written, labelled by account_id. The handler has already returned 200; this is observation-only. account_id=\"__other__\" is the bounded admission overflow bucket — operators must check daemon slog for the original id (issue #278).",
	}, []string{"account_id"})
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
	requestFailures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_request_failures_total",
		Help: "HTTP requests completed with status >= 400, labelled by account_id, the route template, and code ∈ {ok, err} (issue #278, PR #336, ADR-039). code is \"err\" for every increment today (the counter only fires on status >= 400); the label is added so the failure counter mirrors requestTotal and the per-account error-rate view derives from a single source. account_id=\"anonymous\" is the unauthenticated path; account_id=\"__other__\" is the bounded admission overflow bucket. route is r.Pattern (e.g. \"GET /v1/apps/{slug}\") or \"unmatched\" for paths the mux did not dispatch.",
	}, []string{"account_id", "route", "code"})
	requestTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_request_total",
		Help: "HTTP requests completed, labelled by account_id, route, and code (issue #303, ADR-039). The counter is the per-request total — paired with requestFailures (status >= 400 only) for the per-account error-rate view. account_id flows through the same accountLabelSet as requestFailures so a customer is represented by their real id in both, or by \"__other__\" in both. code ∈ {ok, err} (ok on 2xx/3xx, err on 4xx/5xx). route is r.Pattern or \"unmatched\". Backed by the §12 traffic-anomaly recording rules (faas_apid_request_rate_5m, _3d_baseline, _ratio).",
	}, []string{"account_id", "route", "code"})
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
	alertDeliveryAttemptsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_alert_delivery_attempts_total",
		Help: "Count of dispatched alert-rule webhook attempts, labelled by outcome ∈ {delivered, failed} (closed vocabulary, cardinality budget = 2). The counter surfaces the dispatcher's success rate without exposing per-customer detail — the audit events table is the per-customer detail.",
	}, []string{"outcome"})
	for _, outcome := range []string{"delivered", "failed"} {
		alertDeliveryAttemptsTotal.WithLabelValues(outcome)
	}
	alertEvaluatorEnabled := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_alert_evaluator_enabled",
		Help: "1 when the pkg/alerts.Evaluator tick is wired and running on this meterd process; 0 when it isn't (Prometheus not configured, FAAS_HOST_AGE_IDENTITY_PATH empty, no host.age on disk, or meterd's caller skipped the loop). Unlabelled. Pair with alertEvalFiredTotal / alertEvalSkippedDegradedTotal for the dashboard's alert-evaluation-health panel; alert rule 'alertEvalDisabled' queries this gauge for the §12 self-healing alert.",
	})
	// Initialise to 0 so /healthz reports "evaluator disabled" from
	// boot until cmd/meterd explicitly enables it — the absence of a
	// gauge series would otherwise look like "never scraped", which
	// Prometheus treats as a missing time series rather than zero.
	alertEvaluatorEnabled.Set(0)
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
	// path (apid, imaged, builderd, gatewayd, meterd, githubd,
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
	// cooldown_held}); pre-instantiated below so the rows surface
	// in /metrics from boot. App label is per-app (bounded by apps
	// with autoscale configured) — the closed outcome set means
	// the total series cardinality is O(autoscale-enabled apps × 4).
	// cooldown_held (PR-C, issue #462) lands on the wake-gate
	// path when Concurrency(appID) > 0 AND
	// time.Since(apps.last_scale_out_at) < ScaleOutCooldownS.
	scaleUpDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_scale_up_decisions_total",
		Help: "Per-app scale-up trigger decisions. outcome ∈ {admit, reject_at_cap, no_signal, cooldown_held}; app label is the apps.id.",
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
	scaleDownDecisions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_scale_down_decisions_total",
		Help: "Per-app aggressive-reaper decisions (issue #171). outcome ∈ {park, keep, min_floor_already}; app label is the apps.id.",
	}, []string{"app", "outcome"})
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
	// commonCollectors is the per-daemon collector set that every
	// prefix registers. PR-E adds ociEgressDeny to the set when
	// prefix == "imaged" — keeping the common slice as a single source
	// of truth (review finding #5 on PR #332) means a future collector
	// only needs to be added here, not in two parallel MustRegister
	// calls that would silently drift apart.
	commonCollectors := []prometheus.Collector{
		ops, dur, watchdogKills, eventsWriteFail, auditWriteFail,
		auditWriteDur, requestFailures, requestTotal, stripePushDur, paddlePushDur,
		buildDur, buildQueueWait, residentGBPerCustomer, billingCapExceededTotal,
		wakeIDV4Fallback,
		snapshotDiskDrift,
		imagedOCIPull, instanceCPUPct, instanceRSSMB, instanceInflightReqs,
		instanceCPUSecondsTotal,
		instanceStatsCollectDur, instanceStatsPartialErrors,
		scaleUpDecisions, scaleDownDecisions, scaleUpAdmitRPS, sseClients,
		egressDeny,
		failedLoginTotal, failedLoginDropped,
		failedLoginAuditWriteFailures,
		topTenantRPS,
		apidLogsEmittedTotal,
		oauthDisabledTotal,
		advisoryBatchesEmittedTotal,
		apidStatelessAdvisoryEventsTotal,
		apidGithubdBridgeEnqueuedTotal,
		githubdPathFilterTotal,
		throttleSecondsTotal, throttleRatio,
		egressSourceErrors,
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
	// `kind` label set ({github} — the only kind the bridge
	// produces today; the loop is forward-compat for future
	// daemon-to-daemon build sources). Same pattern as the
	// pre-instantiations above so the row surfaces in /metrics
	// from boot.
	for _, kind := range []string{"github"} {
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
	// Pre-instantiate the closed plan set for the billingCapExceeded
	// counter (issue #279). Same precedent as residentGBPerCustomer
	// above. An idle box with no cap-hits would otherwise render the
	// §12 dashboard panel as "no data" until the first cap hit.
	for _, plan := range api.Plans {
		billingCapExceededTotal.WithLabelValues(string(plan))
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
	for _, outcome := range []string{"admit", "reject_at_cap", "no_signal", "cooldown_held"} {
		scaleUpDecisions.WithLabelValues("", outcome)
	}
	// Issue #300: pre-instantiate the ("other",) row of the per-tenant
	// RPS gauge so the help/TYPE surfaces in /metrics from boot, before
	// the first 5s sampler tick fires. Same precedent as the closed
	// scale-up outcome / egress-deny catalog loops above. Real customer
	// ids are added by TopTenantRPSFor (cmd/apid/topn.go / cmd/gatewayd/
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
	// issue #171: pre-instantiate the {park, keep, min_floor_already}
	// outcome rows for the empty-app label so the help/TYPE surfaces
	// in /metrics from boot, mirroring the scale-up pattern above.
	// min_floor_already (PR-C, issue #462) is the per-app "would
	// have parked, but min_instances is reached" outcome the
	// aggressive reaper emits.
	for _, outcome := range []string{"park", "keep", "min_floor_already"} {
		scaleDownDecisions.WithLabelValues("", outcome)
	}
	// issue #279 (PR-B, CPU-hour visibility): pre-instantiate the
	// empty (app, node) row so the help/TYPE surfaces in /metrics
	// from boot. Same precedent as the scale-up / scale-down
	// outcome rows above. Real per-(app, node) rows are added by
	// the rollup in ReplaceInstanceStats.
	instanceCPUSecondsTotal.WithLabelValues("", "")
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
		registry:                         reg,
		ops:                              ops,
		dur:                              dur,
		watchdogKills:                    watchdogKills,
		eventsWriteFail:                  eventsWriteFail,
		auditWriteFail:                   auditWriteFail,
		auditWriteDur:                    auditWriteDur,
		requestFailures:                  requestFailures,
		requestTotal:                     requestTotal,
		accountLabels:                    newAccountLabelSet(maxAccountLabelValues),
		failedLoginTotal:                 failedLoginTotal,
		failedLoginDropped:               failedLoginDropped,
		failedLoginAuditWriteFailures:    failedLoginAuditWriteFailures,
		alertEvalSkippedDegradedTotal:    alertEvalSkippedDegradedTotal,
		alertEvalFiredTotal:              alertEvalFiredTotal,
		alertDeliveryAttemptsTotal:       alertDeliveryAttemptsTotal,
		alertEvaluatorEnabled:            alertEvaluatorEnabled,
		pgBackupLastPushed:               pgBackupLastPushed,
		ipLabels:                         newIPLabelSet(maxIPLabelValues),
		topTenantRPS:                     topTenantRPS,
		topAccounts:                      newTopAccountSet(topAccountSetCap),
		throttleSecondsTotal:             throttleSecondsTotal,
		throttleRatio:                    throttleRatio,
		topApps:                          newTopAppSet(topAppSetCap),
		throttleSecondsLastSeen:          newCPUThrottleLastSeen(),
		cpuSecondsLast:                   newCPUSecondsLastSeen(),
		stripePushDur:                    stripePushDur,
		paddlePushDur:                    paddlePushDur,
		buildDur:                         buildDur,
		buildQueueWait:                   buildQueueWait,
		residentGBPerCustomer:            residentGBPerCustomer,
		billingCapExceededTotal:          billingCapExceededTotal,
		wakeIDV4Fallback:                 wakeIDV4Fallback,
		snapshotDiskDrift:                snapshotDiskDrift,
		capacitySignatureRejected:        capacitySignatureRejected,
		imagedOCIPull:                    imagedOCIPull,
		instanceCPUPct:                   instanceCPUPct,
		instanceRSSMB:                    instanceRSSMB,
		instanceInflightReqs:             instanceInflightReqs,
		instanceCPUSecondsTotal:          instanceCPUSecondsTotal,
		instanceStatsCollectDur:          instanceStatsCollectDur,
		instanceStatsPartialErrors:       instanceStatsPartialErrors,
		cpuStatsCollectDur:               cpuStatsCollectDurLocal,
		scaleUpDecisions:                 scaleUpDecisions,
		scaleDownDecisions:               scaleDownDecisions,
		scaleUpAdmitRPS:                  scaleUpAdmitRPS,
		sseClients:                       sseClients,
		egressDeny:                       egressDeny,
		ociEgressDeny:                    ociEgressDeny,
		provenanceWrites:                 provenanceWrites,
		imageScanVulns:                   imageScanVulns,
		apidLogsEmittedTotal:             apidLogsEmittedTotal,
		egressSourceErrors:               egressSourceErrors,
		oauthDisabledTotal:               oauthDisabledTotal,
		advisoryBatchesEmittedTotal:      advisoryBatchesEmittedTotal,
		apidStatelessAdvisoryEventsTotal: apidStatelessAdvisoryEventsTotal,
		apidGithubdBridgeEnqueuedTotal:   apidGithubdBridgeEnqueuedTotal,
		githubdPathFilterTotal:           githubdPathFilterTotal,
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
// path (apid, imaged, builderd, gatewayd, meterd, githubd,
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

// ObserveScaleDown records one aggressive-reaper scale-down decision
// (issue #171). One observation per app per 10 s reaper tick that ran
// the new code path. outcome ∈ {park, keep}; "park" is emitted once
// per app per tick even when multiple instances are parked. Safe on
// a nil receiver so schedd unit tests without metrics keep working.
func (m *OpsMetrics) ObserveScaleDown(app, outcome string) {
	if m == nil {
		return
	}
	m.scaleDownDecisions.WithLabelValues(app, outcome).Inc()
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

// GithubdBridgeKindGitHub is the only value the `kind` label
// takes on the githubd → apid build enqueue counter (issue #432
// phase 5). Mirrored as the constant the apid-side bridge
// increments via IncGithubdBridgeEnqueued. The loop in
// NewOpsMetrics pre-instantiates this value so the row surfaces
// in /metrics from boot — same precedent as the advisorySeverity
// closed-set above.
const (
	GithubdBridgeKindGitHub = "github"
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
// githubd_bridge_enqueued_total{kind} (issue #432 phase 5). Called
// from cmd/apid/githubd_bridge.go's EnqueueBuild handler on each
// landed build row. kind is GithubdBridgeKindGitHub — the only
// value the githubd bridge produces today; the switch is
// forward-compat for future daemon-to-daemon build sources. Unknown
// values produce no increment (closed-set guard). Nil receiver is
// allowed for parity with the other Observe* accessors — apid
// unit tests that don't wire metrics keep working.
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
	case GithubdBridgeKindGitHub:
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
	case "", "unknown":
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
