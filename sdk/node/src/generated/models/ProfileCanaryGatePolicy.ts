/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in policy requiring consecutive qualified route CPU/request comparisons before a canary stage advances.
 */
export type ProfileCanaryGatePolicy = {
  confirmations: number;
  /**
   * Must cover warmup plus all confirmation windows and ingestion grace.
   */
  timeout_seconds: number;
  /**
   * Continuing is an explicit acceptance of inconclusive profile evidence. Confirmed regressions remain held.
   */
  on_timeout: 'hold' | 'continue';
  /**
   * Worker-only opt-in recovery for request-mode canaries to the exact current stable predecessor. Service recovery uses the checked handoff flow.
   */
  auto_rollback: boolean;
};

