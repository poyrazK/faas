/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Defines a customer SLO on an app (ADR-747).
 */
export type CreateSLORequest = {
  /**
   * Unique per app; lowercase letters, digits, dashes and underscores.
   */
  name: string;
  /**
   * availability counts non-5xx responses among 2xx and 5xx; latency counts requests completing within latency_threshold_ms.
   */
  sli: 'availability' | 'latency';
  /**
   * Required for the latency SLI and rejected otherwise; one of the gateway histogram bucket bounds.
   */
  latency_threshold_ms?: 5 | 10 | 25 | 50 | 100 | 250 | 500 | 1000 | 2000 | 5000 | 10000;
  /**
   * Target share of good requests, as a percentage with at most two decimals.
   */
  objective_pct: number;
  /**
   * Rolling window over which the error budget is measured.
   */
  window_days: 7 | 30;
};

