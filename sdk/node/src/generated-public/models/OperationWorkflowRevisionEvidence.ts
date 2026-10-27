/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Links to reported decision or milestone facts and explicitly unverified expected verification facts. These references do not establish a cause for the revision.
 */
export type OperationWorkflowRevisionEvidence = {
  kind: 'milestone' | 'decision' | 'verification_expected';
  operation_id: string;
  milestone_id: string;
  milestone?: string;
};

