/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One deployment's estimated share of app raw compute value, allocated by its observed request share, with optional measured CPU/request comparison. CPU regression comparisons are advisory, traffic-mix sensitive, and only available for supported runtimes.
 */
export type RequestAnalyticsDeploymentCost = {
  /**
   * Immutable deployment UUID.
   */
  deployment_id: string;
  commit_sha?: string;
  deployment_tag?: string;
  deployment_created_at?: string;
  requests: number;
  request_share_pct: number;
  estimated_compute_cost_millicents: number;
  /**
   * Mean measured guest CPU time per request in the analytics window; only populated for supported Linux one-shot runtimes.
   */
  guest_cpu_avg_ms?: number;
  /**
   * Request-weighted number of requests with measured guest CPU data.
   */
  guest_cpu_measured_requests: number;
  /**
   * Percentage change in mean guest CPU time from the earlier comparable deployment in this analytics window. Only computed when both deployments have at least 20 measured requests and the baseline is non-zero.
   */
  guest_cpu_change_pct?: number;
  /**
   * Tag, commit SHA, or deployment UUID for the earlier measured deployment used as the CPU comparison baseline.
   */
  guest_cpu_compared_to?: string;
  /**
   * True when measured guest CPU/request increased by at least 25% from the comparison baseline. Advisory and sensitive to traffic mix.
   */
  guest_cpu_regression: boolean;
};

