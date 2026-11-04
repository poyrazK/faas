/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Latest retained-state retry checks, observed after the original failure. No inputs or outputs are retained; this is not failure-time proof or an admission guarantee.
 */
export type WorkflowRecoveryFailureEligibility = {
  observed_at: string;
  parent_job_id?: string;
  parent_position?: number;
  reason: string;
  blockers: Array<string>;
};

