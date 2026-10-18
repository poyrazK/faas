/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Error-budget position of one SLO, returned by GET /v1/apps/{slug}/slos/{id} (ADR-747).
 */
export type SLOStatus = {
  /**
   * Start of the rolling window: the later of window_days ago and the hour the SLO was created.
   */
  window_start: string;
  /**
   * Completed hours meterd has recorded in the window.
   */
  hours_recorded: number;
  /**
   * Completed hours in the window; fewer recorded hours means history is still being backfilled or was lost.
   */
  hours_expected: number;
  /**
   * Requests that met the SLI in the recorded hours.
   */
  good: number;
  /**
   * Requests the SLI counted in the recorded hours.
   */
  total: number;
  /**
   * good / total as a percentage; null when the window had no requests.
   */
  attainment_pct: number | null;
  /**
   * Share of the error budget left: 100 when nothing failed, 0 when it is spent, negative once the objective is missed.
   */
  budget_remaining_pct: number | null;
  /**
   * Budget burn over the last hour, live from Prometheus: 1 spends exactly the budget over the window.
   */
  burn_rate_1h: number | null;
  /**
   * Budget burn over the last six hours, live from Prometheus.
   */
  burn_rate_6h: number | null;
  /**
   * prometheus, or degraded: <reason> when budget history or burn rates are unavailable.
   */
  source: string;
};

