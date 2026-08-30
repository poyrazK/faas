// Package presetwhy is the central explanation catalog for the
// dashboard's "What does this alert mean?" panel (issue #1233 /
// ADR-123 PR-C commit 3). Each entry maps a stable catalog preset
// name to the customer-facing Title/Hint/Why/Fix prose that the
// dashboard's per-card <details> panel renders, plus an optional
// docs URL override and an optional per-preset threshold-templated
// renderer.
//
// Why a central package: the prose is the load-bearing UX for
// retention; it must be reviewable in one place, table-driven tested,
// and tripwire-protected. Detection sites (the dashboard's preset
// grid) attach Title/Hint/Why/Fix via Decorate from the With chain —
// but the prose body lives here so the wording is consistent across
// every card.
//
// Modelled on pkg/whycopy (the error-explanations analog). Whycopy
// is keyed by RFC 7807 stable code; alert preset names are domain
// catalog entries, not error codes. Splitting into a sibling package
// keeps the tripwire domains separate — the catalog is the single
// source of truth for the alert_preset grid, just like whycopy is the
// single source of truth for the problem Documents.
//
// Decorate is the single entry point; it copies the catalog row into
// a fresh *Explanation and returns it. Detection sites should call
// Decorate right after fetching the catalog row so the dashboard
// receives the full Title/Hint/Why/Fix block on every card.
package presetwhy

import (
	"fmt"
	"sort"
)

// Explanation is one row of the catalog. The fields are the
// customer-facing prose for one alert preset; Decorate returns a
// fresh *Explanation populated from the catalog row.
type Explanation struct {
	// Title is the one-line summary shown bold at the top of the
	// <details> panel. Mirrors whycopy.Render.Title — short,
	// declarative, no period.
	Title string
	// Hint is the single short next-action line shown immediately
	// under Title. Mirrors SecretHint's shape — one line.
	Hint string
	// Why is the human-readable cause (≤ 512 bytes; CLI tripwire
	// enforces). May be multi-line.
	Why string
	// Fix is the prescriptive remediation (1-3 lines, ≤ 512 bytes).
	Fix string
	// DocsURL is the runbook URL the <a href> in the panel links
	// to. Empty = no link rendered.
	DocsURL string
}

// ObservedRenderer, when non-nil for a row, returns the (why, fix)
// pair templated with the observed value at dashboard-render time.
// The renderer is called by Decorate when the caller supplies an
// observed value; nil = use the static Why/Fix verbatim. The renderer
// must not mutate the catalog, so it returns strings, not *Explanation.
type ObservedRenderer func(observed float64) (why, fix string)

// row is the catalog's internal row. It pairs the public fields
// with an optional Observed renderer.
type row struct {
	Explanation
	Observed ObservedRenderer
}

// catalog is the static explanation table. Order does not matter;
// lookup is map-based. Adding a new preset name → add a row here.
// Tripwire: TestEveryPresetHasPresetwhyEntry in
// cmd/gregale/lint_tripwires_test.go pins 1:1 membership against the
// 8 catalog rows in migrations/00418_alert_presets_seed.sql.
var catalog = map[string]row{
	// ─── Originally enabled (3) — surface their prose for parity ────
	"error_rate_2pct": {
		Explanation: Explanation{
			Title:   "Error rate exceeds 2%",
			Hint:    "your app is failing >2% of requests over the last 15 minutes",
			Why:     "the gateway has counted non-2xx responses (4xx + 5xx) for your app over a rolling 15-minute window; the percentage crossed the 2% threshold",
			Fix:     "• check `gregale logs <slug>` for the recent 5xx spike\n• if the failures are 5xx, the upstream service is the most common cause (DB, third-party API, cache)\n• if 4xx, a client integration broke — check the most common 4xx code in the gateway dashboard",
			DocsURL: "/docs/alerts/error-rate",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("the gateway counted non-2xx responses for your app over a rolling 15-minute window; the rate landed at %.2f%%, past the 2%% threshold", observed),
				fmt.Sprintf("• check `gregale logs <slug>` for the recent 5xx spike (%.2f%% is well past the threshold)\n• if the failures are 5xx, the upstream service is the most common cause (DB, third-party API, cache)\n• if 4xx, a client integration broke — check the most common 4xx code in the gateway dashboard", observed)
		},
	},
	"p95_latency_1s": {
		Explanation: Explanation{
			Title:   "P95 latency exceeds 1 second",
			Hint:    "your slowest 5% of requests are slower than 1 second over the last 15 minutes",
			Why:     "the gateway measured the 95th-percentile response time for your app over a rolling 15-minute window; the p95 latency crossed the 1000 ms threshold",
			Fix:     "• check the latency breakdown in the gateway dashboard — most apps have 1-2 slow paths\n• common culprits: cold-start on a parked instance, a downstream HTTP call without a timeout, a sync DB query on a hot path\n• if you can't trim the latency, raise the threshold via a custom rule (PR-D: per-preset override)",
			DocsURL: "/docs/alerts/latency",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("the gateway measured the 95th-percentile response time for your app over a rolling 15-minute window; the p95 latency landed at %.0f ms, past the 1000 ms threshold", observed),
				fmt.Sprintf("• check the latency breakdown in the gateway dashboard — your p95 is %.0f ms, well past the threshold\n• common culprits: cold-start on a parked instance, a downstream HTTP call without a timeout, a sync DB query on a hot path\n• if you can't trim the latency, raise the threshold via a custom rule (PR-D: per-preset override)", observed)
		},
	},
	"cold_start_10pct": {
		Explanation: Explanation{
			Title:   "Cold starts exceed 10% of traffic",
			Hint:    "more than 10% of your requests woke a parked instance in the last hour",
			Why:     "the gateway counted wake events versus total requests for your app over a rolling 1-hour window; the cold-start rate crossed the 10% threshold. Cold starts add ~250 ms of latency while the microVM boots",
			Fix:     "• if traffic is bursty by design (cron, batch), this is expected — consider warming with a low-frequency probe\n• if traffic is steady but under your plan's idle timeout, raise the timeout (api.IdleTimeoutSeconds in pkg/api/limits.go) — but you'll pay for the resident RAM\n• if traffic is steady AND under the timeout, your idle window is too aggressive",
			DocsURL: "/docs/alerts/cold-starts",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("the gateway counted wake events versus total requests for your app over a rolling 1-hour window; the cold-start rate landed at %.1f%%, past the 10%% threshold. Cold starts add ~250 ms of latency while the microVM boots", observed),
				fmt.Sprintf("• if traffic is bursty by design (cron, batch), this is expected — consider warming with a low-frequency probe\n• if traffic is steady but under your plan's idle timeout, raise the timeout (api.IdleTimeoutSeconds in pkg/api/limits.go) — but you'll pay for the resident RAM\n• if traffic is steady AND under the timeout, your idle window is too aggressive (you saw %.1f%% cold starts — that's a wake-vs-resident imbalance)", observed)
		},
	},
	// ─── Newly enabled signals (5, ADR-123 PR-B + PR-C commit 1) ────
	"api_down": {
		Explanation: Explanation{
			Title:   "API is down",
			Hint:    "the meterd probe could not reach your app over the last 5 minutes",
			Why:     "meterd runs a lightweight reachability probe (HEAD /healthz or a synthetic GET) against each live instance every 30 s; the probe recorded < 1.0 (i.e. unreachable) for at least one instance in the last 5 minutes",
			Fix:     "• check `gregale logs <slug>` for the boot or runtime errors\n• if the VM booted but /healthz is failing, see CodeAppHealthzUnauthorized / CodeAppStartupTimeout in the error-explanations catalog\n• if the VM never reached RUNNING, check the recent deploy (`gregale deploys status <id>`) — a failed deploy is the most common cause",
			DocsURL: "/docs/runbooks/FaasApiDown",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("meterd ran the reachability probe and recorded %.2f (below the 1.0 reachable threshold) for at least one instance in the last 5 minutes", observed),
				"• check `gregale logs <slug>` for the boot or runtime errors\n• if the VM booted but /healthz is failing, see CodeAppHealthzUnauthorized / CodeAppStartupTimeout in the error-explanations catalog\n• if the VM never reached RUNNING, check the recent deploy (`gregale deploys status <id>`) — a failed deploy is the most common cause"
		},
	},
	"spend_eur_20": {
		Explanation: Explanation{
			Title:   "Daily spend exceeds €20",
			Hint:    "your account's 24-hour rolling spend crossed the €20 threshold",
			Why:     "meterd sums the per-instance RAM-seconds cost (plan RAM + 8 MB overhead, billed at the plan rate) for the last 24 hours; the total crossed the €20 threshold. Billing is plan RAM + 8 MB per running second (§4.7) — NOT sampled RSS",
			Fix:     "• if the spend is unexpected, check the instances panel — a runaway loop or ungraceful crash can keep waking instances\n• consider raising idle timeout to park instances faster\n• for predictable workloads, the Scale plan's GB-h pool is the cheapest path (1500 GB-h included)",
			DocsURL: "/docs/runbooks/FaasSpendEur20",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("meterd summed the per-instance RAM-seconds cost for the last 24 hours; the total landed at €%.2f, past the €20 threshold", observed),
				fmt.Sprintf("• if the spend is unexpected (€%.2f is unusual for this app shape), check the instances panel — a runaway loop or ungraceful crash can keep waking instances\n• consider raising idle timeout to park instances faster\n• for predictable workloads, the Scale plan's GB-h pool is the cheapest path (1500 GB-h included)", observed)
		},
	},
	"deploy_failed": {
		Explanation: Explanation{
			Title:   "Recent deployment failed",
			Hint:    "at least one deployment to this app failed in the last hour",
			Why:     "apid stamps apid_deployment_failed_total{service=\"deploy\"} every time a deployment reaches the failed terminal state; the counter went non-zero over the rolling 1-hour window. The most common causes are build failures (CodeStageImageBuildTimeout, CodeStageImageBuildOOM, CodeDepInstallFailed) and runtime failures during the readiness probe",
			Fix:     "• check the latest deployment: `gregale deploys status <id>` shows the failing stage + error code\n• if it's a build failure, the error-explanations catalog entry for that Code… has the remediation\n• if it's a readiness failure, the boot log is in `gregale logs <slug>`",
			DocsURL: "/docs/runbooks/FaasDeployFailed",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("apid stamped apid_deployment_failed_total and the counter landed at %.0f failed deploys in the rolling 1-hour window", observed),
				"• check the latest deployment: `gregale deploys status <id>` shows the failing stage + error code\n• if it's a build failure, the error-explanations catalog entry for that Code… has the remediation\n• if it's a readiness failure, the boot log is in `gregale logs <slug>`"
		},
	},
	"cert_expiring_14d": {
		Explanation: Explanation{
			Title:   "TLS certificate expires within 14 days",
			Hint:    "your custom-domain certificate will expire in less than 14 days",
			Why:     "apid inspects the certificate on every custom-domain renewal scan; the notAfter timestamp is less than 14 days (1209600 seconds) away. Renewals normally run automatically via the cert engine (ADR-099) — this alert fires when auto-renew failed for 7+ days",
			Fix:     "• check the renewal log: `gregale certs status <hostname>` shows the last attempt + ACME challenge outcome\n• if the ACME challenge is failing, the most common cause is a CAA record blocking issuance or a DNS propagation delay\n• if renewal is genuinely blocked, replace the cert manually (`gregale certs rotate <hostname>`)",
			DocsURL: "/docs/runbooks/FaasTLSCertExpiryPage",
		},
		Observed: func(observed float64) (why, fix string) {
			days := observed / 86400
			return fmt.Sprintf("apid inspected the certificate and the notAfter timestamp is %.0f seconds (%.1f days) away, under the 14-day threshold", observed, days),
				fmt.Sprintf("• check the renewal log: `gregale certs status <hostname>` shows the last attempt + ACME challenge outcome (cert has %.1f days left)\n• if the ACME challenge is failing, the most common cause is a CAA record blocking issuance or a DNS propagation delay\n• if renewal is genuinely blocked, replace the cert manually (`gregale certs rotate <hostname>`)", days)
		},
	},
	"queue_backlog_growing": {
		Explanation: Explanation{
			Title:   "Gateway wake queue is backlogged",
			Hint:    "the gateway wake queue depth exceeded 50 for this app over the last 15 minutes",
			Why:     "gatewayd-internal stamps gateway_queue_depth{app=...} every time it accepts a wake request before a live instance is available; the depth stayed above 50 for at least 3 consecutive scrapes (15-minute window at 30 s scrape interval). Note: this metric does NOT carry an account_id label, so it does NOT contribute to the FaasAlertPresetAnyFiringAccount correlation rollup — operators see it under the queue_backlog_growing family only",
			Fix:     "• if the queue is rising steadily, your max_concurrency(plan) is the ceiling — each pending wake blocks until an instance frees up\n• if traffic is bursty, the wake queue caps at 512/30 s — requests beyond that get 503\n• consider lowering the idle timeout to free instances faster (trade-off: more cold starts)",
			DocsURL: "/docs/runbooks/FaasGatewayQueueBacklogGrowing",
		},
		Observed: func(observed float64) (why, fix string) {
			return fmt.Sprintf("gatewayd-internal stamped gateway_queue_depth and the depth landed at %.0f, past the 50 threshold (and stayed there for at least 3 consecutive scrapes)", observed),
				fmt.Sprintf("• if the queue is rising steadily (you saw %.0f pending wakes), your max_concurrency(plan) is the ceiling — each pending wake blocks until an instance frees up\n• if traffic is bursty, the wake queue caps at 512/30 s — requests beyond that get 503\n• consider lowering the idle timeout to free instances faster (trade-off: more cold starts)", observed)
		},
	},
}

// Decorate copies the catalog row for name into a fresh *Explanation.
// When observed is non-zero and the catalog row has an Observed
// renderer, the Why/Fix fields are templated with the observed value;
// otherwise the static Why/Fix is used verbatim. Returns nil when
// name has no catalog row — the dashboard template uses `with` to
// skip the panel cleanly when the function returns nil.
//
// Decorate is the single entry point; the catalog is the single
// source of truth for customer-facing prose. A future preset name
// added to migrations/00418_alert_presets_seed.sql without a row
// here is caught by TestEveryPresetHasPresetwhyEntry in
// cmd/gregale/lint_tripwires_test.go.
func Decorate(name string, observed float64) *Explanation {
	row, ok := catalog[name]
	if !ok {
		return nil
	}
	out := &Explanation{
		Title:   row.Title,
		Hint:    row.Hint,
		Why:     row.Why,
		Fix:     row.Fix,
		DocsURL: row.DocsURL,
	}
	if observed != 0 && row.Observed != nil {
		why, fix := row.Observed(observed)
		if why != "" {
			out.Why = why
		}
		if fix != "" {
			out.Fix = fix
		}
	}
	return out
}

// Codes returns the sorted list of preset names that have a catalog
// row. Used by TestEveryPresetHasPresetwhyEntry to assert 1:1
// membership with the catalog seed in
// migrations/00418_alert_presets_seed.sql.
func Codes() []string {
	out := make([]string, 0, len(catalog))
	for name := range catalog {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the catalog row for name (used by tests). Returns
// (Explanation{}, false) when the name has no row.
func Lookup(name string) (Explanation, bool) {
	r, ok := catalog[name]
	return r.Explanation, ok
}
