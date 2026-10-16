/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scheduler observation for one preview target. Enforced means teardown was confirmed and no resident instance remains. Old observations are reported as unavailable.
 */
export type FinancialBudgetStopTarget = {
  app_id: string;
  environment_id?: string;
  state: 'requested' | 'draining' | 'enforced' | 'failed' | 'unavailable';
  observed_at: string;
  resident_count: number;
  last_error?: 'runtime_identity_unavailable' | 'vm_teardown_failed' | 'instance_settlement_failed' | 'scheduler_observation_stale';
};

