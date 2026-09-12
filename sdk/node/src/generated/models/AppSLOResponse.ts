/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SLODuration } from './SLODuration.js';
/**
 * Per-app SLO panel returned by `GET /v1/apps/{slug}/slo?window=`
 * (issue #696 / ADR-082). Distinct from `AppMetricsResponse`
 * (issue #273 / ADR-042): the SLO surface is a fixed-window
 * (1h/24h/7d) summary of the customer-facing SLO signals,
 * not a 5m slice for the dashboard. The fields overlap only
 * on latency percentiles, error rate, and cold-boot rate — the
 * remaining fields (`throttled_total`, `instance_hours`,
 * `gb_hours`) are net-new per the issue. `wake_queue_p95_ms`
 * remains in the wire shape for compatibility and is zero until
 * the underlying histogram has an app label.
 *
 * On Prometheus failure the endpoint returns 200 with
 * zeroed fields and `source: "degraded: <reason>"`. When
 * Postgres is down but the PromQL pass succeeded, only
 * `instance_hours` / `gb_hours` are zeroed and `source` is
 * `"degraded: postgres unavailable"`.
 *
 */
export type AppSLOResponse = {
  app_id: string;
  app_slug: string;
  /**
   * Echoed SLO window, e.g. `24h`.
   */
  window: '1h' | '24h' | '7d';
  /**
   * "prometheus" on success; "degraded: <reason>" otherwise. Per-app shape: when Postgres fails only instance_hours/gb_hours are zeroed.
   */
  source: string;
  /**
   * RFC3339Nano UTC stamp at which the SLO panel was assembled.
   */
  as_of: string;
  request_duration: SLODuration;
  /**
   * Share of [45]xx requests in the window for this app.
   */
  error_rate_pct: number;
  /**
   * Share of requests that triggered a cold boot.
   */
  cold_boot_rate_pct: number;
  /**
   * Sum of instance × minute / 60 over the window (from `usage_minutes`).
   */
  instance_hours: number;
  /**
   * Sum of mb_seconds / 3600 / 1024 over the window (from `usage_minutes`).
   */
  gb_hours: number;
  /**
   * Reserved compatibility field. Zero until the wake-queue histogram can be scoped to this app.
   */
  wake_queue_p95_ms: number;
  requests_total: number;
  /**
   * Per-app rate-limit count over the window.
   */
  throttled_total: number;
};

