/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-wide per-app metrics rollup for `GET /v1/apps/metrics?range=`
 * (issue #393). One call replaces N per-app
 * `/v1/apps/{slug}/metrics` calls.
 *
 * `apps` is keyed by `app_slug` so the dashboard can render the
 * rows without a parallel `/v1/apps` lookup. The per-app values
 * are `AppMetricsResponse` — the same shape as the per-app
 * endpoint emits, so the dashboard renders the rollup with one
 * code path.
 *
 * `wake_p95_ms` per app is the FLEET p95 (the underlying
 * `gateway_wake_latency_seconds` histogram is unlabeled — there
 * is no per-app wake histogram).
 *
 * On Prometheus failure the endpoint returns 200 with `apps:
 * null` and `source: "degraded: <reason>"`, matching the
 * per-app contract exactly so the dashboard has one empty-state
 * branch across both endpoints.
 *
 */
export type AppsMetricsResponse = {
  /**
   * Time window that was queried for every per-app rollup row.
   */
  range: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * "prometheus" on success; "degraded: <reason>" otherwise.
   */
  source: string;
  /**
   * RFC3339Nano UTC stamp at which the rollup was assembled.
   */
  as_of: string;
  /**
   * Per-app `AppMetricsResponse`, keyed by app_slug. Null when degraded.
   */
  apps: any | null;
};

