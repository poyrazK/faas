/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Notification carrying an initial recovery review and its captured evidence.
 */
export type WorkflowInitialRecoveryReviewEvent = {
  status: 'prepared' | 'submitted' | 'cancelled' | 'expired' | 'stale';
  reason: string;
  occurred_at: string;
  actor_kind: string;
  actor_id: string;
};

