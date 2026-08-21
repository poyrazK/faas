// Prometheus instrumentation for gatewayd-internal (spec §4.1, §12). The metric names
// here are dashboard dependencies — DO NOT rename without coordinating with
// the dashboards in deploy/grafana/. We register on a per-Handler registry
// (not the global default) so concurrent tests don't collide.
//
// Emitted series:
//   - gateway_requests_total{app, plan, code}        counter
//   - gateway_request_duration_seconds{app, class}   histogram (issue #273 /
//     ADR-042; full request-received → handler-return duration, not TTFB;
//     class ∈ {2xx,3xx,4xx,5xx}; per-app series pre-instantiated by
//     PreInstantiateApp so the §12 panel surfaces from first request)
//   - gateway_wake_latency_seconds                    histogram
//   - gateway_wake_queue_wait_seconds                 histogram (M8 §12 dashboard)
//   - gateway_queue_depth{app}                       gauge (set/cleared by
//     WakeGate.SetGaugeSink)
//   - gateway_rate_limited_total{app, plan}          counter
//   - gateway_cold_boot_total{app}                   counter (renamed from
//     gateway_cold_wake_total in #273 / ADR-042; zero external consumers so
//     it is a straight rename, not a dual-emit migration)
//   - gateway_top_tenant_rps{account_id}             gauge (issue #300;
//     label value is the resolved app_id — gatewayd-internal's only
//     tenant-attributable key — see ObserveTopTenantRPS)
//   - gateway_tls_cert_expiry_seconds                gauge (ADR-024 H3, closed
//     in PR #345; refreshed every 5 min by StartCertExpiryRefresher; smallest
//     remaining lifetime across cached certs on disk; negative when a cert
//     is already expired — the page rule fires regardless)
//   - gateway_tls_on_demand_denied_total{reason}     counter (ADR-024 H3, closed
//     in PR #345; reason ∈ {allowlist, dns01, token} — only allowlist is wired
//     today; dns01 + token pre-instantiate at 0 so the panel surfaces from
//     boot and a missing wire-incrementation is visible as a frozen zero.
//     ADR-024 H3.b is the still-open follow-up to bridge the certmagic zap
//     logger into this counter)
//   - gateway_response_bytes_total{app, plan}      counter (ADR-046 PR-2;
//     per-(app, plan) HTTP response body bytes the gateway observed
//     from the ReverseProxy. Canonical persisted metric is
//     usage_minutes.tx_bytes; this counter is the §12
//     FaasTenantEgressSpike real-time operator view — "rate > 1GiB/min
//     sustained 5m on a single (app, plan) pair". Lives on the
//     gatewayd-internal-local registry (the daemon's /metrics scrape); the
//     cross-daemon contract is not duplicated here becausegatewayd-internal
//     does not construct pkg/wire.OpsMetrics today. If a future
//     daemon ever needs to surface this counter alongside its
//     own, instantiate it via a fresh CounterVec — do NOT bring
//     back a wire-side mirror without a real consumer.)
//   - gateway_wake_locality_total{outcome}          counter (PR scale-out
//     readiness; outcome ∈ {local_snapshot, local_coldboot} today;
//     remote_* outcomes slot in transparently when the second compute
//     node joins — pins the wake-locality ratio so the sticky-vs-shared
//     snapshot-store decision is a measurement, not a guess)
//   - gateway_compute_node_changed_subscriber_alive  unlabelled gauge (PR
//     scale-out readiness; bumped every 30s by the LISTEN
//     compute_node_changed subscriber loop in cmd/gatewayd-internal/nodecache.go.
//     Stale gauge means the NodeClientCache is silently out of date and
//     the next compute_nodes UPSERT is invisible to placement — page
//     rule fires when the gauge freezes or drops to 0. The hook is the
//     OBSERVABILITY point; the alert rule + dashboard panel + window
//     choice live in ops wiring, out of scope for this PR.)
//   - gateway_stream_flushes_total{app, plan}    counter (ADR-047 PR-B;
//     one inc per statusRecorder.doFlush on the streaming path — periodic
//     256 KiB / 200 ms triggers plus the residual capture. Ratio of
//     stream_flushes_total to requests_total for streaming apps
//     approximates avg flush count per response. Bounded to (app, plan)
//     per the responseBytes counter; the streaming app set is
//     plan-bounded via pgRouter.)
//   - gateway_stream_active{app, plan}          gauge (ADR-047 PR-D;
//     concurrent in-flight streaming requests. Inc'd in
//     setupStreamingWriter when the streaming path is chosen; Dec'd
//     in the handler's defer after the final flush. Pre-instantiated
//     under the `__other__` placeholder for every closed plan so the
//     §12 panel surfaces from boot — a quiet daemon reads "0 active
//     streams" rather than "no data". Nil-safe via the ObserveStreamStart
//     / ObserveStreamEnd wrappers so the handler hot path doesn't need
//     to gate on every call.)
package gateway

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// edgeRuleKinds is the closed set of (kind, outcome/result)
// labels pre-instantiated on the edgeRuleMatch + edgeRuleApply
// + edgeRuleCompileError counters so the §12 dashboard
// surfaces every tuple from first scrape (no "missing until
// first hit" gaps). Single source of truth — both pre-
// instantiation loops iterate the same slice; a drift between
// the two loops would mask a metric on one side. The drift
// guard at TestEdgeRuleKindsAgreeWithCallSites (in
// metrics_drift_test.go) pins the contract.
//
// Composition (kept in sync with the per-feature contract):
//
//   - Edge-rule kinds (route, rewrite, redirect, headers,
//     cors, jwt, ip, validate, limit, maintenance, geo,
//     throttle) come from migrations/00192_edge_rules.sql:49-51
//   - the ADR-091 hardening PR-A + the D20.5 (issue #881)
//     throttle extension.
//   - Ingress-control kinds (ingress_ip, ingress_members) are
//     the per-app ingress-control gates (ADR-118 ip_allowlist,
//     ADR-123 members_only). The internal_only gate uses
//     ObserveInternalAuthMatch (its own dedicated counter
//     family — see F4 commit) rather than the edge-rule
//     counters, so `ingress_internal` does NOT appear here
//     even though ADR-119 once carved it out as a future-
//     extension point.
//
// Adding a new kind:
//  1. Append to edgeRuleKinds below.
//  2. Emit via ObserveEdgeRuleMatch / ObserveEdgeRuleApply
//     from the new gate's call site.
//  3. The drift guard's call-site scan will catch any new
//     kind that emits without being in the closed set (the
//     guard is a one-way safety net — adding a kind without
//     emitting it surfaces as a noisy zero-value counter
//     on the dashboard, which is acceptable).
var edgeRuleKinds = []string{
	"rewrite",
	"redirect",
	"headers",
	"cors",
	"jwt",
	"ip",
	"validate",
	"limit",
	"maintenance",
	"geo",
	"throttle",
	"budget",
	"ingress_ip",
	"ingress_members",
}

// Metrics is the gatewayd-internal Prometheus bundle. Construct once per Handler via
// NewMetrics and pass into NewHandlerWith.
type Metrics struct {
	registry *prometheus.Registry

	requests    *prometheus.CounterVec
	wakeLatency prometheus.Histogram
	// drainWaitSeconds (issue #587 / PR-A) is the per-daemon
	// graceful-shutdown drain histogram (see ObserveDrainWait).
	drainWaitSeconds *prometheus.HistogramVec
	// inflightRequests (issue #587 / PR-A) is the per-daemon
	// drain.Tracker in-flight gauge (see SetInflightRequests).
	inflightRequests *prometheus.GaugeVec
	// wakeLatencyByNode (PR #4 / ADR-092 §3.5) is the per-node
	// labelled twin of wakeLatency. The unlabeled histogram stays
	// untouched — it's the §12 SLA contract and is consumed by
	// pkg/appmetrics/appmetrics.go:175 for the fleet p95. The new
	// histogram is operator-side only; the obsNodeWakeLatency
	// handler in cmd/apid PromQL-evals it for per-node quantiles.
	// The label is the compute_node.id (UUID) rather than the
	// human-readable name because gatewayd-internal has no
	// node-id → node-name cache on the wake path; the obs handler
	// resolves id → name via the existing ListComputeNodes call
	// before returning. Label cardinality is bounded by
	// compute_nodes count (today: 1, tomorrow: tens). ADR-091
	// §3.6 documents the label cardinality constraint.
	wakeLatencyByNode *prometheus.HistogramVec
	wakeQueueWait     prometheus.Histogram
	// wakePhaseDuration (ADR-098 C11): phase-decomposed wake
	// latency vector. Closed phase set pre-instantiated below so
	// the §12 panel surfaces zero on an idle gateway.
	wakePhaseDuration *prometheus.HistogramVec
	queueDepth        *prometheus.GaugeVec
	rateLimited       *prometheus.CounterVec
	// leaderBootstrapAborts (ADR-098 C7): counter labelled by
	// reason — closed set {queue_empty_no_instance, ttl_expired,
	// app_deleted}. Pre-instantiated in NewMetrics so the §12
	// dashboard chip surfaces from first scrape.
	leaderBootstrapAborts *prometheus.CounterVec
	// edgeRuleMatch: ADR-089 PR 3. Counter labelled by
	// (kind, outcome) — `kind` is the EdgeRuleKind
	// (route|rewrite|redirect|headers|cors|jwt|ip; closed set
	// per migrations/00192_edge_rules.sql:49-51), `outcome` is
	// one of {match, miss, blocked}. The handler increments
	// from matchAndSubstituteRoute (handler.go:1449-1451) so the
	// §12 dashboard panel "edge rule match rate" surfaces from
	// first scrape — the closed label set is pre-instantiated
	// at boot below. PR 4-7 extend kind; the outcome set is
	// stable across all kinds.
	edgeRuleMatch *prometheus.CounterVec
	// edgeRuleApply (ADR-091 hardening PR-A): counter of apply-path
	// outcomes, distinct from edgeRuleMatch (which counts the
	// matcher's pick). A rule can MATCH the matcher but FAIL at apply
	// time (e.g. JWT verify returns ErrJWKSNotRegistered, or an IP
	// rule's CIDR list is empty after redaction). The {kind, result}
	// labels partition success / error so the §12 dashboard chip
	// "edge rule apply rate" surfaces from first scrape. result is
	// one of {success, error} — a 2-element closed set; expanding it
	// requires a new metric (the apply-error mix is its own surface).
	edgeRuleApply *prometheus.CounterVec
	// edgeRuleValidateFailures (issue #975 #3 / Mega-Foundation #979-a):
	// counter of kind=validate body mismatches, labelled by
	// {mode, reason}. `mode` is the rule's validate_mode
	// (observe|warn|block — closed set; the schema enforces the
	// values at column level). `reason` is the bounded taxonomy
	// emitted by pkg/edgevalidate (required_missing |
	// type_mismatch | additional_properties_not_allowed |
	// enum_violation | format_violation | other — 6 elements, also
	// closed). The counter increments in every mode — the reject
	// decision is independent of the count, so the dashboard can
	// read the same series for "how often does this rule fail?" in
	// observe mode and for "how often would this rule have failed?"
	// in block mode. The {app_id, rule_id} pair is NOT a label on
	// this counter; the per-rule break-out is queryable via the
	// existing edgeRuleApply{result=error} counter, which is
	// incremented on the block-mode reject path. Cardinality is
	// therefore `mode × reason` = 4 × 6 = 24 (modes: observe, warn,
	// block, other — `other` is the coerce-on-unknown bucket;
	// reasons: required_missing, type_mismatch,
	// additional_properties_not_allowed, enum_violation,
	// format_violation, other), well below the 50-counter budget
	// any single Metrics instance ships.
	//
	// DEPRECATED (ADR-128 §5): the canonical metric for new
	// dashboards is `validateFailures` below (named
	// gateway_validate_failures_total per the issue spec).
	// edgeRuleValidateFailures is shadow-emitted for one release
	// so existing dashboards keep working, then dropped.
	edgeRuleValidateFailures *prometheus.CounterVec
	// validateFailures (issue #975 #3 / ADR-128): the spec-named
	// counter at gateway_validate_failures_total{app_id, rule_id,
	// mode, reason}. Replaces edgeRuleValidateFailures above as
	// the canonical observability surface. The (app_id, rule_id)
	// pair is bounded by ruleLabelSet (pkg/gateway/rule_label_set.go)
	// at 256 distinct rule IDs per app; overflow collapses to
	// rule_id="__other__" so the Prometheus series set stays
	// bounded over the daemon's lifetime. Worst-case per-app
	// cardinality is (256 rules + 1 __other__) × 4 modes × 6
	// reasons = 6168 series per app, well under Prometheus'
	// per-instance label budget.
	//
	// Pre-instantiation: NONE — Prometheus CounterVec requires
	// all labels at WithLabelValues time, so the (app_id, rule_id)
	// pair cannot be pre-instantiated without knowing runtime
	// inputs. Series surface on first scrape as rules fire.
	// Operators alerting on rate == 0 must handle the cold-start
	// window (no failure has been observed yet → no series exists
	// → rate returns no data, not zero). The legacy
	// edgeRuleValidateFailures counter above IS pre-instantiated
	// (24 combos — mode × reason only) for the §12 panel-at-day-1
	// contract; once validateFailures graduates out of the
	// shadow window, that pre-instantiation can move here as
	// synthetic (app_id, rule_id, mode, reason) tuples for the
	// known fleet.
	validateFailures *prometheus.CounterVec
	// ruleLabels (ADR-128 §D3) is the per-app admission set
	// backing the (app_id, rule_id) label pair on
	// validateFailures. See pkg/gateway/rule_label_set.go for
	// the contract — mirror of boxLabelSet / accountLabelSet with
	// per-app (instead of global) cap.
	ruleLabels *ruleLabelSet
	// routeConsumerThrottleDecisions (ADR-104, issue #881 Phase 3):
	// counter of per-consumer throttle decisions, labelled by
	// {kind, outcome}. `kind` is the KeyBy dimension
	// (none | api_key | jwt_subject | jwt_claim — closed set per
	// pkg/api.ThrottleKeyBy* constants). `outcome` is the
	// decision (admit | throttle | anonymous — the third covers
	// anonymous traffic on a per-consumer rule, which the limiter
	// passes through to the rule-only bucket). Distinct from
	// edgeRuleApply (which tracks the per-rule throttle path
	// separately) so a §12 dashboard panel can read both:
	// edgeRuleApply tracks per-rule bucket denies, this counter
	// tracks per-consumer denies within the per-rule scope.
	routeConsumerThrottleDecisions *prometheus.CounterVec
	// responseCache (ADR-122 §Decision) is the kind=cache outcome
	// counter, labelled by `outcome` ∈ {hit, miss, bypass_authed,
	// bypass_uncacheable, stale_if_error_served, store_skipped}.
	// hit_rate = hit / (hit + miss + bypass_* + stale_*); the
	// bypass_* + stale_* outcomes are reported separately so an
	// operator can see when their hit-rate numerator is being
	// muted by auth bypasses (suggesting traffic that the cache
	// will never serve) vs. uncacheable-method bypasses
	// (suggesting a rule that doesn't match the customer's actual
	// verb mix). store_skipped captures the cacheability
	// predicate veto (Set-Cookie / Cache-Control / cap overflow)
	// so a customer seeing "why isn't my cache populating?"
	// has a dashboard chip to consult.
	responseCache *prometheus.CounterVec
	// responseCacheWakesAvoided (ADR-122 §Decision) counts cache
	// hits that genuinely displaced a cold boot — i.e. a hit
	// against an app with zero healthy instances at the moment
	// of the hit. A hit against an already-warm app saves
	// latency but no compute, and must NOT be counted as savings
	// (the honesty property behind the saved-cost figure). Per-
	// app label (closed by appID cardinality).
	responseCacheWakesAvoided *prometheus.CounterVec
	// responseCacheBytes (ADR-122 §Decision) is the in-process
	// store occupancy gauge; entryBytes is the per-entry
	// distribution (avg over recent entries). Both surface on
	// the §12 panel so operators can tune the byte ceiling.
	responseCacheBytes   prometheus.Gauge
	responseCacheEntries prometheus.Gauge
	// edgeRuleCompileError (ADR-091 hardening PR-A): counter of
	// compile-time failures inside the cmd-side loader
	// (cmd/gatewayd-internal/edge_rules.go::warnPathGlobErrs). A
	// non-zero value here means a rule shipped broken — the loader
	// silently dropped the offending rule and is serving traffic
	// without it. The {kind} label is one of the seven shipped
	// kinds; the counter surfaces the signal even when the
	// loader's WARN log is drowned by other gatewayd-internal noise.
	edgeRuleCompileError *prometheus.CounterVec
	// envelopeCapWarns (issue #995 Phase 4 / ADR-121): warn
	// counters for the body-cap surfaces (request, response,
	// raw-bridge). Labels: {app_id, bucket}; bucket ∈
	// {near_threshold, exceeded}. The app_id label is bounded by
	// the platform's per-account app count (~100s) — see ADR-093
	// §cardinality for the precedent. route_label is intentionally
	// NOT admitted (could explode per ADR-093 cap of 50 per app).
	responseBodyWarnTotal *prometheus.CounterVec
	// internalAuthMatch (ADR-119 / issue #477 #4): counter of
	// apps.public_auth_mode='internal_only' ingress verifications,
	// labelled by outcome (matched | blocked). `matched` = a
	// valid Authorization: Bearer JWT with aud='gregale.internal'
	// was verified against the per-service public-key allowlist
	// (pkg/internalsvc.Verify). `blocked` = verification failed
	// (missing/invalid/expired/unknown svc). Round-2 peer-review
	// (#6) removed the `bypass_stripped` label that no code path
	// incremented (the Header.Del on internal_proxy.go was
	// unconditional, so the strip was never bypassed and the
	// counter stayed at zero forever). Distinct from edgeRuleMatch
	// (which counts per-rule edge-rule decisions, not auth events);
	// re-using edgeRuleMatch would conflate "an edge rule fired"
	// with "a JWT was verified" and break the §12 dashboard
	// "internal auth match rate" chip. Closed (outcome) set,
	// pre-instantiated at boot below.
	internalAuthMatch *prometheus.CounterVec
	// appMaintenance (ADR-091 amendment / §4.1.2.0): counter of
	// apps.maintenance_mode coarse-gate matches. Distinct from the
	// edgeRule* family because the coarse gate is not an edge
	// rule — there's no matcher, no audit row beyond the
	// app.maintenance_mode_match emit, no per-rule cap, no
	// cross-account defense. The {plan} label is the closed
	// {Free, Hobby, Pro, Scale} set so the §12 dashboard panel
	// "apps in maintenance by plan" surfaces from first scrape.
	// Pre-instantiated at boot in NewMetrics via the closed plan
	// loop below (mirrors streamActive / accountRateLimited
	// pre-instantiation pattern).
	appMaintenance *prometheus.CounterVec
	// responseBytes: ADR-046 PR-2 producer observability.
	// Counter labelled by app (UUID, bounded by per-plan app
	// quotas) and plan (Free|Hobby|Pro|Scale — closed set). The
	// canonical persisted metric is usage_minutes.tx_bytes;
	// this counter is the real-time operator view (egress
	// per app), and backs the §12 FaasTenantEgressSpike alert
	// ("rate > 1GiB/min sustained for 5m on a single app").
	// See ObserveResponseBytes below.
	responseBytes *prometheus.CounterVec
	// streamFlushes backs the streaming flush counter (ADR-047
	// PR-B). One inc per statusRecorder.doFlush on the streaming
	// path. Same (app, plan) labels as responseBytes so the
	// §12 dashboard can ratio them. See ObserveStreamFlush
	// below.
	streamFlushes *prometheus.CounterVec
	// streamActive backs the ADR-047 PR-D concurrent-stream
	// gauge. Inc'd in setupStreamingWriter when the streaming
	// path is chosen; Dec'd in the handler's defer after the
	// final flush completes. Pre-instantiated under the
	// `__other__` placeholder for every closed plan so the §12
	// panel surfaces from boot ("0 active streams" rather than
	// "no data" for a quiet daemon). See ObserveStreamStart /
	// ObserveStreamEnd below.
	streamActive *prometheus.GaugeVec
	// geoipDBAgeSeconds: ADR-091 D21. Gauge labelled by the
	// geoip.Source (dbip | maxmind). The reader's pkg/geoip
	// BootAt() is read at scrape time; the gauge is the
	// operator's "is the DB stale?" tripwire. A scrape value
	// > 30 days = the auto-refresh is failing AND the operator
	// hasn't replaced the file manually; the dashboard panel
	// flags red.
	geoipDBAgeSeconds *prometheus.GaugeVec
	// accountRateLimited backs the per-account throttling introduced by
	// ADR-040 (issue #292). Labels: account_id, plan. Pre-instantiates
	// the four plan rows under the `__other__` placeholder so the §12
	// dashboard panel never shows "no data" before the first 429.
	// Real account_id rows are bounded by accountLabels (see
	// account_label_set.go) — overflow collapses to `__other__` so the
	// §12 panel stays bounded past customer-count growth (issue #278
	// precedent in pkg/wire/metrics.go).
	accountRateLimited *prometheus.CounterVec
	// accountLabels bounds the dynamic `account_id` label set on
	// accountRateLimited (and any future account-labelled metric in
	// this package). Mirrors pkg/wire/metrics.go's accountLabelSet
	// (issue #278); the duplicate type is deliberate — pkg/gateway
	// has no dependency on pkg/wire today, and importing pkg/wire
	// solely for an unexported primitive would couple this package
	// to the cross-daemon metrics graph. See account_label_set.go
	// for the behavioural contract and the rationale for non-eviction.
	accountLabels *accountLabelSet
	// hostnameLabels bounds the dynamic `hostname` label set on
	// tlsCertExpiryByHost. Same non-evicting, map+mutex shape as
	// accountLabels; see hostname_label_set.go for the contract and
	// the rationale for non-eviction. The cap (hostnameLabelSetCap)
	// is intentionally documented as a daemon-lifetime ceiling.
	hostnameLabels *hostnameLabelSet
	// hostKinds remembers the (hostname, kind) tuple the refresher
	// wrote via ObserveHostCertExpiry so the stale-delete path in
	// nanStaleAdmittedHostCertExpiry can target the exact tuple
	// (DeleteLabelValues is tuple-keyed). hostnameLabelSet is
	// hostname-keyed only; the kind is set at write time and isn't
	// recoverable from the set later. Stored as a plain string so
	// metrics.go doesn't import CertKind from cert_expiry.go.
	//
	// Concurrency: single-writer by design. ONLY refreshCertExpiryOnce
	// (and its callees ObserveHostCertExpiry / DeleteHostCertExpiry)
	// touch this map, and it runs as a single-goroutine ticker
	// (cmd/gatewayd-internal/main.go). The /metrics scrape path doesn't read it
	// — Gather walks the *Vec by label, not by hostKinds. If a future
	// change introduces a second writer, this map must grow its own
	// mutex.
	hostKinds map[string]string
	// requestDuration backs issue #273 / ADR-042: per-app full
	// request-received → handler-return duration. Labels: app, class
	// (2xx/3xx/4xx/5xx — derived by statusClassBucket, NOT the full
	// status code, to keep cardinality bounded; see ADR-042 for the
	// math). The per-app rows are pre-instantiated by PreInstantiateApp
	// at the first Backend.Lookup hit so dashboards surface from the
	// first request rather than after the first observation. The
	// 11-bucket spread is deliberately different from wakeLatency's
	// SLO-clustered buckets (0.35/0.8s): this histogram must resolve
	// sub-100ms warm responses AND multi-second slow ones.
	requestDuration *prometheus.HistogramVec
	// requestsByRoute backs the per-route observability counter
	// (ADR-093 / issue #273 — opt-in follow-up to ADR-042 §1).
	// Labels: app, plan, route, code. The `route` label admits
	// through the bounded routeLabelSet per app (cap = 50 + the
	// reserved __route_other__ overflow bucket); the cap is
	// app.RouteMetricsEnabled AND the operator kill-switch
	// (cmd/gatewayd-internal/config.go [route_metrics] enabled).
	// When the cap is disabled, all admission collapses to the
	// reserved empty-string "no appID" sentinel so the column
	// never appears on /metrics — same shape as the existing
	// `gateway_requests_total{app="-"}` series. The
	// pre-instantiation loop in NewMetrics below must come AFTER
	// the operator kill-switch is read — the daemon reads the
	// config before NewMetrics returns, so the loop only emits
	// the closed (plan) set under the empty-row placeholder when
	// the operator has the feature disabled.
	requestsByRoute *prometheus.CounterVec
	// durationByRoute backs the per-route histogram
	// (ADR-093 D4). Labels: app, route, class. Same bucket
	// choice as the per-app requestDuration above (warm-friendly
	// 0.005s..10s spread). The per-app per-route closed `class`
	// set is pre-instantiated by PreInstantiateAppRoute after
	// the route is admitted through routeLabelSet.
	durationByRoute *prometheus.HistogramVec
	// failuresByRoute backs the per-route failures counter
	// (ADR-093 D4). Labels: app, plan, route, code. Mirrors
	// the existing gateway_requests_total shape but adds
	// `route`; the counter is incremented only when status
	// ≥ 400 so the dashboard can compute error_rate_pct directly
	// from the ratio without a separate error-rate histogram.
	// Same operator kill-switch + customer-opt-in gate as
	// requestsByRoute.
	failuresByRoute *prometheus.CounterVec
	// coldBoot backs the renamed gateway_cold_boot_total counter
	// (issue #273 / ADR-042). Renamed outright from coldWake —
	// gateway_cold_wake_total had zero external consumers (no
	// dashboard panel, alert rule, runbook, ADR, or spec mention),
	// so a dual-emit migration would have doubled the cold-boot
	// rate for no benefit.
	coldBoot *prometheus.CounterVec
	// topTenantRPS is the gateway-side mirror of apid's
	// apid_top_tenant_rps gauge (issue #300). It shares the
	// label name `account_id` with apid so a single Grafana
	// panel can join both surfaces — but the label VALUE at
	// the gateway is the resolved app_id, not an authenticated
	// principal. The public edge (gatewayd-public) is pre-auth (TLS + hostname routing
	// only); the only tenant-attributable key on the request
	// path is the app_id (the apps table's owner is in apid's
	// domain). Operators reading the panel should treat
	// gateway_top_tenant_rps as "noisy apps seen at the edge"
	// and apid_top_tenant_rps as "noisy customers on the API".
	//
	// The 5s sample cadence + 24h rolling reset are owned by
	// the same sampler pattern as apid's (cmd/gatewayd-internal/main.go
	// runs a parallel topNSampler); see pkg/wire/topn.go for
	// the cardinality contract.
	topTenantRPS *prometheus.GaugeVec
	// ADR-024 H3 (closed in PR #345): TLS observability closures. The
	// counter is incremented from pkg/gateway/tls_wire.go's
	// allowlistToDecisionFunc on a denied mint (today only the
	// allowlist branch is wired; the dns01 + token branches
	// pre-instantiate at 0 and gain their wire-incrementation in the
	// ADR-024 H3.b follow-up). The gauge is refreshed every 5 min by
	// StartCertExpiryRefresher (see pkg/gateway/cert_expiry.go) — it
	// reports the smallest remaining lifetime across cached certs on
	// disk so the §12 panels can surface both "expires soon" (warn at
	// 30 d) and "about to expire" (page at 14 d) without a per-host
	// fan-out. A negative value means a cert on disk is already past
	// its NotAfter; the page rule fires regardless.
	tlsCertExpiry prometheus.Gauge
	// tlsCertExpiryByHost is the per-host mirror of tlsCertExpiry
	// (ADR-024 H3 follow-up, Finding 2). Labels: hostname, kind.
	// Same remaining-lifetime semantics as the aggregate (negative
	// when expired; NaN when the host is no longer observed, so
	// Prometheus drops the series and the alert's < expression
	// returns false for the absent series). bounded by
	// hostnameLabels; overflow collapses to hostname="__other__".
	// `kind` is "wildcard" or "ondemand" (or "unknown" — see
	// cert_expiry.go for the classification rules). The aggregate
	// tlsCertExpiry stays as a daemon-level compatibility metric so
	// existing alert rules (FaasTLSCertExpiryPage / Warn) keep
	// firing unchanged; new per-host rules query tlsCertExpiryByHost.
	tlsCertExpiryByHost *prometheus.GaugeVec
	// tlsCertExpiryRefresherWalkComplete is a counter the refresher
	// ticks once per tick with a {result} label ∈ {complete,
	// partial, empty}. Operators page on `increase(...{result="partial"}[1h]) > 0`
	// to catch a refresher silently failing (e.g. a filesystem blip
	// where the root is unreachable). Empty ticks are also counted
	// so a fresh daemon or a post-cutover box doesn't page on
	// "no certs" (the rule's `result="partial"` filter excludes them).
	tlsCertExpiryRefresherWalkComplete *prometheus.CounterVec
	tlsOnDemandDenied                  *prometheus.CounterVec
	// tenantSurfaceCert (ADR-100 / issue #879) counts every cert
	// remint driven by a tenant_surface_changed pg_notify.
	// Labels: result ∈ {issued, failed, skipped} and kind ∈
	// {per_host_san, shared_wildcard}. Pre-instantiated at boot
	// so the §12 panel surfaces from first scrape; an idle
	// daemon shows zeros, a quiet one shows counts climbing
	// only on real customer mutations. Bounded label sets
	// (no per-surface cardinality) keep the time-series
	// footprint flat.
	tenantSurfaceCert *prometheus.CounterVec
	// wakeLocality is the increment-only wake-outcome classifier that
	// backs the multiplex scale-out decision (PR scale-out readiness,
	// ADRs 025/028). Outcome ∈ {local_snapshot, local_coldboot} today;
	// when a second compute node joins, remote_* outcomes slot in
	// transparently without a metric rename. Nil-safe via the
	// ObserveWakeLocality wrapper so the Handler hot path doesn't need
	// to gate on every call.
	wakeLocality *prometheus.CounterVec
	// wakeSnapshotTier (issue #470 / PR #470-FU-A and B) is the
	// per-wake-counter that backs the warm-tier dashboard panel.
	// Tier ∈ {warm, init, cold}; counted on every wake
	// completion. The dashboard panel "p50 wake latency by
	// snapshot tier" joins this counter with the wake-latency
	// histogram (the histogram stays unlabeled by tier to keep
	// cardinality bounded; the counter is the join key).
	wakeSnapshotTier *prometheus.CounterVec
	// wsUpgradeTotal (issue #676 / ADR-080 follow-up, PR-B) counts
	// every Connection: Upgrade + Upgrade: <token> request that
	// crosses the cmd/gatewayd-internal three-input gate
	// (pkg/gateway/handler.go:2899). Labels {plan, outcome}:
	//   plan     ∈ api.Plans (Free/Hobby/Pro/Scale)
	//   outcome  ∈ {accepted, plan_denied, bridge_disabled}
	// "plan_denied" surfaces from both the request-time 501
	// (writeWebSocketNotAllowed) and the PATCH-time 403
	// (cmd/apid/handlers_ext.go:261-268). "bridge_disabled" is
	// the FAAS_GATEWAY_RAW_STREAM_ENABLED=false / h.rawByNode==nil
	// branch (PR-A follow-up). The closed label cross-product is
	// pre-instantiated at boot so dashboards render with zero
	// rows from idle fleet, non-zero as soon as production WS
	// traffic arrives.
	wsUpgradeTotal *prometheus.CounterVec
	// wsActiveSessions (issue #676 / ADR-080 follow-up, PR-B) is
	// the in-flight raw-bytes Upgrade session gauge, labelled by
	// plan. Inc/Dec happens via IncWSSessionStart /
	// DecWSSessionEnd inside rawStreamOnceWithEvents; the defer
	// pair keeps Inc/Dec symmetric across every return branch
	// (init_failed / upstream_unavailable / client_disconnect /
	// accepted).
	wsActiveSessions *prometheus.GaugeVec
	// wsSessionDuration (issue #676 / ADR-080 follow-up, PR-B) is
	// the histogram of wall-clock seconds per session. Buckets
	// span 50 ms (one TCP round-trip) to 24 h (the
	// rawStreamSessionDeadline ceiling at
	// pkg/gateway/forwardproxy.go:464); intermediate buckets
	// align with the plan wake_idle_timeout matrix so a panel
	// can read "how long past the per-plan idle timeout did
	// this session hold" without a custom
	// histogram_quantile interpolation. Labels {plan, outcome}:
	//   outcome ∈ {accepted, init_failed, upstream_unavailable,
	//              client_disconnect} (no plan_denied /
	//              bridge_disabled — those never open a
	//              session)
	wsSessionDuration *prometheus.HistogramVec
	// wsSessionBytes (issue #676 / ADR-080 follow-up, PR-B) is
	// the counter of bytes that traverse the raw-bytes bridge,
	// labelled by {plan, direction}. direction ∈ {tx, rx}:
	//   tx — bytes flowing customer → guest (request body + raw
	//        HTTP request line + headers that the bridge carries
	//        verbatim); incremented in the body-copy goroutine
	//        at pkg/gateway/forwardproxy.go:~558
	//   rx — bytes flowing guest → customer (response body + raw
	//        HTTP status line + headers); incremented in the
	//        receiver loop at pkg/gateway/forwardproxy.go:~651
	// Raw-stream egress bytes ALSO flow through the
	// per-instance egress ring via egressSink.RecordResponseBytes
	// (PR-C follow-up) so usage_minutes.tx_bytes reflects WS
	// workloads without a separate meterd surface.
	wsSessionBytes *prometheus.CounterVec
	// computeNodeChangedSubscriberAlive is the per-process liveness
	// gauge for the LISTEN compute_node_changed subscriber loop
	// (cmd/gatewayd-internal/nodecache.go:102-141). PR scale-out readiness:
	// bumped every `subscriberHeartbeatInterval` (30s) while the
	// subscriber is alive. On ctx cancel, channel close, or the
	// initial subscribe failure, the heartbeat goroutine stops and
	// the gauge freezes at its last value — operators see "I'm stale"
	// without a separate series per channel. Unlabelled: cardinality
	// is per-process, not per node / channel / daemon. Nil-safe via
	// TouchComputeNodeChangedSubscriber so cmd/gatewayd-internal's wiring can
	// pass nil *Metrics in tests without a guarded call site.
	computeNodeChangedSubscriberAlive prometheus.Gauge
}

// Wake-snapshot-tier counter label set. The engine drives these
// values (pkg/sched/engine.go::Wake writes warm / init / cold per
// the resolved snapshot row); the gateway forwards them to the
// dashboard panel "wake latency by snapshot-tier" (PR #470-FU-C
// Grafana dashboard). Stable strings — renames here are a metric
// break (deletion of series).
const (
	tierWarm = "warm"
	tierInit = "init"
	tierCold = "cold"
)

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry:       reg,
		accountLabels:  newAccountLabelSet(),
		hostnameLabels: newHostnameLabelSet(),
		hostKinds:      make(map[string]string),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total gateway requests, labelled by app, plan, and HTTP status class.",
		}, []string{"app", "plan", "code"}),
		// ADR-089 PR 3 — kind=route substitution outcomes.
		// Pre-instantiated below so the §12 panel surfaces from
		// first scrape; PR 4-7 add (kind=rewrite, ...), (kind=jwt, ...).
		edgeRuleMatch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_edge_rule_match_total",
			Help: "Edge-rule matcher outcomes, labelled by kind and outcome (match|miss|blocked). ADR-089 PR 3.",
		}, []string{"kind", "outcome"}),
		// ADR-091 hardening PR-A — apply-path counter (distinct from
		// match). A rule can match the matcher but fail at apply time
		// (e.g. JWKS lookup returns ErrJWKSNotRegistered, or an IP
		// rule's CIDR list is empty after redaction). The {kind, result}
		// partition powers the §12 dashboard chip "edge rule apply rate".
		edgeRuleApply: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_edge_rule_apply_total",
			Help: "Edge-rule apply-path outcomes (success|error), labelled by kind. ADR-091 hardening PR-A.",
		}, []string{"kind", "result"}),
		// Issue #975 #3 / Mega-Foundation #979-a — kind=validate body
		// mismatches, labelled by {mode, reason}. The schema-side
		// counter is the (app, rule_id) tuple from rule load. Tagged
		// closed sets, so cardinality is bounded by mode × reason =
		// 4 × 6 = 24 (mode includes `other` as the coerce-on-unknown
		// bucket; reason is closed at 6).
		edgeRuleValidateFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_edge_rule_validate_failures_total",
			Help: "Edge-rule kind=validate body mismatches, labelled by validate_mode (observe|warn|block|other) and reason (required_missing|type_mismatch|additional_properties_not_allowed|enum_violation|format_violation|other). The counter increments in every mode; the reject decision is independent. `other` is the coerce bucket for unknown inputs. Issue #975 #3 / Mega-Foundation #979-a. DEPRECATED (ADR-128 §5): use gateway_validate_failures_total instead.",
		}, []string{"mode", "reason"}),
		// validateFailures (ADR-128 §5 / issue #975 #3) — the
		// spec-named replacement for edgeRuleValidateFailures
		// above. Same {mode, reason} closed vocab; the new
		// labels are {app_id, rule_id} so operators can localize
		// failures to a specific rule on a specific app. The
		// rule_id axis is bounded by ruleLabelSet
		// (pkg/gateway/rule_label_set.go) — 256 distinct rule IDs
		// per app, overflow collapses to "__other__".
		validateFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_validate_failures_total",
			Help: "Edge-rule kind=validate body mismatches, labelled by app_id (unbounded but addressable via PromQL `app_id=...`), rule_id (bounded per-app by ruleLabelSet; overflow → \"__other__\"), mode (observe|warn|block|other) and reason (required_missing|type_mismatch|additional_properties_not_allowed|enum_violation|format_violation|other). The counter increments in every mode — the reject decision is independent of the count. `other` is the coerce-on-unknown bucket. Replaces gateway_edge_rule_validate_failures_total per ADR-128 §5. Issue #975 #3 / Mega-Foundation #979-a.",
		}, []string{"app_id", "rule_id", "mode", "reason"}),
		// ruleLabelSet admission (ADR-128 §D3) — see
		// pkg/gateway/rule_label_set.go for the contract.
		ruleLabels: newRuleLabelSet(),
		// ADR-104 (issue #881 Phase 3) — per-consumer throttle
		// decisions, distinct from the per-rule edgeRuleApply path.
		// `kind` ∈ {none, api_key, jwt_subject, jwt_claim} tracks the
		// KeyBy dimension; `outcome` ∈ {admit, throttle, anonymous}
		// tracks the per-consumer admit/deny split. The anonymous
		// outcome covers anonymous traffic on a per-consumer rule —
		// the limiter passes through to the per-rule bucket, but the
		// dashboard surfaces this so an operator can see when an
		// authn-gated app is seeing unauthenticated traffic on a
		// per-consumer rule (which is a misconfiguration).
		routeConsumerThrottleDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "route_consumer_throttle_decisions_total",
			Help: "Per-consumer throttle decisions, labelled by KeyBy kind (none|api_key|jwt_subject|jwt_claim) and outcome (admit|throttle|anonymous). ADR-104, issue #881 Phase 3.",
		}, []string{"kind", "outcome"}),
		// ADR-122 §Decision: kind=cache outcome counter.
		responseCache: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_response_cache_total",
			Help: "Edge response-cache outcomes, labelled by outcome (hit|miss|bypass_authed|bypass_uncacheable|stale_if_error_served|store_skipped). hit_rate = hit / (hit + miss). bypass_* outcomes are NOT counted in hit_rate. ADR-122.",
		}, []string{"outcome"}),
		responseCacheWakesAvoided: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_response_cache_wakes_avoided_total",
			Help: "Cache hits that genuinely displaced a cold boot (HealthyCount == 0 at hit time). Per-app; saved-cost surface. ADR-122.",
		}, []string{"app"}),
		responseCacheBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_response_cache_bytes",
			Help: "In-process kind=cache store occupancy in bytes. ADR-122.",
		}),
		responseCacheEntries: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_response_cache_entries",
			Help: "In-process kind=cache store entry count. ADR-122.",
		}),
		// ADR-091 hardening PR-A — compile-time errors caught by
		// cmd/gatewayd-internal/edge_rules.go::warnPathGlobErrs. A
		// non-zero value here means a rule shipped broken and was
		// silently dropped by the loader; the dashboard chip surfaces
		// it even when the loader's WARN log is drowned in noise.
		edgeRuleCompileError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_edge_rule_compile_error_total",
			Help: "Edge-rule compile-time errors caught by the cmd-side loader, labelled by kind. ADR-091 hardening PR-A.",
		}, []string{"kind"}),
		// Issue #995 Phase 4 / ADR-121 — envelope-cap warn
		// counter for the response body (used by both the buffered
		// and streaming capWriter paths).
		responseBodyWarnTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_response_body_warn_total",
			Help: "Count of response bodies that approached or exceeded the per-plan MaxResponseBodyBytes cap, labelled by app_id and bucket (near_threshold|exceeded).",
		}, []string{"app_id", "bucket"}),
		// ADR-119 — apps.public_auth_mode='internal_only' ingress
		// verification outcomes. Closed outcome set; the pre-
		// instantiation below surfaces every tuple from first
		// scrape so the §12 dashboard chip "internal auth match
		// rate" starts at zero (not absent). Bounded to 3 series,
		// well below the 50-counter budget.
		internalAuthMatch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_internal_auth_match_total",
			Help: "apps.public_auth_mode='internal_only' ingress verification outcomes (matched|blocked), labelled by outcome. ADR-119.",
		}, []string{"outcome"}),
		// ADR-091 amendment — coarse-gate apps.maintenance_mode
		// short-circuit counter. Distinct from edgeRule* family
		// because the coarse gate is per-app, not per-rule. Plan
		// label is the closed Free|Hobby|Pro|Scale set, pre-
		// instantiated at boot below.
		appMaintenance: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_app_maintenance_total",
			Help: "Apps.maintenance_mode coarse-gate short-circuits, labelled by plan. ADR-091 amendment / §4.1.2.0.",
		}, []string{"plan"}),
		// ADR-093 / issue #273 follow-up: per-route observability
		// counters. The `route` label is admitted through the
		// per-app routeLabelSet (cap = 50 + __route_other__
		// overflow). The constructor does NOT pre-instantiate
		// every (app, route, code) combo because the route label
		// is unbounded — the routeLabelSet is the cap. The
		// closed (plan, code) set is pre-instantiated below
		// under the empty placeholder so the §12 dashboard panel
		// for opt-in apps surfaces from the first scrape.
		//
		// The metric names carry the `_by_route` suffix to keep
		// them disjoint from the pre-existing {app, plan, code}
		// `gateway_requests_total` series — Prometheus rejects two
		// CounterVecs with the same name but different label sets
		// at registration time. Dashboards consuming
		// `gateway_requests_by_route_total` etc. read the opt-in
		// apps' per-route rows; the existing fleet-level
		// `gateway_requests_total{app, plan, code}` is unchanged.
		requestsByRoute: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_by_route_total",
			Help: "Per-route counter (ADR-093). Adds {route} as a 4th label when the app has apps.route_metrics_enabled=true; the per-app ADR-042 view is the same series with route omitted. route is method + raw path (pre-rewrite); admit() collapses wildcard-path overflow into the reserved __route_other__ bucket (cap = 50 per app).",
		}, []string{"app", "plan", "route", "code"}),
		durationByRoute: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "gateway_request_duration_by_route_seconds",
			Help: "Per-route duration histogram (ADR-093). Adds {route} as a 3rd label when the app has apps.route_metrics_enabled=true; the per-app ADR-042 view is the same histogram with route omitted. The closed `class` set is pre-instantiated per app per admitted route via PreInstantiateAppRoute.",
			Buckets: []float64{
				0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10,
			},
		}, []string{"app", "route", "class"}),
		failuresByRoute: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_request_failures_by_route_total",
			Help: "Per-route failures counter (ADR-093). Increments on status ≥ 400; the dashboard computes error_rate_pct directly from the ratio to gateway_requests_total. Same route admission as gateway_requests_by_route_total above.",
		}, []string{"app", "plan", "route", "code"}),
		geoipDBAgeSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "geoip_db_age_seconds",
			Help: "Seconds since the geoip DB was last (re)loaded. ADR-091 D21; the operator's 'is the DB stale?' tripwire.",
		}, []string{"source"}),
		wakeLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "gateway_wake_latency_seconds",
			Help: "End-to-end latency from request received to first upstream byte after a cold wake.",
			// Buckets target the §12 SLO: p50 ≤ 0.35 s, p95 ≤ 0.8 s, page > 1.5 s.
			Buckets: []float64{
				0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0,
			},
		}),
		// PR #4 / ADR-092 §3.5. Same bucket layout as wakeLatency
		// — same distribution, same SLO targets — just labelled
		// by node_id so the per-node p95/p99 surfaces. Do NOT
		// change the existing unlabeled histogram's buckets
		// without re-running the §12 fleet p95 calibration; the
		// new histogram is opt-in and not part of any SLO.
		wakeLatencyByNode: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "gateway_wake_latency_seconds_by_node",
			Help: "Per-node wake latency (operator-side only; the unlabeled gateway_wake_latency_seconds remains the §12 fleet p95 source). ADR-092 §3.5. node_id is the compute_nodes.id UUID; the obs handler resolves id to name via ListComputeNodes.",
			Buckets: []float64{
				0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0,
			},
		}, []string{"node_id"}),
		// Spec §12 row "wake queue wait p95". Observed by WakeGate.Wait
		// on every caller that joins a single-flight coalescing wake. The
		// leader (the request that actually triggers the wake) reads near
		// zero; followers (peer requests parked while the leader's restore
		// runs) read close to the restore latency.
		//
		// Buckets skew toward the wake-completion window (50ms..2s) so
		// the histogram exposes the p50/p95 cleanly; the long tail
		// (5s, 10s) catches pathological stalls where the gate's 30s TTL
		// is approaching.
		wakeQueueWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "gateway_wake_queue_wait_seconds",
			Help: "Time spent in the per-app wake queue (single-flight coalescing) before the request was released to upstream. Spec §12 row 'wake queue wait p95'.",
			Buckets: []float64{
				0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0,
			},
		}),
		// ADR-098 C11: phase-decomposed wake telemetry. Sibling of
		// wakeLatency (gateway_wake_latency_seconds). The aggregate
		// histogram stays byte-identical to pre-C11 buckets — that
		// series is the §12 SLO source-of-truth (p50 ≤ 0.35 s,
		// p95 ≤ 0.8 s). This vector adds the recovery dimension
		// when a regression fires: phase ∈ {"queue_wait",
		// "coordinator_wait", "schedd_admit", "vmmd_wake",
		// "guest_ready", "cold_fallback_reason"}. Phases are
		// labelled by the emit site, not the boundary, so a stalled
		// coordinator shows up as coordinator_wait tail, not as a
		// generic wake latency regression.
		//
		// Pre-instantiated below with the closed phase set so the
		// §12 panel surfaces zero on an idle gateway.
		wakePhaseDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "gateway_wake_phase_duration_seconds",
			Help: "Phase-decomposed wake latency (ADR-098 C11). Each phase is observed once per request at the boundary it measures. The aggregate gateway_wake_latency_seconds is unchanged.",
			// Same bucket envelope as wakeLatency so the dashboards
			// can compare phase histograms to the aggregate without
			// re-bucketing.
			Buckets: []float64{
				0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0,
			},
		}, []string{"phase"}),
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_queue_depth",
			Help: "Current number of waiters per app's wake queue (sampled).",
		}, []string{"app"}),
		// ADR-098 C7: closed-set reasons pre-instantiated so the
		// §12 dashboard chip "leader bootstrap aborts" surfaces
		// zero rows from boot. Adding a new reason is a code +
		// dashboard change.
		leaderBootstrapAborts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_leader_bootstrap_aborts_total",
			Help: "Detached leader goroutine aborts on the bootstrap cap, labelled by reason (queue_empty_no_instance|ttl_expired|app_deleted). ADR-098 C7.",
		}, []string{"reason"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_rate_limited_total",
			Help: "Requests rejected by the per-app rate limiter.",
		}, []string{"app", "plan"}),
		// ADR-046 PR-2 producer observability. Counter is
		// registered on the gatewayd-internal-local registry (this
		// daemon scrapes /metrics via the control listener).
		// The cross-daemon pkg/wire.OpsMetrics mirror was
		// removed in the PR-2 review pass — there was no
		// production caller, and dual registries with no
		// cross-daemon consumer is dead counter surface.
		responseBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_response_bytes_total",
			Help: "Per-(app, plan) HTTP response body bytes observed by the gateway (ADR-046, ADR-047). Sum of per-flush deltas on the streaming path plus the residual capture; single observation on the buffered path. Total equals the canonical persisted usage_minutes.tx_bytes for the same request. Real-time operator view for §12 anomaly detection.",
		}, []string{"app", "plan"}),
		// PR-B streaming flush counter (ADR-047). One inc per
		// statusRecorder.doFlush call on the streaming path —
		// covers both the periodic (256 KiB / 200 ms) triggers and
		// the residual capture. Multiply by average delta in a
		// scrape window to estimate bytes/flush on the §12
		// dashboard; ratio of stream_flushes_total to requests_total
		// across streaming apps approximates the avg flush count per
		// response. Bounded to (app, plan) like the rest of the
		// gateway counters; the cardinality lives on the streaming
		// app set which is already plan-bounded via pgRouter.
		streamFlushes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_stream_flushes_total",
			Help: "Per-(app, plan) streaming response flushes (ADR-047 PR-B). One increment per statusRecorder.doFlush call on the streaming path: periodic 256 KiB / 200 ms triggers plus the residual capture. Real-time view of streaming activity; ratio to gateway_requests_total for streaming apps estimates avg flush count per response.",
		}, []string{"app", "plan"}),
		// ADR-047 PR-D concurrent-stream gauge. Inc'd in
		// setupStreamingWriter when the streaming path is
		// chosen; Dec'd in the handler's defer after the final
		// flush. The buffered path never touches this gauge
		// (it durates the response into a single Go-time
		// transfer). Pre-instantiated below under the
		// `__other__` placeholder for every closed plan so the
		// §12 dashboard panel surfaces zero-valued series from
		// boot rather than "no data" for a quiet daemon.
		streamActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_stream_active",
			Help: "Per-(app, plan) concurrent in-flight streaming responses (ADR-047 PR-D). Inc'd when setupStreamingWriter installs the streaming path; Dec'd in the handler's defer after the final flush. Buffered-path requests are NOT counted. Real-time operator view of streaming concurrency; pairs with gateway_stream_flushes_total to estimate avg flush per response.",
		}, []string{"app", "plan"}),
		// ADR-040 / issue #292. account_id label has cardinality O(active
		// accounts × 4 plans). Bounded admission lives in
		// accountLabels (account_label_set.go) — real ids over the
		// accountLabelSetCap collapse to "__other__" before they
		// reach this counter so the §12 panel stays bounded past
		// customer-count growth. Pre-instantiation only touches the
		// closed (plan) set under the "__other__" placeholder so the
		// §12 panel surfaces from boot. Real account_id rows appear
		// on first 429 (or via admit() pre-instantiation if a future
		// change wants the closed loop tightened).
		accountRateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_per_account_rate_limited_total",
			Help: "Requests rejected by the per-account rate limiter (ADR-040 / issue #292). Labelled by account_id, plan. account_id=\"__other__\" is the bounded admission overflow placeholder for BOTH the closed (plan) pre-instantiation AND the accountLabelSet overflow (capacity = accountLabelSetCap = 10k). Past the cap the admit() helper collapses every distinct account_id to \"__other__\" so a flood of distinct accounts does not mint unbounded series.",
		}, []string{"account_id", "plan"}),
		// Issue #273 / ADR-042. Per-app full request duration.
		// Buckets resolve both warm (≤100ms) and slow (≥1s) traffic; the
		// ~50ms mark separates the two regimes for an at-a-glance
		// p50/p95 reading.
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "gateway_request_duration_seconds",
			Help: "Per-app full request duration (request received to handler return), labelled by HTTP status class (2xx/3xx/4xx/5xx). Issue #273 / ADR-042.",
			Buckets: []float64{
				0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10,
			},
		}, []string{"app", "class"}),
		// Issue #273 / ADR-042. Renamed outright from gateway_cold_wake_total.
		coldBoot: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_cold_boot_total",
			Help: "Requests that triggered a cold boot for an app. Issue #273 / ADR-042 (renamed from gateway_cold_wake_total).",
		}, []string{"app"}),
		topTenantRPS: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_top_tenant_rps",
			Help: "Top-N 5s request rate per tenant observed at the edge (issue #300). Label key is account_id for parity with apid_top_tenant_rps; the label VALUE at the gateway is the resolved app_id (gatewayd-internal is pre-auth and only sees hostname→app routing). Cardinality bounded at topAccountSetCap (1000) + 1 \"other\" overflow by pkg/wire/topn.go via the cmd/gatewayd-internal/topn.go sampler. The overflow bucket literally named \"other\" matches apid's gauge.",
		}, []string{"account_id"}),
		// ADR-024 H3 (closed in PR #345). Gauge starts unset (NaN at
		// scrape time — Prometheus drops NaN series, so an idle daemon
		// emits no series at all); the page rule's `<` then returns
		// false and the alert stays silent until a real cert has been
		// minted. SetTLSCertExpiry may emit a negative value when a
		// cert on disk is past its NotAfter — that's intentional, the
		// page rule fires regardless.
		tlsCertExpiry: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_tls_cert_expiry_seconds",
			Help: "Smallest remaining lifetime across cached certs on disk (cfg.StorageDir). ADR-024 H3 (closed). Page at ≤14 d; warn at ≤30 d. Gauge is unset (no series) before the first cert is minted; the `<` alert expression handles a missing series correctly. A negative value means a cert on disk is already past its NotAfter — the page rule fires regardless.",
		}),
		// ADR-024 H3 follow-up (Finding 2): per-host visibility. Same
		// gauge family as tlsCertExpiry but with hostname + kind
		// labels. Negative = expired. NaN = the host is no longer
		// observed (certmagic removed it; refresher NaN's the gauge
		// so Prometheus drops the series and the alert's < expression
		// returns false for the absent series). hostname="__other__"
		// is the bounded admission overflow for hostname (capacity
		// hostnameLabelSetCap = 10k); kind ∈ {wildcard, ondemand,
		// unknown} — see cert_expiry.go's classifyByIssuerKey for the
		// source-of-truth. kind="unknown" means the issuer key didn't
		// match either the wildcard or the on-demand bucket and is
		// excluded from the per-host page rules so operators don't
		// chase a misclassification.
		tlsCertExpiryByHost: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_tls_cert_expiry_by_host_seconds",
			Help: "Per-host remaining cert lifetime (cfg.StorageDir). ADR-024 H3 follow-up (Finding 2). hostname=\"__other__\" is the bounded admission overflow for the hostname label (capacity hostnameLabelSetCap = 10k); kind ∈ {wildcard, ondemand, unknown}. Negative = expired; NaN = host no longer observed (Prometheus drops the series). Page rules query `hostname!=\"__other__\",kind!=\"unknown\"` so misclassification and overflow do not page.",
		}, []string{"hostname", "kind"}),
		// Walk completeness signal so an operator can page on "the
		// refresher silently failed" rather than waiting for the
		// gauge to drift. result ∈ {complete, partial, empty}.
		// `complete` = full walk succeeded with ≥1 cert;
		// `empty` = full walk succeeded with 0 certs (boot or
		// post-cutover state); `partial` = walk returned an error
		// after partial success OR the root was missing/inaccessible.
		// The page rule increments on result="partial".
		tlsCertExpiryRefresherWalkComplete: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_tls_cert_expiry_refresher_walk_complete_total",
			Help: "Cert-expiry refresher walk-completeness classifier (Finding 2). result ∈ {complete, partial, empty}. Increment once per tick. Page rule fires on increase of result=\"partial\" so a silently-failing refresher pages the operator before certs actually expire.",
		}, []string{"result"}),
		// ADR-024 H3 (closed in PR #345). The reason label set is closed
		// and pre-instantiated below so every reason series surfaces in
		// /metrics from boot. Only `allowlist` is wired today (from the
		// on-demand DecisionFunc); dns01 + token gain their wire-
		// incrementation in the still-open H3.b follow-up. The frozen-
		// zero is the visibility for that follow-up — operators see
		// the panel exist and a stuck-at-zero signals the follow-up
		// is unmerged.
		tlsOnDemandDenied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_tls_on_demand_denied_total",
			Help: "On-demand cert mint denials, labelled by reason. ADR-024 H3 (closed in PR #345); H3.b is the still-open follow-up. reason=allowlist is incremented from pkg/gateway/tls_wire.go's allowlistToDecisionFunc today. reason=dns01 and reason=token are reserved for the H3.b follow-up that bridges the certmagic ACME-issuer logger through this counter; the series are pre-instantiated at 0 so the dashboard panel surfaces from boot and a missing wire-incrementation is visible as a frozen zero.",
		}, []string{"reason"}),
		// ADR-100 / issue #879 — per-surface cert-remint
		// counter. result ∈ {issued, failed, skipped} (skipped
		// is the "no verified hostnames" / "soft-deleted
		// surface" / "unsupported cert_kind" path that never
		// reaches the CA). kind ∈ {per_host_san,
		// shared_wildcard} (per_host_san is the only kind the
		// v1 issuer mints; shared_wildcard counts surface so
		// the dashboard surfaces the deferred-ADR backlog).
		tenantSurfaceCert: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_tenant_surface_cert_total",
			Help: "Tenant surface cert remint outcomes, labelled by result and kind. ADR-100 / issue #879. result ∈ {issued, failed, skipped}; kind ∈ {per_host_san, shared_wildcard}.",
		}, []string{"result", "kind"}),
		// PR scale-out readiness — wake-locality counter. The closed
		// (outcome) set is pre-instantiated below so the panel surfaces
		// from boot. New outcomes (remote_*) join by widening the
		// pre-instantiation loop; the metric name stays stable.
		wakeLocality: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_wake_locality_total",
			Help: "Wake-outcome classifier used to drive the sticky-vs-shared snapshot-store decision. outcome ∈ {local_snapshot, local_coldboot} today; remote_* outcomes slot in when a second compute node joins. Counting is restricted to admissions that actually brought up an instance — warm requests and at-capacity benign outcomes are not enumerated.",
		}, []string{"outcome"}),
		// PR #470-FU-B (issue #470): the per-wake snapshot-tier
		// counter for the warm-tier dashboard panel. Tier values
		// are {warm, init, cold}; the engine sets tier on the
		// wake outcome (PR #470-FU-A) and the gateway increments
		// here on every wake completion. Bounded cardinality
		// (3 tier values) — the counter is cheap.
		wakeSnapshotTier: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_wake_snapshot_tier_total",
			Help: "Wake outcomes classified by the snapshot tier the engine selected. tier ∈ {warm, init, cold}. The warm-tier bucked rises as the framework-ready signal (PR #470-FU-B) populates the per-instance column and the engine's captureWarmSnapshot (PR #470-FU-A) issues a warm-tier PauseAndSnapshot.",
		}, []string{"tier"}),
		// PR scale-out readiness — liveness gauge for the LISTEN
		// compute_node_changed subscriber. Bumped every
		// `subscriberHeartbeatInterval` (30s) by the goroutine in
		// cmd/gatewayd-internal/nodecache.go.WatchEvictions; the gauge freezes
		// at its last value when the heartbeat goroutine ends
		// (ctx cancel / channel close / initial subscribe failure)
		// so a frozen gauge is the "subscriber died" signal. Series
		// is absent before the first tick — the alert expression
		// handles absent correctly the same way it does for
		// gateway_tls_cert_expiry_seconds (H3, closed in PR #345).
		computeNodeChangedSubscriberAlive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_compute_node_changed_subscriber_alive",
			Help: "Liveness gauge for the LISTEN compute_node_changed subscriber loop in cmd/gatewayd-internal/nodecache.go. Bumped every subscriberHeartbeatInterval (30s) while the subscriber is alive. A frozen or zero gauge means the NodeClientCache is silently out of date and the next compute_nodes UPSERT is invisible to placement. The hook is the observability point; the alert rule + window choice live in ops wiring, out of scope for this PR.",
		}),
		// Issue #676 / ADR-080 follow-up, PR-B: raw-bytes
		// Upgrade / WebSocket observability surface. Four series
		// registered against the per-Handler registry (matching
		// the rest of pkg/gateway/metrics.go — gateway_wake_*
		// / gateway_cold_boot_* / gateway_request_*, all
		// per-Handler). The dashboard panels "ws: upgrades by
		// plan (5m)", "ws: active sessions by plan",
		// "ws: session duration p50/p95/p99 by plan", and
		// "ws: session bytes (rx/tx) by plan" depend on these
		// existing from boot.
		//
		// Plan / outcome / direction closed sets are declared
		// alongside the helper methods below
		// (WSOutcome / WSDirection constants) so the constructor
		// pre-instantiate loop and the runtime helpers share one
		// source of truth.
		wsUpgradeTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_ws_upgrade_total",
				Help: "Count of Connection: Upgrade + Upgrade: <token> requests crossing the cmd/gatewayd-internal three-input gate (issue #676 / ADR-080). Labelled by {plan, outcome}: plan ∈ api.Plans (Free/Hobby/Pro/Scale), outcome ∈ {accepted, plan_denied, bridge_disabled}. 'plan_denied' is the WebSocketResponseAllowed()=false branch; 'bridge_disabled' is FAAS_GATEWAY_RAW_STREAM_ENABLED=false / h.rawByNode==nil. 12 series total.",
			},
			[]string{"plan", "outcome"},
		),
		wsActiveSessions: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gateway_ws_active_sessions",
				Help: "In-flight raw-bytes Upgrade sessions (issue #676 / ADR-080), labelled by plan. Inc/Dec via IncWSSessionStart / DecWSSessionEnd inside pkg/gateway/forwardproxy.go's rawStreamOnceWithEvents (the defer pair keeps symmetry across every return branch). 4 series total.",
			},
			[]string{"plan"},
		),
		// Buckets span 50 ms (one TCP round-trip) to 24 h (the
		// rawStreamSessionDeadline ceiling at
		// pkg/gateway/forwardproxy.go:464). Intermediate buckets
		// align with the plan wake_idle_timeout matrix (Hobby
		// 60 s, Pro 300 s, Scale 600 s).
		wsSessionDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "gateway_ws_session_duration_seconds",
				Help: "Wall-clock seconds per raw-bytes Upgrade session (issue #676 / ADR-080). Labelled by {plan, outcome}: plan ∈ api.Plans, outcome ∈ {accepted, init_failed, upstream_unavailable, client_disconnect}. Buckets span 50 ms (one TCP round-trip) to 24 h (the rawStreamSessionDeadline); intermediate buckets align with the plan wake_idle_timeout matrix.",
				Buckets: []float64{
					0.05, 0.25, 1, 5, 30, 60, 300, 600, 1800, 7200, 21600, 86400,
				},
			},
			[]string{"plan", "outcome"},
		),
		wsSessionBytes: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_ws_session_bytes_total",
				Help: "Bytes that traverse the raw-bytes bridge (issue #676 / ADR-080), labelled by {plan, direction}: plan ∈ api.Plans, direction ∈ {tx, rx}. tx = customer→guest request body+headers; rx = guest→customer response body+headers. 8 series total.",
			},
			[]string{"plan", "direction"},
		),
	}
	// Pre-instantiate the WS closed label cross-product (issue
	// #676 / ADR-080 follow-up, PR-B). Same precedent as
	// tlsOnDemandDenied / accountRateLimited above. The
	// ws_session_duration_seconds histogram excludes plan_denied
	// / bridge_disided because those never open a session — the
	// counter is sufficient for the rejected-at-gate outcomes.
	wsOutcomes := []WSOutcome{WSOutcomeAccepted, WSOutcomePlanDenied, WSOutcomeBridgeDisabled}
	wsSessionOutcomes := []WSOutcome{WSOutcomeAccepted, WSOutcomeInitFailed, WSOutcomeUpstreamUnavailable, WSOutcomeClientDisconnect}
	wsDirections := []WSDirection{WSDirectionTx, WSDirectionRx}
	wsPlans := []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale}
	for _, plan := range wsPlans {
		planStr := string(plan)
		m.wsActiveSessions.WithLabelValues(planStr)
		for _, outcome := range wsOutcomes {
			m.wsUpgradeTotal.WithLabelValues(planStr, string(outcome))
		}
		for _, outcome := range wsSessionOutcomes {
			m.wsSessionDuration.WithLabelValues(planStr, string(outcome))
		}
		for _, direction := range wsDirections {
			m.wsSessionBytes.WithLabelValues(planStr, string(direction))
		}
	}
	// Pre-instantiate the ("other",) row on gateway_top_tenant_rps so
	// the gauge surfaces from boot, mirroring the apid precedent
	// (pkg/wire/metrics.go:632). Without this, a quiet daemon would
	// emit zero series for the first 5s and a dashboard reader would
	// see "no data" until the first observation + first sampler tick.
	// (Issue #300.)
	m.topTenantRPS.WithLabelValues("other")
	// Pre-instantiate every closed (reason) label tuple on
	// gateway_tls_on_demand_denied_total so the counter's HELP/TYPE
	// and zero-valued series surface in /metrics from the moment the
	// daemon binds — same precedent as auditWriteFail / requestFailures
	// pre-instantiation above and the egress-deny / scale-decisions
	// catalog pre-instantiation in pkg/wire/metrics.go. Without this
	// loop, the `reason="dns01"` / `reason="token"` rows would only
	// appear after the first denial, hiding the "frozen zero =
	// follow-up unmerged" signal we depend on for the §12 dashboard
	// panel. NewMetrics is called exactly once per daemon
	// (cmd/gatewayd-internal/main.go:269), so each daemon gets exactly one set
	// of pre-instantiated series; if you ever construct a second
	// *Metrics, that's by design, not a bug. (ADR-024 H3, PR #345.)
	for _, reason := range []string{"allowlist", "dns01", "token"} {
		m.tlsOnDemandDenied.WithLabelValues(reason)
	}
	// ADR-040 / issue #292. Pre-instantiate the closed (plan) row set
	// under the "__other__" placeholder so the §12 dashboard panel
	// surfaces a zero-valued series from boot. Real account_id rows
	// appear on first 429 — bounded admission is the alert + runbook
	// concern, not the limiter's.
	for _, plan := range []string{"free", "hobby", "pro", "scale"} {
		m.accountRateLimited.WithLabelValues("__other__", plan)
	}
	// PR scale-out readiness. Pre-instantiate the closed (outcome)
	// set so the panel surfaces from boot. Mirrors the
	// tlsOnDemandDenied / accountRateLimited pre-instantiation
	// pattern above. To add a new outcome (e.g. remote_*):
	// extend this slice; the metric name stays stable.
	// ADR-089 PR 3 + PR 4 — pre-instantiate the closed (kind, outcome)
	// cross product for every shipped kind. PR 5-7 add kinds to
	// the outer loop (cors / jwt / ip). PR-B (kind=validate)
	// widens the outer loop with the "failed" outcome (broken
	// schema compile + 415 + 413 audit rows all land here). JWT
	// additionally has "failed" + "missing" outcomes (the
	// verifier path emits those — kind=jwt has more distinct
	// failure modes than the other kinds because every sentinel
	// error in pkg/edgejwks maps to a separate outcome for the
	// dashboard). CORS + IP keep the closed {match, miss, blocked}
	// triple. The D20.5 amendment (issue #881) widens the loop
	// with `throttle` so the §12 dashboard panel surfaces
	// per-rule throttle match/deny rates from first scrape. The
	// closed set guarantees the §12 dashboard panel "edge rule
	// match rate" surfaces every (kind, outcome) tuple from
	// first scrape.
	for _, kind := range edgeRuleKinds {
		for _, outcome := range []string{"match", "miss", "blocked", "failed"} {
			m.edgeRuleMatch.WithLabelValues(kind, outcome)
		}
	}
	// ADR-122 §Decision: pre-instantiate the closed
	// `outcome` set on the response-cache counter so the §12
	// panel surfaces every outcome from boot. Adding a new
	// outcome is a code + dashboard change (the closed set
	// is intentional — label cardinality stays bounded).
	for _, outcome := range []string{"hit", "miss", "bypass_authed", "bypass_uncacheable", "stale_if_error_served", "store_skipped"} {
		m.responseCache.WithLabelValues(outcome)
	}
	// Phase 3 (ADR-104, issue #881): pre-instantiate the closed
	// (kind, outcome) cross product for the per-consumer throttle
	// decision counter. `kind` matches the KeyBy dimension
	// (api.ThrottleKeyBy* constants); `outcome` is the per-request
	// admit/deny split. The "anonymous" outcome covers
	// unauthenticated traffic on a per-consumer rule — a
	// misconfiguration signal for the dashboard.
	for _, kind := range []string{"none", "api_key", "jwt_subject", "jwt_claim"} {
		for _, outcome := range []string{"admit", "throttle", "anonymous"} {
			m.routeConsumerThrottleDecisions.WithLabelValues(kind, outcome)
		}
	}
	for _, outcome := range []string{"match", "miss", "blocked", "failed", "missing"} {
		m.edgeRuleMatch.WithLabelValues("jwt", outcome)
	}
	for _, outcome := range []string{"local_snapshot", "local_coldboot"} {
		m.wakeLocality.WithLabelValues(outcome)
	}
	// PR #470-FU-B (issue #470): pre-instantiate the closed
	// (tier) set on the per-wake snapshot-tier counter so the
	// warm-tier dashboard panel surfaces from boot. The set is
	// intentionally small (warm / init / cold) — the engine
	// drives the actual value. To add a new tier (e.g. "cold"
	// vs "lifecycle_init"): extend this slice; the metric name
	// stays stable.
	for _, tier := range []string{tierWarm, tierInit, tierCold} {
		m.wakeSnapshotTier.WithLabelValues(tier)
	}
	// ADR-098 C7: pre-instantiate the closed (reason) set on the
	// leader-bootstrap-aborts counter so the §12 dashboard chip
	// "leader bootstrap aborts" surfaces every reason from boot.
	// Adding a new reason is a code + dashboard change.
	for _, reason := range []string{"queue_empty_no_instance", "ttl_expired", "app_deleted"} {
		m.leaderBootstrapAborts.WithLabelValues(reason)
	}
	// ADR-119 — pre-instantiate the closed (outcome) set on the
	// internal-auth-match counter so the §12 dashboard chip
	// "internal auth match rate" surfaces from first scrape.
	// Round-2 peer-review (#6): the previous shape pre-instantiated
	// a `bypass_stripped` label that no code path incremented —
	// the Header.Del on internal_proxy.go was unconditional →
	// the counter stayed at zero forever, wasting 1 of the
	// 50-counter-per-app budget (ADR-093 cap) and undermining
	// operator trust in the metric family. The label is removed.
	// The set is {matched, blocked}; if a future hardening pass
	// adds a third label, it's a code + dashboard change.
	for _, outcome := range []string{"matched", "blocked"} {
		m.internalAuthMatch.WithLabelValues(outcome)
	}
	// ADR-128 §5: pre-instantiate the closed (mode, reason) cross
	// product on the LEGACY edgeRuleValidateFailures counter
	// (24 = 4 modes × 6 reasons) for the §12 panel-at-day-1
	// contract. The canonical validateFailures counter carries
	// (app_id, rule_id, mode, reason) and cannot be
	// pre-instantiated without runtime inputs (CounterVec
	// requires all labels at WithLabelValues time). Operators
	// alerting on rate == 0 must handle the cold-start window
	// for the canonical counter; the legacy one is kept warm
	// for one release per ADR-128 §5 so existing dashboards
	// stay populated while the migration lands.
	for _, mode := range []string{"observe", "warn", "block", "other"} {
		for _, reason := range []string{
			"required_missing",
			"type_mismatch",
			"additional_properties_not_allowed",
			"enum_violation",
			"format_violation",
			"other",
		} {
			m.edgeRuleValidateFailures.WithLabelValues(mode, reason)
		}
	}
	// ADR-024 H3 follow-up (Finding 2): pre-instantiate the closed
	// (result) set on the walk-completeness counter so the §12
	// dashboard panel surfaces from boot. result="partial" is the
	// page signal — pre-instantiating it lets the rule fire on
	// increase without waiting for a first partial event.
	for _, result := range []string{"complete", "partial", "empty"} {
		m.tlsCertExpiryRefresherWalkComplete.WithLabelValues(result)
	}
	// ADR-100 / issue #879 — pre-instantiate the closed
	// (result, kind) cartesian product so the §12 panel
	// surfaces from boot. Bounded (3 results × 3 kinds = 9
	// series) so the time-series footprint is flat. An idle
	// daemon shows all nine counters at zero; a quiet one
	// climbs only on real customer mutations. kind="" is
	// stamped because the "surface deleted between notify and
	// lookup" path (cert_issuer_tenant_surface.go:180) emits
	// under an empty kind label — the row is gone, so its
	// CertKind is unrecoverable. Surfacing kind="" from boot
	// keeps the dashboard chip visible without waiting for the
	// first clean-up race; the kind={per_host_san,
	// shared_wildcard} entries match CertKind constants and
	// any future kind (ADR-114 deferred work) widens this
	// slice.
	for _, result := range []string{"issued", "failed", "skipped"} {
		for _, kind := range []string{"", "per_host_san", "shared_wildcard"} {
			m.tenantSurfaceCert.WithLabelValues(result, kind)
		}
	}
	// ADR-047 PR-D: pre-instantiate the closed (plan) set on
	// gateway_stream_active under the "__other__" placeholder so the
	// §12 dashboard panel surfaces zero-valued series from the moment
	// the daemon binds, mirroring the streamFlushes / accountRateLimited
	// pre-instantiation pattern above. Real app rows appear on the
	// first streaming request. The bound is the (app, plan) tuple set
	// (same shape as accountRateLimited); bounded admission is the
	// alert + runbook concern, not the gauge's.
	for _, plan := range []string{"free", "hobby", "pro", "scale"} {
		m.streamActive.WithLabelValues("__other__", plan)
	}
	// PR #4 doesn't touch the edge-rule label set, but the
	// ADR-091 hardening PR-A pre-instantiation must remain present
	// so the §12 dashboard chip "edge rule apply rate" + "edge rule
	// compile errors" surface every tuple from first scrape. Closed
	// set: {route, rewrite, redirect, headers, cors, jwt, ip, validate,
	// limit, maintenance, geo, throttle, ingress_ip, ingress_internal,
	// ingress_members}. Adding a new kind requires extending this
	// slice — the metric name is stable. `ingress_ip` was added by
	// ADR-118 for the per-app ingress IP allowlist
	// (pkg/gateway/handler.go::applyIngressIPAllowlist).
	// `ingress_members` was added by ADR-123 for the per-app
	// members_only org-membership gate
	// (pkg/gateway/public_auth_members_only.go::
	// applyIngressMembersOnly). Note: the internal_only gate
	// (pkg/gateway/internal_svc_auth.go::applyIngressInternalSvc)
	// uses ObserveInternalAuthMatch (its own dedicated
	// counter family) rather than the edge-rule counters —
	// that's why `ingress_internal` does NOT appear in the
	// edge-rule closed set even though ADR-119 carved it
	// out as a future-extension point. Adding it here would
	// publish zero-valued tuples that nothing emits and that
	// the §12 dashboard cannot pivot on (no Grafana panel
	// groups ingress_internal + ingress_ip together — the
	// internal-only metric family is separate). The closed
	// set stays truthful: pre-instantiate exactly what the
	// ObserveEdgeRuleMatch / ObserveEdgeRuleApply call sites
	// emit.
	for _, kind := range edgeRuleKinds {
		for _, result := range []string{"success", "error"} {
			m.edgeRuleApply.WithLabelValues(kind, result)
		}
		m.edgeRuleCompileError.WithLabelValues(kind)
	}
	// ADR-091 amendment — pre-instantiate the closed (plan) set on
	// the coarse-gate counter so the §12 dashboard panel "apps in
	// maintenance by plan" surfaces from boot. Closed set mirrors
	// streamActive / accountRateLimited pre-instantiation above.
	for _, plan := range []string{"free", "hobby", "pro", "scale"} {
		m.appMaintenance.WithLabelValues(plan)
	}
	// ADR-098 C11: pre-instantiate the closed phase set so the §12
	// panel reads zero on an idle gateway. queue_wait is the
	// legacy scalar (gateway_wake_queue_wait_seconds) folded into
	// a labelled histogram here; the scalar stays for one release
	// per the dual-write plan. coordinator_wait is the per-app
	// wake-coordinator spin-up at schedd. schedd_admit is the
	// schedd.EnsureWake RPC. vmmd_wake is the vmmd.Wake RPC
	// latency. guest_ready is the framework-ready handshake
	// stamp propagation. cold_fallback_reason is the wake that
	// fell through to cold boot — labelled to match the vmmd
	// result enum (closed set).
	for _, phase := range []string{
		"queue_wait", "coordinator_wait", "schedd_admit",
		"vmmd_wake", "guest_ready", "cold_fallback_reason",
	} {
		m.wakePhaseDuration.WithLabelValues(phase)
	}
	// ADR-091 D21 (kind=geo) + issue #676 / ADR-080 follow-up,
	// PR-B (raw-bytes Upgrade / WebSocket observability). Registered
	// alongside the rest of the gateway_* series so the §12 dashboard
	// panels (ws_upgrade / ws_active / ws_duration / ws_bytes /
	// geoip_db_age_seconds) surface from boot. The pre-instantiate
	// loops above stamp every closed label cell — a missing
	// registration here would cause /metrics to omit the series
	// entirely even with the WithLabelValues calls in place.
	//
	// ADR-100 / issue #879 PR-A: also register m.tenantSurfaceCert
	// (the per-surface cert-remint outcome counter) so the
	// gateway_tenant_surface_cert_total{result,kind} series
	// surfaces from boot. The pre-instantiate loop above (where
	// tenantSurfaceCert is stamped across the closed (result, kind)
	// cartesian) is the same pattern as the rest of the family.
	reg.MustRegister(m.requests, m.requestDuration, m.wakeLatency, m.wakeLatencyByNode, m.wakeQueueWait, m.wakePhaseDuration, m.queueDepth, m.rateLimited, m.accountRateLimited, m.coldBoot, m.tlsCertExpiry, m.tlsCertExpiryByHost, m.tlsCertExpiryRefresherWalkComplete, m.tlsOnDemandDenied, m.tenantSurfaceCert, m.wakeLocality, m.wakeSnapshotTier, m.computeNodeChangedSubscriberAlive, m.responseBytes, m.streamFlushes, m.streamActive, m.edgeRuleMatch, m.edgeRuleApply, m.edgeRuleValidateFailures, m.validateFailures, m.edgeRuleCompileError, m.responseBodyWarnTotal, m.internalAuthMatch, m.appMaintenance, m.requestsByRoute, m.durationByRoute, m.failuresByRoute, m.leaderBootstrapAborts, m.wsUpgradeTotal, m.wsActiveSessions, m.wsSessionDuration, m.wsSessionBytes, m.geoipDBAgeSeconds, m.routeConsumerThrottleDecisions, m.responseCache, m.responseCacheWakesAvoided, m.responseCacheBytes, m.responseCacheEntries)
	// Issue #587 / PR-A: per-daemon graceful-shutdown drain
	// observability. Same shape as the wire.OpsMetrics series,
	// registered on the gateway.Metrics registry so it surfaces
	// in the existing /metrics scrape (gatewayd-internal uses
	// gateway.Metrics; gatewayd-public uses wire.OpsMetrics).
	// Both daemons see the same metric NAME; only the registry
	// differs. The {daemon, op} labels are pre-instantiated
	// here so the dashboard surfaces rows from boot.
	m.drainWaitSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_drain_wait_seconds",
		Help:    "Wall-clock seconds the graceful-shutdown drain (issue #587 / PR-A / pkg/gateway/drain) waited before every in-flight request goroutine finished. Labelled by {daemon, outcome}; outcome ∈ {clean, deadline_exceeded, ctx_cancelled} so an operator can tell a fast clean drain from a forced one without re-reading the daemon log. Bucket set covers <100ms idle drain up to the full DrainGrace=25s ceiling.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 20, 25},
	}, []string{"daemon", "outcome"})
	m.drainWaitSeconds.WithLabelValues("gatewayd-internal", "clean")
	m.drainWaitSeconds.WithLabelValues("gatewayd-internal", "deadline_exceeded")
	m.drainWaitSeconds.WithLabelValues("gatewayd-internal", "ctx_cancelled")
	m.drainWaitSeconds.WithLabelValues("gatewayd-public", "clean")
	m.drainWaitSeconds.WithLabelValues("gatewayd-public", "deadline_exceeded")
	m.drainWaitSeconds.WithLabelValues("gatewayd-public", "ctx_cancelled")
	m.inflightRequests = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gateway_inflight_requests",
		Help: "Current in-flight request count tracked by the per-daemon drain.Tracker (issue #587 / PR-A / pkg/gateway/drain). Labelled by {daemon, op}; op ∈ {http, upgrade, control}. NO plan or app label — Prometheus cardinality discipline (per the cluster plan's 'Decisions baked in' §2).",
	}, []string{"daemon", "op"})
	m.inflightRequests.WithLabelValues("gatewayd-internal", "http")
	m.inflightRequests.WithLabelValues("gatewayd-internal", "upgrade")
	m.inflightRequests.WithLabelValues("gatewayd-internal", "control")
	m.inflightRequests.WithLabelValues("gatewayd-public", "http")
	m.inflightRequests.WithLabelValues("gatewayd-public", "upgrade")
	m.inflightRequests.WithLabelValues("gatewayd-public", "control")
	reg.MustRegister(m.drainWaitSeconds, m.inflightRequests)
	return m
}

// ObserveDrainWait (issue #587 / PR-A) records the wall-clock
// duration of a drain.Tracker.Drain call. outcome is one of
// drain.Outcome{Clean,DeadlineExceeded,Cancelled}; daemon ∈
// {gatewayd-public, gatewayd-internal}. Nil-safe.
func (m *Metrics) ObserveDrainWait(daemon, outcome string, seconds float64) {
	if m == nil || m.drainWaitSeconds == nil {
		return
	}
	m.drainWaitSeconds.WithLabelValues(daemon, outcome).Observe(seconds)
}

// SetInflightRequests (issue #587 / PR-A) sets the per-daemon
// per-op in-flight gauge. Nil-safe.
func (m *Metrics) SetInflightRequests(daemon, op string, count float64) {
	if m == nil || m.inflightRequests == nil {
		return
	}
	m.inflightRequests.WithLabelValues(daemon, op).Set(count)
}

// Registry returns the underlying *prometheus.Registry — pass to promhttp.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler returns an http.Handler that serves the registry's metrics in the
// Prometheus text exposition format. Mount at /metrics on the control listener.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// ObserveRequest records a completed request's outcome. code is the HTTP
// status class as a 3-digit string ("200", "404", "503"...).
func (m *Metrics) ObserveRequest(appID, plan, code string) {
	m.requests.WithLabelValues(appID, plan, code).Inc()
}

// ObserveResponseBytes increments the per-(app, plan) egress byte
// counter for ADR-046 PR-2. Nil-receiver safe (mirrors the rest of
// the Observe* family). Called from Handler.recordEgress on the
// 2xx/3xx path only. The counter lives on the gatewayd-internal-local
// registry; the cross-daemon pkg/wire mirror was removed (review
// pass: no production caller existed).
func (m *Metrics) ObserveResponseBytes(appID, plan string, n int64) {
	if m == nil || appID == "" || plan == "" || n <= 0 {
		return
	}
	m.responseBytes.WithLabelValues(appID, plan).Add(float64(n))
}

// ObserveResponseBodyWarn increments the response-body warn counter
// for issue #995 Phase 4 / ADR-121. Called from capWriter when a
// response crosses the near-threshold (80%) or exceeded (100%) mark
// for the per-plan MaxResponseBodyBytes cap. The two flags are
// mutually exclusive at the call site (exceeded wins); the
// implementation tolerates both true as a no-op guard, but the
// contract is "exactly one" so the counter increments in a single
// bucket. route_label is intentionally NOT admitted (ADR-093 cap of
// 50 per app); app_id is bounded by the platform's per-account app
// count (~100s) — same precedent as edgeRuleApply. Nil-receiver
// safe.
func (m *Metrics) ObserveResponseBodyWarn(appID string, nearThreshold, exceeded bool) {
	if m == nil || appID == "" {
		return
	}
	switch {
	case exceeded:
		m.responseBodyWarnTotal.WithLabelValues(appID, "exceeded").Inc()
	case nearThreshold:
		m.responseBodyWarnTotal.WithLabelValues(appID, "near_threshold").Inc()
	}
}

// ObserveStreamFlush increments the per-(app, plan) streaming
// flush counter for ADR-047 PR-B. Called from the per-flush
// onFlush closure installed by setupStreamingWriter; one inc per
// statusRecorder.doFlush call on the streaming path. The
// buffered path never calls this. Nil-receiver safe (the
// Handler.setupStreamingWriter site already nil-guards before
// the call, but the extra safety here matches the rest of the
// Observe* family and keeps the unit tests honest).
func (m *Metrics) ObserveStreamFlush(appID, plan string) {
	if m == nil || appID == "" || plan == "" {
		return
	}
	m.streamFlushes.WithLabelValues(appID, plan).Inc()
}

// ObserveStreamStart increments the per-(app, plan) concurrent
// streaming gauge for ADR-047 PR-D. Called from
// setupStreamingWriter when the streaming path is chosen.
// The buffered path never calls this — buffered requests
// durate the response in a single Go-time transfer and don't
// participate in the streaming concurrency model. Balanced by
// ObserveStreamEnd in the handler's defer after the final
// flush. Nil-receiver safe (mirrors the rest of the Observe*
// family).
func (m *Metrics) ObserveStreamStart(appID, plan string) {
	if m == nil || appID == "" || plan == "" {
		return
	}
	m.streamActive.WithLabelValues(appID, plan).Inc()
}

// ObserveStreamEnd decrements the per-(app, plan) concurrent
// streaming gauge. Always paired with ObserveStreamStart in
// the same handler goroutine (handler.go:setupStreamingWriter
// installs the Inc; the handler's defer installs the Dec after
// the final flush). The defer pattern makes the gauge
// leak-free under panic — the only way the gauge can drift is
// if a future PR moves the Dec out of the defer, which the
// TestMetricsStreamActiveStartEnd 1000-iteration stress loop
// catches. Nil-receiver safe.
func (m *Metrics) ObserveStreamEnd(appID, plan string) {
	if m == nil || appID == "" || plan == "" {
		return
	}
	m.streamActive.WithLabelValues(appID, plan).Dec()
}

// ObserveRequestDuration records the full request duration (received →
// handler return) for the {app, class} label tuple. class ∈ {"2xx",
// "3xx", "4xx", "5xx"}; anything outside that closed set falls through
// to prometheus's default behaviour (a new label tuple surfaces in
// /metrics). Issue #273 / ADR-042.
//
// Nil-receiver safe (follows the ObserveWakeQueueWait precedent) so
// the Handler hot path doesn't need to nil-guard on every request.
func (m *Metrics) ObserveRequestDuration(appID, class string, d time.Duration) {
	if m == nil {
		return
	}
	m.requestDuration.WithLabelValues(appID, class).Observe(d.Seconds())
}

// PreInstantiateApp writes zero-valued series for the closed (class)
// label set under appID so dashboards surface from the first request
// rather than after the first observation (issue #273 / ADR-042). App
// IDs are runtime values that cannot be pre-instantiated at boot, but
// the inner class set is closed and bounded. Idempotent — calling
// twice for the same appID is cheap (prometheus's WithLabelValues
// returns the existing series). Call from the Handler's Backend.Lookup
// hit path, deduped via sync.Map so the hot path stays allocation-
// free after first sight.
func (m *Metrics) PreInstantiateApp(appID string) {
	if m == nil || appID == "" {
		return
	}
	for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
		m.requestDuration.WithLabelValues(appID, class)
	}
}

// ObserveRequestRoute records one per-route counter increment for
// opt-in apps (ADR-093 / issue #273 follow-up to ADR-042 §1). The
// caller (Handler.observe) is responsible for routing the call
// here only when the app's RouteMetricsEnabled flag is true AND
// the operator kill-switch is enabled — the metrics graph
// itself is shared and accepts every label. nil-receiver safe
// (mirrors ObserveRequestDuration). The route argument is the
// post-admit value (caller routed through routeLabelSet so the
// caller has already paid the cap check); passing the pre-admit
// string would create a way to bypass the cap.
func (m *Metrics) ObserveRequestRoute(appID, plan, route, code string) {
	if m == nil || appID == "" || route == "" {
		return
	}
	m.requestsByRoute.WithLabelValues(appID, plan, route, code).Inc()
}

// ObserveRequestDurationRoute records the per-route histogram
// observation. Caller-routed through routeLabelSet on the
// routeLabelSet side (same module-private pointer); nil-receiver
// safe.
func (m *Metrics) ObserveRequestDurationRoute(appID, route, class string, d time.Duration) {
	if m == nil || appID == "" || route == "" {
		return
	}
	m.durationByRoute.WithLabelValues(appID, route, class).Observe(d.Seconds())
}

// RequestFailureRoute records one per-route failure counter
// increment for opt-in apps. Mirrors the existing per-app
// gateway_requests_total{code} reader pattern; the dashboard
// computes error_rate_pct as the ratio of this counter to
// ObserveRequestRoute for the same (app, route). Caller-routed
// through routeLabelSet. nil-receiver safe.
func (m *Metrics) RequestFailureRoute(appID, plan, route, code string) {
	if m == nil || appID == "" || route == "" {
		return
	}
	m.failuresByRoute.WithLabelValues(appID, plan, route, code).Inc()
}

// PreInstantiateAppRoute writes zero-valued series for the
// closed (class) set under (appID, route) so dashboards surface
// from the first request rather than after the first observation
// (ADR-093 D4). The closed class set is the same as
// PreInstantiateApp above (2xx/3xx/4xx/5xx). Caller-routed
// through routeLabelSet — the route argument is post-admit, so
// the cap is enforced before this is called. nil-receiver safe.
func (m *Metrics) PreInstantiateAppRoute(appID, route string) {
	if m == nil || appID == "" || route == "" {
		return
	}
	for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
		m.durationByRoute.WithLabelValues(appID, route, class)
	}
}

// ObserveRateLimit records a 429 outcome.
func (m *Metrics) ObserveRateLimit(appID, plan string) {
	m.rateLimited.WithLabelValues(appID, plan).Inc()
}

// ObserveAccountRateLimit records a 429 outcome from the per-account
// limiter (ADR-040 / issue #292). Nil-receiver-safe — mirrors the
// ObserveWakeQueueWait / ObserveTLSOnDemandDenied pattern, unlike
// ObserveRateLimit (which the call site guards with `if h.metrics != nil`).
// Per-account 429s are the dashboard's primary abuse signal so the
// call site shouldn't have to remember the nil guard.
//
// accountID is routed through the bounded accountLabelSet (issue #278
// pattern from pkg/wire/metrics.go) before reaching the counter.
// Overflow past accountLabelSetCap collapses to "__other__" so the
// §12 panel stays bounded past customer-count growth; the literal
// label "anonymous" and the overflow "other" are passed through
// untouched so the closed (plan) pre-instantiated series keep working.
func (m *Metrics) ObserveAccountRateLimit(accountID, plan string) {
	if m == nil {
		return
	}
	if m.accountLabels != nil {
		accountID = m.accountLabels.admit(accountID)
	}
	m.accountRateLimited.WithLabelValues(accountID, plan).Inc()
}

// ObserveColdBoot records that this request caused a cold boot and observes
// the wake latency (request-received to first upstream byte). Issue #273 /
// ADR-042 renamed from ObserveColdWake; the gateway_cold_wake_total →
// gateway_cold_boot_total rename is intentional and not dual-emitted.
//
// PR #4 (ADR-092 §3.5) also observes the labelled per-node histogram
// `gateway_wake_latency_seconds_by_node`. The label value is the
// compute_node.id (UUID) — gatewayd-internal has no name cache on
// the wake path, and id is the durable shard key. nodeID=="" is
// possible only on a wake that lost its target (parked mid-wake);
// the labelled bucket "__unknown" preserves the observation
// without polluting the per-node quantiles — the unknown bucket
// is excluded from per-node PromQL by the obsNodeWakeLatency
// handler's matcher.

// ObserveLeaderBootstrapAbort (ADR-098 C7) records a detached-leader
// goroutine abort under the bootstrap cap. Closed (reason) set
// pre-instantiated in NewMetrics; nil-safe on the receiver.
func (m *Metrics) ObserveLeaderBootstrapAbort(reason string) {
	if m == nil {
		return
	}
	m.leaderBootstrapAborts.WithLabelValues(reason).Inc()
}

func (m *Metrics) ObserveColdBoot(appID string, latency time.Duration, nodeID string) {
	m.coldBoot.WithLabelValues(appID).Inc()
	m.wakeLatency.Observe(latency.Seconds())
	if m.wakeLatencyByNode == nil {
		return
	}
	label := nodeID
	if label == "" {
		label = "__unknown"
	}
	m.wakeLatencyByNode.WithLabelValues(label).Observe(latency.Seconds())
}

// ObserveWakeQueueWait records how long a request waited in the
// per-app wake queue before the gate released it (single-flight
// coalescing). Nil-safe so WakeGate can call it without branching.
//
// Deprecated: also observed via gateway_wake_phase_duration_seconds{phase="queue_wait"}.
// The scalar here stays for one release — dashboards migrate in
// the follow-up. ADR-098 C11.
func (m *Metrics) ObserveWakeQueueWait(d time.Duration) {
	if m == nil {
		return
	}
	m.wakeQueueWait.Observe(d.Seconds())
	// ADR-098 C11: dual-write into the labelled phase histogram so
	// dashboards querying the new name see the same series shape.
	m.wakePhaseDuration.WithLabelValues("queue_wait").Observe(d.Seconds())
}

// ObserveWakePhase records a single phase-decomposed wake boundary
// measurement (ADR-098 C11). phase ∈ {"queue_wait", "coordinator_wait",
// "schedd_admit", "vmmd_wake", "guest_ready", "cold_fallback_reason"}.
// Closed set is pre-instantiated in NewMetrics. Nil-safe so the
// gateway hot path doesn't branch.
func (m *Metrics) ObserveWakePhase(phase string, d time.Duration) {
	if m == nil {
		return
	}
	m.wakePhaseDuration.WithLabelValues(phase).Observe(d.Seconds())
}

// ObserveWakeLocality increments the wake-locality counter for the
// given outcome (PR scale-out readiness). outcome ∈ {local_snapshot,
// local_coldboot} today; unknown values fall through to Prometheus's
// default behaviour (a new label tuple surfaces in /metrics but the
// dashboard panel never queries for them). Nil-safe so the Handler
// hot path doesn't need a nil guard — mirrors ObserveWakeQueueWait
// and ObserveTLSOnDemandDenied. Only call from paths that actually
// admitted an instance; warm requests and at-capacity benign
// outcomes should NOT invoke this so the metric answers "what
// fraction of admissions were local?" not "what fraction of
// requests were local?".
func (m *Metrics) ObserveWakeLocality(outcome string) {
	if m == nil {
		return
	}
	m.wakeLocality.WithLabelValues(outcome).Inc()
}

// ObserveEdgeRuleMatch (ADR-089 PR 3) increments the edge-rule
// matcher counter. kind is the EdgeRuleKind (route today; PR 4-7
// extend), outcome ∈ {match, miss, blocked}. The label tuples are
// pre-instantiated at boot in NewMetrics so the §12 dashboard panel
// "edge rule match rate" surfaces from first scrape — see the
// tlsOnDemandDenied / wakeLocality pre-instantiation loop above.
// Nil-safe so the Handler hot path doesn't need a nil guard.
// "match" is fired only when a rule substituted the inbound App
// end-to-end; "blocked" is fired only on cross-account attempts
// (defense-in-depth on top of the apid create-time same-account
// guarantee); "miss" is fired on a clean miss (no rule for the
// host, or no rule whose path/method matched). PR 4-7 will call
// this with additional kind values from their own match paths.
func (m *Metrics) ObserveEdgeRuleMatch(kind, outcome string) {
	if m == nil {
		return
	}
	m.edgeRuleMatch.WithLabelValues(kind, outcome).Inc()
}

// ObserveEdgeRuleApply (ADR-091 hardening PR-A) increments the
// apply-path counter. kind is the EdgeRuleKind; result is one of
// {success, error}. Distinct from ObserveEdgeRuleMatch, which counts
// the matcher's pick — a rule can match and still fail at apply time.
// Nil-safe so the Handler hot path doesn't need a nil guard. PR #4
// does not extend the result set; future error subclasses (e.g.
// "jwks_expired", "ip_empty") require a new metric to keep cardinality
// bounded (the §12 panel queries on {result=success}/{result=error}).
func (m *Metrics) ObserveEdgeRuleApply(kind, result string) {
	if m == nil {
		return
	}
	m.edgeRuleApply.WithLabelValues(kind, result).Inc()
}

// ObserveEdgeRuleValidateFailure (issue #975 #3 / Mega-Foundation #979-a
// / ADR-128 §5) increments the kind=validate failure counter. mode is
// the rule's validate_mode (observe|warn|block); reason is the
// bounded taxonomy from pkg/edgevalidate (required_missing |
// type_mismatch | additional_properties_not_allowed |
// enum_violation | format_violation | other). The metric is
// incremented in every mode — the reject decision is independent and
// handled by the handler. Nil-safe so the Handler hot path doesn't
// need a nil guard, mirroring ObserveEdgeRuleMatch /
// ObserveEdgeRuleApply above. Unknown mode values are coerced to
// "other" so a malformed wire payload cannot add a new label tuple
// and break the §12 dashboard panel.
//
// ADR-128 §5 — both the deprecated
// gateway_edge_rule_validate_failures_total{mode, reason} counter
// (legacy dashboards) AND the new gateway_validate_failures_total
// {app_id, rule_id, mode, reason} counter are incremented on each
// call. The legacy counter is shadow-emitted for one release, then
// dropped. The (appID, ruleID) pair is admitted through ruleLabelSet
// (cap 256 per app; overflow → "__other__") so the new metric's
// rule_id axis stays bounded. Pass appID=""/ruleID="" if the caller
// cannot supply them (defensive — the handler always does); the
// ruleLabel helper coerces empty inputs to "__other__".
func (m *Metrics) ObserveEdgeRuleValidateFailure(appID, ruleID, mode, reason string) {
	if m == nil {
		return
	}
	switch mode {
	case "observe", "warn", "block":
	default:
		mode = reasonOther
	}
	switch reason {
	case "required_missing",
		"type_mismatch",
		"additional_properties_not_allowed",
		"enum_violation",
		"format_violation":
	default:
		reason = reasonOther
	}
	// Legacy counter (ADR-128 §5 deprecation window).
	m.edgeRuleValidateFailures.WithLabelValues(mode, reason).Inc()
	// Canonical counter — rule_id is admitted through the
	// per-app set so the Prometheus series set stays bounded.
	resolvedRule := m.ruleLabel(appID, ruleID)
	m.validateFailures.WithLabelValues(appID, resolvedRule, mode, reason).Inc()
}

// ruleLabel exposes the per-app admission set as a Metrics
// method so callers don't need to know the underlying type. Safe
// on a nil receiver — returns the input unchanged for the daemon
// paths that don't wire Metrics (unit tests, see pkg/gateway
// test fixtures). Mirrors OpsMetrics.boxLabel
// (pkg/wire/metrics.go:5966).
func (m *Metrics) ruleLabel(appID, ruleID string) string {
	if m == nil || m.ruleLabels == nil {
		return ruleID
	}
	return m.ruleLabels.admit(appID, ruleID)
}

// ObserveRouteConsumerThrottleDecision (ADR-104, issue #881 Phase 3)
// increments the per-consumer throttle decision counter. Called
// from applyEdgeRuleThrottle after the AllowWithConsumerKey result
// is in. kind is the resolved KeyBy dimension
// (pkg/api.ThrottleKeyByNone | ...APIKey | ...JWTSubject | ...JWTClaim)
// — normalised to the closed set, with "" coerced to "none" so an
// empty wire payload doesn't add a new label tuple. outcome is
// one of {admit, throttle, anonymous} — anonymous is the
// authn-missing path on a per-consumer rule, a misconfiguration
// signal for the dashboard. Nil-safe.
func (m *Metrics) ObserveRouteConsumerThrottleDecision(kind, outcome string) {
	if m == nil {
		return
	}
	if kind == "" {
		kind = "none"
	}
	m.routeConsumerThrottleDecisions.WithLabelValues(kind, outcome).Inc()
}

// ObserveInternalAuthMatch (ADR-119) increments the
// apps.public_auth_mode='internal_only' verification outcome
// counter. Called from Handler.applyIngressInternalSvc (and the
// parallel SynthServer.applyIngressInternalSvc at synth.go:300,
// :340, :617) after the gate runs. outcome is one of
// {matched, blocked} — the closed set pre-instantiated at boot.
// Round-2 peer-review (#6) removed the dead `bypass_stripped`
// label. An unknown outcome is coerced to "blocked" so a future
// regression that ships a new outcome string does not blow up
// the §12 dashboard chip on first contact (same fallback
// posture as ObserveEdgeRuleMatch). Nil-safe.
func (m *Metrics) ObserveInternalAuthMatch(outcome string) {
	if m == nil {
		return
	}
	switch outcome {
	case "matched", "blocked":
		// known
	default:
		outcome = "blocked"
	}
	m.internalAuthMatch.WithLabelValues(outcome).Inc()
}

// ObserveEdgeRuleCompileError (ADR-091 hardening PR-A) increments the
// compile-time error counter. Called from
// cmd/gatewayd-internal/edge_rules.go::warnPathGlobErrs when a rule's
// glob/path/CIDR failed to parse at boot. kind is one of the seven
// shipped kinds; the loader dropped the offending rule and is serving
// traffic without it. Nil-safe so the loader doesn't need a nil guard.
func (m *Metrics) ObserveEdgeRuleCompileError(kind string) {
	if m == nil {
		return
	}
	m.edgeRuleCompileError.WithLabelValues(kind).Inc()
}

// ObserveAppMaintenance (ADR-091 amendment / §4.1.2.0) increments
// the coarse-gate apps.maintenance_mode short-circuit counter.
// plan is the matched app's plan (Free|Hobby|Pro|Scale). The
// counter is closed-set bounded by the pre-instantiation loop
// above; an unknown plan still emits (the metric accepts any label
// value) but only the four canonical plans surface in the §12
// dashboard panel. Nil-safe so the Handler hot path doesn't need
// a nil guard.
func (m *Metrics) ObserveAppMaintenance(plan string) {
	if m == nil {
		return
	}
	m.appMaintenance.WithLabelValues(plan).Inc()
}

// ObserveWakeSnapshotTier (issue #470 / PR #470-FU-B) increments
// the per-wake snapshot-tier counter. tier ∈ {warm, init, cold};
// the engine sets tier on the wake outcome (PR #470-FU-A) and the
// handler increments here. The dashboard panel "p50 wake latency
// by snapshot tier" joins this counter with the wake-latency
// histogram on the wake completion time (the histogram is
// unlabeled by tier to keep cardinality bounded). The counter
// itself is bounded to 3 label values so the panel query is
// safe. Empty tier falls back to "init" — the engine's default
// tier when the snapshot is mid-creation or the wake outcome is
// the legacy (pre-#470) parked-then-restored path. Nil-safe so
// the Handler hot path doesn't need a nil guard.
func (m *Metrics) ObserveWakeSnapshotTier(tier string) {
	if m == nil {
		return
	}
	if tier == "" {
		tier = tierInit
	}
	m.wakeSnapshotTier.WithLabelValues(tier).Inc()
}

// TouchComputeNodeChangedSubscriber bumps the subscriber-liveness gauge
// by 1. Called every `subscriberHeartbeatInterval` (30s, see
// cmd/gatewayd-internal/nodecache.go) by the heartbeat goroutine that mirrors
// StartCertExpiryRefresher. The gauge value is monotonically
// increasing until the heartbeat goroutine ends (ctx cancel / channel
// close / initial subscribe failure) — the freeze is the "I'm stale"
// signal. The exact value is irrelevant; the alert cares about
// "frozen or still 0", not "=N". Gauge starts unset (Prometheus drops
// NaN) so the panel row is absent before the first tick; the
// freeze-detection rule will handle absent correctly the same way it
// does for gateway_tls_cert_expiry_seconds.
//
// The Prometheus rule itself (window, expression, severity) is ops
// wiring and out of scope for this PR. The hook is the
// observability point.
//
// Nil-safe: cmd/gatewayd-internal/tests may pass a nil *Metrics. Production
// passes deps.metrics which is always non-nil after NewMetrics.
func (m *Metrics) TouchComputeNodeChangedSubscriber() {
	if m == nil || m.computeNodeChangedSubscriberAlive == nil {
		return
	}
	m.computeNodeChangedSubscriberAlive.Inc()
}

// TopTenantRPSEmit drives the gateway_top_tenant_rps gauge from
// the sampler goroutine. topN is the sorted (id, rps) slice
// produced by the sampler; "other" is the overflow bucket rps.
// Called once per 5s tick; nil-safe.
//
// The sampler in cmd/gatewayd-internal/topn.go runs a local topAccountSet
// (mirroring pkg/wire/topn.go) and computes the top-N from its
// own snapshot. It passes the resulting (id, rps) list here
// rather than a closure because the gateway side has no
// topAccountSet of its own to query. Same cardinality bound
// (cap + 1) applies — the sampler caps the slice length before
// calling this method.
//
// Returns the number of series emitted.
func (m *Metrics) TopTenantRPSEmit(topN []TopNEntry, otherRPS float64) int {
	if m == nil || m.topTenantRPS == nil {
		return 0
	}
	for _, e := range topN {
		m.topTenantRPS.WithLabelValues(e.ID).Set(e.RPS)
	}
	m.topTenantRPS.WithLabelValues("other").Set(otherRPS)
	return len(topN) + 1
}

// TopNEntry is the (id, rps) tuple the gateway sampler passes to
// Metrics.TopTenantRPSEmit. Defined here (not in the sampler)
// because the gateway Metrics surface owns the gauge and the
// sampler just feeds it.
type TopNEntry struct {
	ID  string
	RPS float64
}

// SetQueueDepth records the current wake-queue depth for an app.
func (m *Metrics) SetQueueDepth(appID string, depth int) {
	m.queueDepth.WithLabelValues(appID).Set(float64(depth))
}

// ObserveTLSOnDemandDenied increments the per-reason counter that backs
// gateway_tls_on_demand_denied_total (ADR-024 H3). reason ∈ {allowlist,
// dns01, token}; unknown reasons fall through to the
// prometheus.NewCounterVec default behaviour (a new labelled series
// surfaces in /metrics but the operator panel never queries for them).
// Called from pkg/gateway/tls_wire.go's allowlistToDecisionFunc — today
// only with reason="allowlist"; the dns01 + token branches are wired in
// the H3.b follow-up that bridges certmagic's ACME-issuer logger. Safe
// on a nil receiver so callers running outside the daemon (tests with a
// stub Metrics) don't need a nil-check at every call site; matches the
// ObserveBuildCount / SetResidentGBPerCustomer nil-safe precedent.
func (m *Metrics) ObserveTLSOnDemandDenied(reason string) {
	if m == nil {
		return
	}
	m.tlsOnDemandDenied.WithLabelValues(reason).Inc()
}

// ObserveTenantSurfaceCert (ADR-100 / issue #879) increments the
// per-surface cert-remint counter. result ∈ {issued, failed,
// skipped} (skipped = no verified hostnames / soft-deleted
// surface / unsupported cert_kind — paths that never reach the
// CA). kind ∈ {per_host_san, shared_wildcard}. nil-safe so the
// pkg/gateway cert-issuer path can be exercised by tests that
// don't wire a full Metrics — same pattern as
// ObserveTLSOnDemandDenied above.
func (m *Metrics) ObserveTenantSurfaceCert(result, kind string) {
	if m == nil {
		return
	}
	m.tenantSurfaceCert.WithLabelValues(result, kind).Inc()
}

// SetTLSCertExpiry writes the smallest remaining lifetime across cached
// certs on disk to the gateway_tls_cert_expiry_seconds gauge (ADR-024
// H3, closed in PR #345). d is the time delta to the soonest-expiring
// cert — positive when at least one cert is on disk and unexpired,
// negative when a cert is already past its NotAfter (the page rule
// fires regardless of sign). Callers must NOT touch the gauge when
// there are no certs; the prometheus.Gauge default is "no series",
// and Prometheus's `<` comparator against a missing series returns
// false (so the alert is silent pre-first-mint). Refreshed every 5 min
// by StartCertExpiryRefresher (see pkg/gateway/cert_expiry.go). Safe
// on a nil receiver.
func (m *Metrics) SetTLSCertExpiry(d time.Duration) {
	if m == nil {
		return
	}
	m.tlsCertExpiry.Set(d.Seconds())
}

// ObserveHostCertExpiry writes the remaining lifetime for one host to
// the gateway_tls_cert_expiry_by_host_seconds gauge (Finding 2).
// hostname is routed through m.hostnameLabels.admit() before reaching
// the gauge; overflow collapses to hostname="__other__". The gauge
// value semantics mirror SetTLSCertExpiry: positive for a not-yet-
// expired cert, negative for an expired one (the per-host page rule
// fires regardless of sign).
//
// Records the (hostname, kind) tuple in m.hostKinds so the stale-
// delete path on subsequent ticks can target the exact tuple (the
// hostnameLabelSet is hostname-keyed only and cannot recover the
// kind later).
//
// kind ∈ {wildcard, ondemand, unknown}. Safe on a nil receiver.
func (m *Metrics) ObserveHostCertExpiry(hostname, kind string, d time.Duration) {
	if m == nil {
		return
	}
	if m.hostnameLabels != nil {
		hostname = m.hostnameLabels.admit(hostname)
	}
	m.tlsCertExpiryByHost.WithLabelValues(hostname, kind).Set(d.Seconds())
	if m.hostKinds != nil {
		m.hostKinds[hostname] = kind
	}
}

// DeleteHostCertExpiry removes a (hostname, kind) series from the
// gateway_tls_cert_expiry_by_host_seconds gauge (Finding 2). Used
// by the cert-expiry refresher to drop stale-host series on every
// walk: a host that was present in tick N but absent in tick N+1
// must not carry a stale value into the next tick, so the gauge
// series is deleted (DeleteLabelValues is the Prometheus-canonical
// way to drop a labelled series — exposition drops absent series
// entirely, so the alert's < expression returns false).
//
// hostname is routed through m.hostnameLabels.admit() before the
// delete so the call matches the same label tuple the original
// ObserveHostCertExpiry would have written. Also forgets the
// (hostname, kind) tuple in m.hostKinds. Safe on a nil receiver;
// idempotent on a non-existent series (DeleteLabelValues returns
// false but does not error).
func (m *Metrics) DeleteHostCertExpiry(hostname, kind string) {
	if m == nil {
		return
	}
	if m.hostnameLabels != nil {
		hostname = m.hostnameLabels.admit(hostname)
	}
	m.tlsCertExpiryByHost.DeleteLabelValues(hostname, kind)
	if m.hostKinds != nil && m.hostKinds[hostname] == kind {
		delete(m.hostKinds, hostname)
	}
}

// ObserveCertExpiryRefresherWalkResult increments the
// gateway_tls_cert_expiry_refresher_walk_complete_total counter for
// the result ∈ {complete, partial, empty}. The refresher calls this
// once per tick. Safe on a nil receiver so unit tests can omit the
// metric bundle without nil-guarding every call site.
func (m *Metrics) ObserveCertExpiryRefresherWalkResult(result string) {
	if m == nil {
		return
	}
	m.tlsCertExpiryRefresherWalkComplete.WithLabelValues(result).Inc()
}

// requestLogger is a one-line structured slog request logger used by Handler.
// Built as a type so tests can replace WithLogger.
type requestLogger struct{ log *slog.Logger }

func (l *requestLogger) Log(appID, code string, latency time.Duration, cold bool, requestID string) {
	if l == nil || l.log == nil {
		return
	}
	// requestID flows from the x-faas-request-id HTTP header (pkg/gateway/observability.go:requestIDFrom)
	// and is therefore attacker-controllable. Strip CR/LF/NUL/DEL before logging so a forged
	// header cannot smuggle a new log line into the stream. appID and code are server-generated
	// (UUIDs / HTTP status class digit) and need no sanitization.
	//
	// codeql[go/log-injection] false-positive: logsanitize.Field is not in CodeQL's sanitizer model
	// (the query only recognizes inline strings.ReplaceAll), but it does strip the injection bytes
	// at runtime — matching the defense-in-depth precedent set for the synth RPC (47d5531).
	l.log.Info("gateway_request",
		"app_id", appID,
		"code", code,
		"latency_ms", latency.Milliseconds(),
		"cold", cold,
		"request_id", logsanitize.Field(requestID),
	)
}

// Issue #676 / ADR-080 follow-up, PR-B: closed-set label constants
// for the gateway_ws_* Prometheus surface. Defining the sets here
// — alongside the per-Handler Metrics type — means the constructor
// pre-instantiate loop and the runtime helper methods share the
// same source of truth. Adding a fourth outcome or a third
// direction requires a deliberate constant edit; the alternative
// (string literals scattered across pkg/gateway + handler.go) is
// how §12 dashboard naming conventions get silently out of sync
// with the metric names.
type WSOutcome string

const (
	// WSOutcomeAccepted — request cleared the three-input gate
	// (isUpgradeRequest + plan allowed + h.rawByNode wired) and
	// the raw forwarder accepted the stream. The session
	// duration histogram and the byte counters both fire on
	// this outcome.
	WSOutcomeAccepted WSOutcome = "accepted"
	// WSOutcomePlanDenied — Plan.WebSocketResponseAllowed()
	// returned false (Free plan). Surfaced by both the request-
	// time 501 (pkg/gateway/handler.go writeWebSocketNotAllowed)
	// and the PATCH-time 403
	// (cmd/apid/handlers_ext.go:261-268).
	WSOutcomePlanDenied WSOutcome = "plan_denied"
	// WSOutcomeBridgeDisabled — either the
	// FAAS_GATEWAY_RAW_STREAM_ENABLED=false kill switch
	// (cmd/gatewayd-internal/run.go: PR-A) or h.rawByNode==nil
	// in tests. Surfaced as the forwarderMissing=true branch of
	// writeWebSocketNotAllowed with the same 501 +
	// x-faas-error-reason: websocket_not_on_plan response shape.
	WSOutcomeBridgeDisabled WSOutcome = "bridge_disabled"
	// WSOutcomeInitFailed — rawStreamOnceWithEvents failed to
	// open the bidi ForwardRawStream or send the init frame
	// (the gRPC dial refused, the box lost its mTLS cert, or
	// the bridge binary is missing). Distinct from
	// client_disconnect because the failure happens BEFORE
	// any bytes flow; the byte counters stay at zero.
	WSOutcomeInitFailed WSOutcome = "init_failed"
	// WSOutcomeUpstreamUnavailable — vmmd returned
	// codes.Unavailable mid-session (the box crashed, the
	// bridge binary lost its netns handle, etc.). Surfaced
	// as 503 + body="upstream unavailable" from the
	// receiver loop.
	WSOutcomeUpstreamUnavailable WSOutcome = "upstream_unavailable"
	// WSOutcomeClientDisconnect — the customer closed the
	// connection mid-stream (TCP FIN before EOF on the
	// body-copy goroutine). This is the normal-session-end
	// case for WS clients; the panel
	// rate(gateway_ws_session_duration_seconds{outcome=
	// "client_disconnect"}) is the customer-side churn signal.
	WSOutcomeClientDisconnect WSOutcome = "client_disconnect"
)

type WSDirection string

const (
	// WSDirectionTx — bytes flowing customer → guest
	// (request body + the raw HTTP request line + headers
	// that the raw bridge carries verbatim). Incremented in
	// the body-copy goroutine at
	// pkg/gateway/forwardproxy.go:~558.
	WSDirectionTx WSDirection = "tx"
	// WSDirectionRx — bytes flowing guest → customer
	// (response body + the raw HTTP status line + headers
	// that the bridge pipes back). Incremented in the
	// receiver loop at pkg/gateway/forwardproxy.go:~651.
	// Raw-stream egress bytes ALSO flow through the
	// per-instance egress ring via
	// egressSink.RecordResponseBytes (PR-C follow-up) so
	// usage_minutes.tx_bytes reflects WS workloads without a
	// separate meterd surface.
	WSDirectionRx WSDirection = "rx"
)

// IncWSUpgrade (issue #676 / ADR-080 follow-up, PR-B) bumps the
// {plan, outcome}-labeled gateway_ws_upgrade_total counter at the
// cmd/gatewayd-internal three-input gate
// (pkg/gateway/handler.go:2899). Caller passes the resolved plan
// (Free/Hobby/Pro/Scale from api.Plans) and one of the WSOutcome
// constants. The closed label set is pre-instantiated at boot
// (see NewMetrics) so this call always returns a real counter;
// nil only if m itself is nil (callers in the pre-metrics test
// corpus use nil-safe wrap patterns).
func (m *Metrics) IncWSUpgrade(plan string, outcome WSOutcome) {
	if m == nil {
		return
	}
	m.wsUpgradeTotal.WithLabelValues(plan, string(outcome)).Inc()
}

// IncWSSessionStart (issue #676 / ADR-080 follow-up, PR-B)
// increments the {plan}-labeled gateway_ws_active_sessions
// gauge when a raw-bytes Upgrade session opens. MUST be paired
// with DecWSSessionEnd on every return path;
// pkg/gateway/forwardproxy.go's rawStreamOnceWithEvents uses a
// `IncWSSessionStart + defer DecWSSessionEnd` pair at the top of
// the function so symmetry is automatic across init_failed /
// upstream_unavailable / client_disconnect / accepted.
func (m *Metrics) IncWSSessionStart(plan string) {
	if m == nil {
		return
	}
	m.wsActiveSessions.WithLabelValues(plan).Inc()
}

// DecWSSessionEnd (issue #676 / ADR-080 follow-up, PR-B)
// decrements the {plan}-labeled gateway_ws_active_sessions
// gauge when a raw-bytes Upgrade session closes. MUST be paired
// with IncWSSessionStart; the defer-pair at
// rawStreamOnceWithEvents guarantees symmetry. Calling
// DecWSSessionEnd without a matching Inc is a programmer error
// and will produce negative gauge values.
func (m *Metrics) DecWSSessionEnd(plan string) {
	if m == nil {
		return
	}
	m.wsActiveSessions.WithLabelValues(plan).Dec()
}

// ObserveWSSessionDuration (issue #676 / ADR-080 follow-up, PR-B)
// records one wall-clock seconds sample in the
// {plan, outcome}-labeled gateway_ws_session_duration_seconds
// histogram. Buckets span 50 ms to 24 h; a session hitting the
// rawStreamSessionDeadline ceiling
// (pkg/gateway/forwardproxy.go:464) emits the top bucket
// cleanly. Outcome distinguishes the closed-session paths
// (accepted / client_disconnect) from the failure paths
// (init_failed / upstream_unavailable) so a Grafana panel can
// split "WS churn" (client_disconnect rate) from "WS
// availability" (accepted / (accepted + init_failed +
// upstream_unavailable) ratio).
func (m *Metrics) ObserveWSSessionDuration(plan string, outcome WSOutcome, d time.Duration) {
	if m == nil {
		return
	}
	m.wsSessionDuration.WithLabelValues(plan, string(outcome)).Observe(d.Seconds())
}

// AddWSSessionBytes (issue #676 / ADR-080 follow-up, PR-B) adds
// n bytes to the {plan, direction}-labeled
// gateway_ws_session_bytes_total counter. Use Add (not Inc)
// because a single Write or Read can carry more than 1 byte —
// e.g. a 16 KiB gorilla/websocket frame in the round-trip e2e
// (pkg/gateway/forwardproxy_handler_test.go). Counter math is
// identical to a per-byte Inc but the API surface documents the
// byte-volume intent. n<=0 is a no-op (handles short-reads where
// the body goroutine returned 0 bytes before EOF without
// double-counting).
func (m *Metrics) AddWSSessionBytes(plan string, direction WSDirection, n int64) {
	if m == nil {
		return
	}
	if n <= 0 {
		return
	}
	m.wsSessionBytes.WithLabelValues(plan, string(direction)).Add(float64(n))
}
