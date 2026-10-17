/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public observation of shared capacity. Underlying unfinished resources remain counted after diagnostic expiry or cancellation requests. No admission authority.
 */
export type BindingVerificationBudget = {
  run_id: string;
  max_concurrency: number;
  active_resources: number;
  waiting_steps: number;
  full: boolean;
};

