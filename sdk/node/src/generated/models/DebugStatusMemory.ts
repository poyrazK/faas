/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Present when the app's guests ran short of memory in the last day: the rollup's low point and the RAM that would have kept 30% of the guest's memory free (ADR-20261102124306). Released per account by the app_health_memory platform flag.
 */
export type DebugStatusMemory = {
  episodes: number;
  lowest_available_mb: number;
  total_mb: number;
  peak_anon_mb: number;
  last_at: string;
  /**
   * The app's ram_mb.
   */
  current_mb: number;
  /**
   * RAM that would have kept 30% free, in 64 MB steps; always above current_mb.
   */
  suggested_mb: number;
  plan: string;
  /**
   * The most RAM the account's plan allows.
   */
  plan_mb: number;
  fits_plan: boolean;
  /**
   * The smallest plan that allows suggested_mb, when fits_plan is false; absent when no plan does.
   */
  upgrade_plan?: string;
  upgrade_mb?: number;
  /**
   * The same suggestion the app health memory check gives.
   */
  advice: string;
};

