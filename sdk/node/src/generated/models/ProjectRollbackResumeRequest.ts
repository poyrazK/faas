/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Require a reviewed plan when resuming a paused project rollback or undo.
 */
export type ProjectRollbackResumeRequest = {
  /**
   * Resume plan hash from a runnable preview of the same paused operation.
   */
  expected_plan_hash?: string;
};

