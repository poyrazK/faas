/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One route/method's bounded request and estimated-compute share for an immutable deployment. CPU comparisons are advisory, require at least 20 measured requests on both revisions, and remain sensitive to traffic mix.
 */
export type RequestAnalyticsRouteDeploymentObservation = {
  /**
   * Stable UUID identifying the immutable revision that served this route.
   */
  deployment_id: string;
  commit_sha?: string;
  deployment_tag?: string;
  /**
   * RFC 3339 timestamp used to order same-route CPU comparisons.
   */
  deployment_created_at?: string;
  requests: number;
  /**
   * This route/deployment cell's share of all observed app requests in the window.
   */
  request_share_pct: number;
  /**
   * Estimated raw RAM-hour value allocated to this route/deployment cell by request share; not an invoice amount.
   */
  estimated_compute_cost_millicents: number;
  /**
   * Request-weighted mean measured guest CPU time for this route under this deployment; supported Linux one-shot runtimes only.
   */
  guest_cpu_avg_ms?: number;
  guest_cpu_measured_requests: number;
  /**
   * Change in measured mean guest CPU/request from the preceding comparable deployment for this same route/method.
   */
  guest_cpu_change_pct?: number;
  /**
   * Tag, commit SHA, or deployment UUID of the preceding comparable revision.
   */
  guest_cpu_compared_to?: string;
  /**
   * True when measured guest CPU/request rose by at least 25% from the preceding comparable revision.
   */
  guest_cpu_regression: boolean;
};

