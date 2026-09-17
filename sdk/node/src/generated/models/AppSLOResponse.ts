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
 * `gb_hours`) are net-new per the issue. `wake_queue_p95_ms` is
 * nullable and paired with `wake_queue_sample_status`; the value is
 * unavailable until the underlying histogram has a tenant label.
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
   * Share of 5xx requests in the window for this app. Client-caused 4xx responses do not consume availability budget.
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
   * Tenant-scoped wake-queue p95, or null when the source is unavailable or has no sample.
   */
  wake_queue_p95_ms: number | null;
  /**
   * Availability of wake_queue_p95_ms. unavailable means tenant-scoped telemetry is not emitted.
   */
  wake_queue_sample_status: 'available' | 'no_sample' | 'unavailable';
  requests_total: number;
  /**
   * Per-app rate-limit count over the window.
   */
  throttled_total: number;
};

