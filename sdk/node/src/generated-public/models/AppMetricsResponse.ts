/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRow } from './RouteRow.js';
/**
 * Per-app metrics snapshot. Latencies are
 * milliseconds for the 2xx class only; failures surface as
 * `error_rate_pct`. `wake_p95_ms` is the FLEET p95 — the
 * underlying `gateway_wake_latency_seconds` histogram is
 * unlabeled. On Prometheus failure the endpoint returns 200 with
 * zeroed fields and `source: "degraded: <reason>"`.
 *
 */
export type AppMetricsResponse = {
  app_id: string;
  /**
   * Echoed window.
   */
  range: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * "prometheus" on success, "degraded: <reason>" otherwise.
   */
  source: string;
  /**
   * RFC3339Nano UTC.
   */
  as_of: string;
  /**
   * Share of requests in the window. Drives the dashboard empty state.
   */
  request_count: number;
  /**
   * p50 of `gateway_request_duration_seconds_bucket{class="2xx"}` over the window, in ms.
   */
  latency_p50_ms: number;
  /**
   * p95 over 2xx traffic in the window, in ms.
   */
  latency_p95_ms: number;
  /**
   * p99 over 2xx traffic in the window, in ms.
   */
  latency_p99_ms: number;
  /**
   * Share of 5xx requests in the window. Client-caused 4xx responses do not consume availability budget.
   */
  error_rate_pct: number;
  /**
   * Share of requests that triggered a cold boot (the WakeGate
   * leader). Followers waiting on the gate see zero cold
   * contribution; their wait is on the dashboard via
   * `gateway_wake_queue_wait_seconds`.
   *
   */
  cold_start_pct: number;
  /**
   * FLEET p95 wake latency (the unlabeled histogram). Labelled as such in the UI.
   */
  wake_p95_ms: number;
  /**
   * Number of fleet wakes wake_p95_ms was computed from. Below 20 the p95 is not meaningful and clients show it as unavailable. Omitted when the count is unknown.
   */
  wake_samples?: number;
  /**
   * Current wake-queue depth
   * (`gateway_queue_depth{app, account_id}` — `account_id`
   * admitted via accountLabelSet, overflow=`__other__`,
   * added). Backs the `queue_backlog_growing`
   * alert preset: comparison `gt 50` over the
   * chosen window. Best-effort: absent on Prometheus
   * failure (the field is `null`); the evaluator's
   * degraded-source contract skips firing rather than
   * guessing.
   *
   */
  queue_depth?: number;
  /**
   * Per-app egress byte delta over the window (informational; not billed). It is not collected yet and stays 0.
   */
  egress_bytes?: number;
  /**
   * Per-app egress byte delta over the window, gateway-side mirror (informational; not billed). Source: gateway_egress_tx_bytes_total{app} (Prom rollup; the gateway-local per-instance egress ring populates the counter on each raw-stream chunk). Distinct from `egress_bytes` (the scheduler-side mirror) so a divergence between the two surfaces immediately on the dashboard. Best-effort: query failure does NOT flip the response to degraded — matches the `egress_bytes` semantics.
   */
  tx_bytes?: number;
  /**
   * Per-route breakdown for opt-in apps. Absent when
   * `route_metrics_enabled` is false on the app — the dashboard
   * distinguishes "feature off" (field absent) from "feature
   * on, no traffic" (empty array). Each row is the bounded
   * detail from the gateway in-memory reader: max
   * 50 distinct routes + the `__route_other__` wildcard-path
   * overflow bucket. The route label is method + declared template
   * when available, otherwise a best-effort inferred public path.
   *
   */
  routes?: Array<RouteRow>;
  /**
   * Count of `wake.boot_started` events the scheduler recorded for
   * this app in the trailing 24 hours. Sourced from the
   * `events` table via the events_wake_id_idx jsonb expression
   * index — sub-second on a healthy app.
   * Best-effort: 0 on a degraded store call, an empty app, or
   * when the events table predates per-app wake events
   * (older wake events carry no app ID and count as 0). The
   * dashboard surfaces this as the "wakes today" line item;
   * combined with `cold_start_pct` it answers "is my app
   * wake-bound or sleep-bound". Field absent on Free
   * (the per-app dashboard is Hobby+ only —
   *
   */
  wakes_24h?: number;
  /**
   * Share of cache-eligible requests served from
   * `gateway_response_cache` over the selected
   * window. The value is sourced from the additive
   * `gateway_response_cache_app_total{app,outcome}` metric
   * and counts only `hit / (hit + miss)`. Absence means the
   * app had no cache telemetry in the window, not an observed
   * 0% hit rate.
   *
   */
  cache_hit_rate_pct?: number;
  /**
   * API-availability error budget remaining over the selected
   * metrics window, using the current 99.5% API-availability
   * target. 100 means the full budget remains and 0 means it is
   * exhausted. Absence means the app had no requests in the
   * window, not an observed exhausted budget.
   *
   */
  error_budget_pct?: number;
};

