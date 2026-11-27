/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Completeness and freshness of authoritative retained meter evidence.
 */
export type FinancialMeterCoverage = {
  complete: boolean;
  fresh: boolean;
  expected_minutes: number;
  complete_minutes: number;
  /**
   * Enough of the period was sampled to forecast it, either every minute or at least 99% of the elapsed minutes with the last hour complete. A forecast over a partly sampled period uses the covered_time_run_rate_v1 method.
   */
  projectable?: boolean;
  unpriced_quantity: number;
  non_billable_quantity: number;
  reasons: Array<string>;
};

