/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One approval decision for the operation's current generation.
 */
export type OperationApprovalRequest = {
  expected_generation: number;
  decision: 'approved' | 'rejected';
  /**
   * Recorded with the decision in the operation history.
   */
  comment?: string;
};

