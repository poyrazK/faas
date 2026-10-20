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
   * How the instance started, for example restore or cold_boot.
   */
  method?: string;
  /**
   * Milliseconds from boot start to ready; omitted until the wake completed.
   */
  ready_in_ms?: number;
};

