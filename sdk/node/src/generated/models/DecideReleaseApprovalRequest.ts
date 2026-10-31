/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One person's decision on a release. A rejection needs a comment.
 */
export type DecideReleaseApprovalRequest = {
  decision: 'approve' | 'reject';
  /**
   * Recorded with the decision; required to reject.
   */
  comment?: string;
};

