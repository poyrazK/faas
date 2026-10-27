/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable review decision with trusted authenticated actor identity.
 */
export type WorkflowRecoveryReviewDecision = {
  decision: 'approved' | 'dismissed';
  reason: string;
  decided_at: string;
  actor_kind: string;
  actor_id: string;
};

