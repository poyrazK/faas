/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Frozen per-target explanation including eligibility refusals and policy suppression. The resume count identifies the reviewed admitted generation.
 */
export type WorkflowRecoveryPolicyDecision = {
  /**
   * Exact run status observed at candidate evaluation when available.
   */
  failure_status?: string;
  /**
   * Eligibility result before policy preparation limits.
   */
  preflight_outcome?: string;
  /**
   * Frozen preflight blocker codes.
   */
  preflight_blockers?: Array<string>;
  position: number;
  run_id: string;
  resume_count: number;
  trigger: string;
  action: string;
  outcome: string;
};

