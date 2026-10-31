/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact admitted recovery item and generation reviewed by an operator. Preview is advisory and commit rechecks eligibility. Operator identity is resolved from authentication.
 */
export type WorkflowRecoveryCancellationRequest = {
  /**
   * Optional stable action identifier. Apply retains the original outcome including refusals; reusing it with changed arguments conflicts. Preview never writes a receipt.
   */
  request_id?: string;
  position: number;
  expected_resume_count: number;
  /**
   * Nonblank UTF-8 reason without control characters. Limited to 512 bytes.
   */
  reason: string;
};

