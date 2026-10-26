/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * How a matched request's app was woken, when its wake history is still retained. Omitted for warm requests.
 */
export type AccountTraceWake = {
  wake_id: string;
  /**
   * How the instance started, for example restore or cold_boot. A planned restore that fell back to a cold boot reads cold_boot.
   */
  method?: string;
  /**
   * Milliseconds from boot start to ready; omitted until the wake completed.
   */
  ready_in_ms?: number;
  /**
   * What schedd planned for this wake when it differs from method, such as restore for a restore that fell back to a cold boot.
   */
  planned_method?: string;
  /**
   * Closed reason for a restore that fell back to a cold boot, when one is reported.
   */
  restore_fallback_reason?: 'after_restore_failed';
};

