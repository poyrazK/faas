/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRow } from './RouteRow.js';
/**
 * Per-app metrics snapshot (issue #273 / ADR-041). Latencies are
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
   * Share of [45]xx requests in the window.
   */
  error_rate_pct: number;
  /**
   * Share of requests that triggered a cold boot (the WakeGate
   * leader). Followers waiting on the gate see zero cold
   * contribution; their wait is on the §12 dashboard via
   * `gateway_wake_queue_wait_seconds`.
   *
   */
  cold_start_pct: number;
  /**
   * FLEET p95 wake latency (the unlabeled histogram). Labelled as such in the UI.
   */
  wake_p95_ms: number;
  /**
   * Current wake-queue depth (`gateway_queue_depth{app}`).
   * Backs the `queue_backlog_growing` alert preset (ADR-123):
   * comparison `gt 50` over the chosen window. Best-effort:
   * absent on Prometheus failure (the field is `null`); the
   * evaluator's degraded-source contract skips firing
   * rather than guessing.
   *
   */
  queue_depth?: number;
  /**
   * Per-app egress byte delta over the window (informational; not billed). ADR-046. Source: schedd_egress_net_tx_bytes_total{app} (Prom rollup of usage_minutes.net_tx_bytes — PR-2 wires the rollup; until then this field stays 0).
   */
  egress_bytes?: number;
  /**
   * Per-app egress byte delta over the window, gateway-side mirror (informational; not billed). ADR-046 PR-2 / issue #415 PR-2. Source: gateway_egress_tx_bytes_total{app} (Prom rollup; the gatewayd-internal-local per-instance egress ring populates the counter on each raw-stream chunk). Distinct from `egress_bytes` (the schedd-side mirror) so a divergence between the two surfaces immediately on the dashboard. Best-effort: query failure does NOT flip the response to degraded — matches the `egress_bytes` semantics.
   */
  tx_bytes?: number;
  /**
   * Per-route breakdown for opt-in apps (ADR-093). Absent when
   * `route_metrics_enabled` is false on the app — the dashboard
   * distinguishes "feature off" (field absent) from "feature
   * on, no traffic" (empty array). Each row is the bounded
   * detail from the gatewayd-internal in-memory reader: max
   * 50 distinct routes + the `__route_other__` wildcard-path
   * overflow bucket. The route label is method + raw path
   * (pre-rewrite, ADR-093 D6).
   *
   */
  routes?: Array<RouteRow>;
};

