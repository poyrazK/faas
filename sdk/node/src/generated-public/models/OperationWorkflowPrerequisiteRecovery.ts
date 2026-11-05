/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-owned steps to restore a missing or unusable retained prerequisite. Derived from the pinned selector; absent for verified evidence. This is guidance and does not establish that the producer operation is currently executable. Steps are ordered perform_operation then publish_milestone then recheck_readiness. Publication must use the same ownership boundary and workflow instance and contract version. Freshness limits still apply.
 */
export type OperationWorkflowPrerequisiteRecovery = {
  reason: 'awaiting_verification' | 'expired' | 'invalidated';
  operation: string;
  milestone: string;
  /**
   * Exclusive occurrence threshold from the invalidating fact or expired prerequisite. Never fabricate an occurrence time to satisfy this threshold.
   */
  after_occurred_at?: string;
  steps: Array<'perform_operation' | 'publish_milestone' | 'recheck_readiness'>;
};

