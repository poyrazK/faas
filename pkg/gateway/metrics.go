// Prometheus instrumentation for gatewayd (spec §4.1, §12). The metric names
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
//     label value is the resolved app_id — gatewayd's only
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
//     gatewayd-local registry (the daemon's /metrics scrape); the
//     cross-daemon contract is not duplicated here because gatewayd
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
//     compute_node_changed subscriber loop in cmd/gatewayd/nodecache.go.
//     Stale gauge means the NodeClientCache is silently out of date and
//     the next compute_nodes UPSERT is invisible to placement — page
//     rule fires when the gauge freezes or drops to 0. The hook is the
//     OBSERVABILITY point; the alert rule + dashboard panel + window
//     choice live in ops wiring, out of scope for this PR.)
package gateway

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Metrics is the gatewayd Prometheus bundle. Construct once per Handler via
// NewMetrics and pass into NewHandlerWith.
type Metrics struct {
	registry *prometheus.Registry

	requests      *prometheus.CounterVec
	wakeLatency   prometheus.Histogram
	wakeQueueWait prometheus.Histogram
	queueDepth    *prometheus.GaugeVec
	rateLimited   *prometheus.CounterVec
	// responseBytes: ADR-046 PR-2 producer observability.
	// Counter labelled by app (UUID, bounded by per-plan app
	// quotas) and plan (Free|Hobby|Pro|Scale — closed set). The
	// canonical persisted metric is usage_minutes.tx_bytes;
	// this counter is the real-time operator view (egress
	// per app), and backs the §12 FaasTenantEgressSpike alert
	// ("rate > 1GiB/min sustained for 5m on a single app").
	// See ObserveResponseBytes below.
	responseBytes *prometheus.CounterVec
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
	// (cmd/gatewayd/main.go). The /metrics scrape path doesn't read it
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
	// principal. Gatewayd is pre-auth (TLS + hostname routing
	// only); the only tenant-attributable key on the request
	// path is the app_id (the apps table's owner is in apid's
	// domain). Operators reading the panel should treat
	// gateway_top_tenant_rps as "noisy apps seen at the edge"
	// and apid_top_tenant_rps as "noisy customers on the API".
	//
	// The 5s sample cadence + 24h rolling reset are owned by
	// the same sampler pattern as apid's (cmd/gatewayd/main.go
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
	// wakeLocality is the increment-only wake-outcome classifier that
	// backs the multiplex scale-out decision (PR scale-out readiness,
	// ADRs 025/028). Outcome ∈ {local_snapshot, local_coldboot} today;
	// when a second compute node joins, remote_* outcomes slot in
	// transparently without a metric rename. Nil-safe via the
	// ObserveWakeLocality wrapper so the Handler hot path doesn't need
	// to gate on every call.
	wakeLocality *prometheus.CounterVec
	// computeNodeChangedSubscriberAlive is the per-process liveness
	// gauge for the LISTEN compute_node_changed subscriber loop
	// (cmd/gatewayd/nodecache.go:102-141). PR scale-out readiness:
	// bumped every `subscriberHeartbeatInterval` (30s) while the
	// subscriber is alive. On ctx cancel, channel close, or the
	// initial subscribe failure, the heartbeat goroutine stops and
	// the gauge freezes at its last value — operators see "I'm stale"
	// without a separate series per channel. Unlabelled: cardinality
	// is per-process, not per node / channel / daemon. Nil-safe via
	// TouchComputeNodeChangedSubscriber so cmd/gatewayd's wiring can
	// pass nil *Metrics in tests without a guarded call site.
	computeNodeChangedSubscriberAlive prometheus.Gauge
}

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
		wakeLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "gateway_wake_latency_seconds",
			Help: "End-to-end latency from request received to first upstream byte after a cold wake.",
			// Buckets target the §12 SLO: p50 ≤ 0.35 s, p95 ≤ 0.8 s, page > 1.5 s.
			Buckets: []float64{
				0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0,
			},
		}),
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
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_queue_depth",
			Help: "Current number of waiters per app's wake queue (sampled).",
		}, []string{"app"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_rate_limited_total",
			Help: "Requests rejected by the per-app rate limiter.",
		}, []string{"app", "plan"}),
		// ADR-046 PR-2 producer observability. Counter is
		// registered on the gatewayd-local registry (this
		// daemon scrapes /metrics via the control listener).
		// The cross-daemon pkg/wire.OpsMetrics mirror was
		// removed in the PR-2 review pass — there was no
		// production caller, and dual registries with no
		// cross-daemon consumer is dead counter surface.
		responseBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_response_bytes_total",
			Help: "Per-(app, plan) HTTP response body bytes observed by the gateway (ADR-046 PR-2). One Add per observed byte, called once per proxied response after the ReverseProxy returns. Canonical persisted metric is usage_minutes.tx_bytes; this counter is the real-time operator view for §12 anomaly detection.",
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
			Help: "Top-N 5s request rate per tenant observed at the edge (issue #300). Label key is account_id for parity with apid_top_tenant_rps; the label VALUE at the gateway is the resolved app_id (gatewayd is pre-auth and only sees hostname→app routing). Cardinality bounded at topAccountSetCap (1000) + 1 \"other\" overflow by pkg/wire/topn.go via the cmd/gatewayd/topn.go sampler. The overflow bucket literally named \"other\" matches apid's gauge.",
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
		// PR scale-out readiness — wake-locality counter. The closed
		// (outcome) set is pre-instantiated below so the panel surfaces
		// from boot. New outcomes (remote_*) join by widening the
		// pre-instantiation loop; the metric name stays stable.
		wakeLocality: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_wake_locality_total",
			Help: "Wake-outcome classifier used to drive the sticky-vs-shared snapshot-store decision. outcome ∈ {local_snapshot, local_coldboot} today; remote_* outcomes slot in when a second compute node joins. Counting is restricted to admissions that actually brought up an instance — warm requests and at-capacity benign outcomes are not enumerated.",
		}, []string{"outcome"}),
		// PR scale-out readiness — liveness gauge for the LISTEN
		// compute_node_changed subscriber. Bumped every
		// `subscriberHeartbeatInterval` (30s) by the goroutine in
		// cmd/gatewayd/nodecache.go.WatchEvictions; the gauge freezes
		// at its last value when the heartbeat goroutine ends
		// (ctx cancel / channel close / initial subscribe failure)
		// so a frozen gauge is the "subscriber died" signal. Series
		// is absent before the first tick — the alert expression
		// handles absent correctly the same way it does for
		// gateway_tls_cert_expiry_seconds (H3, closed in PR #345).
		computeNodeChangedSubscriberAlive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_compute_node_changed_subscriber_alive",
			Help: "Liveness gauge for the LISTEN compute_node_changed subscriber loop in cmd/gatewayd/nodecache.go. Bumped every subscriberHeartbeatInterval (30s) while the subscriber is alive. A frozen or zero gauge means the NodeClientCache is silently out of date and the next compute_nodes UPSERT is invisible to placement. The hook is the observability point; the alert rule + window choice live in ops wiring, out of scope for this PR.",
		}),
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
	// (cmd/gatewayd/main.go:269), so each daemon gets exactly one set
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
	for _, outcome := range []string{"local_snapshot", "local_coldboot"} {
		m.wakeLocality.WithLabelValues(outcome)
	}
	// ADR-024 H3 follow-up (Finding 2): pre-instantiate the closed
	// (result) set on the walk-completeness counter so the §12
	// dashboard panel surfaces from boot. result="partial" is the
	// page signal — pre-instantiating it lets the rule fire on
	// increase without waiting for a first partial event.
	for _, result := range []string{"complete", "partial", "empty"} {
		m.tlsCertExpiryRefresherWalkComplete.WithLabelValues(result)
	}
	reg.MustRegister(m.requests, m.requestDuration, m.wakeLatency, m.wakeQueueWait, m.queueDepth, m.rateLimited, m.accountRateLimited, m.coldBoot, m.tlsCertExpiry, m.tlsCertExpiryByHost, m.tlsCertExpiryRefresherWalkComplete, m.tlsOnDemandDenied, m.wakeLocality, m.computeNodeChangedSubscriberAlive, m.responseBytes)
	return m
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
// 2xx/3xx path only. The counter lives on the gatewayd-local
// registry; the cross-daemon pkg/wire mirror was removed (review
// pass: no production caller existed).
func (m *Metrics) ObserveResponseBytes(appID, plan string, n int64) {
	if m == nil || appID == "" || plan == "" || n <= 0 {
		return
	}
	m.responseBytes.WithLabelValues(appID, plan).Add(float64(n))
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
func (m *Metrics) ObserveColdBoot(appID string, latency time.Duration) {
	m.coldBoot.WithLabelValues(appID).Inc()
	m.wakeLatency.Observe(latency.Seconds())
}

// ObserveWakeQueueWait records how long a request waited in the
// per-app wake queue before the gate released it (single-flight
// coalescing). Nil-safe so WakeGate can call it without branching.
func (m *Metrics) ObserveWakeQueueWait(d time.Duration) {
	if m == nil {
		return
	}
	m.wakeQueueWait.Observe(d.Seconds())
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

// TouchComputeNodeChangedSubscriber bumps the subscriber-liveness gauge
// by 1. Called every `subscriberHeartbeatInterval` (30s, see
// cmd/gatewayd/nodecache.go) by the heartbeat goroutine that mirrors
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
// Nil-safe: cmd/gatewayd tests may pass a nil *Metrics. Production
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
// The sampler in cmd/gatewayd/topn.go runs a local topAccountSet
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
